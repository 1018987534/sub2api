package service

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// PlazaOfficialPricing 模型广场展示用的 LiteLLM 官方参考价（USD per token）。
// 字段为 nil 表示官方数据中该项缺失（0 视为未配置）。
type PlazaOfficialPricing struct {
	InputPrice        *float64
	OutputPrice       *float64
	CacheWritePrice   *float64 // 5m 缓存写入（= LiteLLM cache_creation）
	CacheWrite1hPrice *float64 // 1h 缓存写入（LiteLLM cache_creation_above_1hr）
	CacheReadPrice    *float64
}

// PlazaModel 模型广场中单个模型条目：渠道定价 + 官方参考价。
type PlazaModel struct {
	Name            string
	Platform        string
	Pricing         *ChannelModelPricing
	OfficialPricing *PlazaOfficialPricing
	officialModel   string
}

// PlazaGroup 模型广场中以分组为顶层的条目。
//
// 与 AvailableGroupRef 相比多了 Description 与 Models；Models 来自该分组关联渠道的
// 支持模型（普通分组按分组平台隔离，Composite 分组展开关联渠道已配置的
// 具体平台），与「可用渠道」页口径一致。
type PlazaGroup struct {
	ID                 int64
	Name               string
	Description        string
	Platform           string
	SubscriptionType   string
	RateMultiplier     float64
	PeakRateEnabled    bool
	PeakStart          string
	PeakEnd            string
	PeakRateMultiplier float64
	IsExclusive        bool
	// 图片按次实付倍率：ImageRateIndependent 为 true 时，图片计费模型的实付
	// = 档位价 × ImageRateMultiplier，不乘分组/用户专属倍率（与计费口径一致）。
	ImageRateIndependent bool
	ImageRateMultiplier  float64
	Models               []PlazaModel
}

type plazaModelKey struct {
	platform string
	name     string
}

// plazaInventoryModel is an internal capability edge. Live probes produce an
// account-facing Name plus the final UpstreamModel. Usage fallback rows are
// already customer-facing and set customerFacing so channel mapping is not
// applied a second time.
type plazaInventoryModel struct {
	Name           string
	Platform       string
	UpstreamModel  string
	customerFacing bool
}

// ListPlazaGroups 返回模型广场数据：每个活跃分组附带当前可调度账号
// 的模型能力与官方参考定价。
//
// accountRepo 与上游模型读取器注入时，模型清单来自定时刷新的真实上游能力；
// 上游不可枚举时才回退到近 24 小时成功用量，避免继续依赖静态分组模型。其余聚合口径与
// ListAvailable 一致（Active 渠道、SupportedModels ∪ 全局定价回落、平台隔离），
// 仅把顶层从渠道换成分组：
//   - 渠道按 lower(name) 排序后遍历，保证同名模型去重结果确定；
//   - 同分组同名模型「先见者胜」，仅当已存条目无定价而新条目有定价时升级替换；
//   - 图片计费模型的档位价按实收口径合成（分组图片价 > 渠道档位价 > 渠道默认按次价，
//     见 plazaImageDisplayPricing）；
//   - 每个模型附带 LiteLLM 官方参考价（查不到为 nil）；
//   - 只返回 Models 非空的分组；分组按 RateMultiplier 升序（同倍率按名称），
//     组内模型按名称排序。
//
// 可见性过滤（专属分组）不在此层做，由 handler 按登录态裁剪。
func (s *ChannelService) ListPlazaGroups(ctx context.Context) ([]PlazaGroup, error) {
	channels, err := s.repo.ListAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("list channels: %w", err)
	}
	groups, err := s.groupRepo.ListActive(ctx)
	if err != nil {
		return nil, fmt.Errorf("list active groups: %w", err)
	}

	// The runtime cache resolves one channel per group with the last repository
	// row winning. Preserve that association before sorting a copy for stable
	// legacy aggregation.
	channelByGroup := make(map[int64]*Channel, len(groups))
	for i := range channels {
		if channels[i].Status != StatusActive {
			continue
		}
		channels[i].normalizeBillingModelSource()
		for _, groupID := range channels[i].GroupIDs {
			channelByGroup[groupID] = &channels[i]
		}
	}
	sortedChannels := append([]Channel(nil), channels...)
	sort.SliceStable(sortedChannels, func(i, j int) bool {
		return strings.ToLower(sortedChannels[i].Name) < strings.ToLower(sortedChannels[j].Name)
	})

	byGroup := make(map[int64]*PlazaGroup, len(groups))
	groupEnt := make(map[int64]*Group, len(groups))
	order := make([]int64, 0, len(groups))
	for i := range groups {
		g := &groups[i]
		byGroup[g.ID] = &PlazaGroup{
			ID:                   g.ID,
			Name:                 g.Name,
			Description:          g.Description,
			Platform:             g.Platform,
			SubscriptionType:     g.SubscriptionType,
			RateMultiplier:       g.RateMultiplier,
			PeakRateEnabled:      g.PeakRateEnabled,
			PeakStart:            g.PeakStart,
			PeakEnd:              g.PeakEnd,
			PeakRateMultiplier:   g.PeakRateMultiplier,
			IsExclusive:          g.IsExclusive,
			ImageRateIndependent: g.ImageRateIndependent,
			ImageRateMultiplier:  g.ImageRateMultiplier,
		}
		groupEnt[g.ID] = g
		order = append(order, g.ID)
	}
	actualModels := s.liveSchedulableModelsByGroup(ctx, groups)

	// modelIdx[groupID][platform+modelName] = index into byGroup[groupID].Models
	modelIdx := make(map[int64]map[plazaModelKey]int, len(groups))
	for i := range sortedChannels {
		ch := &sortedChannels[i]
		if ch.Status != StatusActive {
			continue
		}
		ch.normalizeBillingModelSource()
		supported := ch.SupportedModels()
		s.fillGlobalPricingFallback(supported)

		for _, gid := range ch.GroupIDs {
			pg, ok := byGroup[gid]
			if !ok {
				continue
			}
			idx := modelIdx[gid]
			if idx == nil {
				idx = make(map[plazaModelKey]int, len(supported))
				modelIdx[gid] = idx
			}
			for j := range supported {
				m := supported[j]
				if pg.Platform == PlatformComposite {
					if !isConcreteRequestPlatform(m.Platform) {
						continue
					}
				} else if m.Platform != pg.Platform {
					continue
				}
				pricing := plazaImageDisplayPricing(m.Pricing, groupEnt[gid])
				key := plazaModelKey{platform: m.Platform, name: m.Name}
				if at, seen := idx[key]; seen {
					// 先见者胜；仅当已存条目无定价而新条目有定价时升级。
					if pg.Models[at].Pricing == nil && pricing != nil {
						pg.Models[at].Pricing = pricing
					}
					continue
				}
				idx[key] = len(pg.Models)
				pg.Models = append(pg.Models, PlazaModel{
					Name:     m.Name,
					Platform: m.Platform,
					Pricing:  pricing,
				})
			}
		}
	}

	officialMemo := make(map[string]*PlazaOfficialPricing)
	out := make([]PlazaGroup, 0, len(order))
	for _, gid := range order {
		pg := byGroup[gid]
		live := actualModels[gid]
		// Production injects accountRepo, so an empty live inventory means no
		// currently schedulable capability. Do not leak stale channel-configured
		// models into the public catalog in that case. Unit-test/legacy callers
		// without accountRepo retain the historical channel-driven behavior.
		if s.accountRepo != nil && len(live) == 0 {
			continue
		}
		if len(pg.Models) == 0 && len(live) == 0 {
			continue
		}
		if len(live) > 0 {
			pg.Models = s.livePlazaModelsForChannel(live, channelByGroup[gid], groupEnt[gid])
		}
		if len(pg.Models) == 0 {
			continue
		}
		sort.SliceStable(pg.Models, func(i, j int) bool {
			if pg.Models[i].Name != pg.Models[j].Name {
				return pg.Models[i].Name < pg.Models[j].Name
			}
			return pg.Models[i].Platform < pg.Models[j].Platform
		})
		for j := range pg.Models {
			officialModel := pg.Models[j].officialModel
			if officialModel == "" {
				officialModel = pg.Models[j].Name
			}
			pg.Models[j].OfficialPricing = s.lookupOfficialPricing(officialModel, officialMemo)
		}
		out = append(out, *pg)
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].RateMultiplier != out[j].RateMultiplier {
			return out[i].RateMultiplier < out[j].RateMultiplier
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

func (s *ChannelService) livePlazaModelsForChannel(
	live []plazaInventoryModel,
	channel *Channel,
	group *Group,
) []PlazaModel {
	accountModels := make(map[plazaModelKey]plazaInventoryModel, len(live))
	for _, model := range live {
		if model.customerFacing {
			continue
		}
		key := plazaModelKey{platform: model.Platform, name: strings.TrimSpace(model.Name)}
		if _, exists := accountModels[key]; !exists {
			accountModels[key] = model
		}
	}

	out := make([]PlazaModel, 0, len(live))
	seen := make(map[plazaModelKey]struct{}, len(live))
	addPublic := func(platform, publicName, channelModel, upstreamModel string) {
		publicName = strings.TrimSpace(publicName)
		channelModel = strings.TrimSpace(channelModel)
		upstreamModel = strings.TrimSpace(upstreamModel)
		if publicName == "" || channelModel == "" || upstreamModel == "" {
			return
		}
		key := plazaModelKey{platform: platform, name: strings.ToLower(publicName)}
		if _, exists := seen[key]; exists {
			return
		}
		pricing, allowed := s.plazaPricingForLiveModel(channel, group, platform, publicName, channelModel, upstreamModel)
		if !allowed {
			return
		}
		seen[key] = struct{}{}
		out = append(out, PlazaModel{
			Name:          publicName,
			Platform:      platform,
			Pricing:       pricing,
			officialModel: upstreamModel,
		})
	}
	addAccountTarget := func(platform, publicName, accountModel string) {
		target, ok := accountModels[plazaModelKey{platform: platform, name: strings.TrimSpace(accountModel)}]
		if !ok {
			return
		}
		addPublic(platform, publicName, accountModel, target.UpstreamModel)
	}

	// Every account-facing model remains directly requestable unless the current
	// channel remaps it elsewhere. In that case, resolve against the remapped
	// account capability before exposing it.
	for _, model := range live {
		if model.customerFacing {
			continue
		}
		channelModel := resolvePlazaChannelModel(channel, model.Platform, model.Name)
		addAccountTarget(model.Platform, model.Name, channelModel)
	}
	if channel != nil {
		for platform, mapping := range channel.ModelMapping {
			for publicName := range mapping {
				if strings.Contains(publicName, "*") {
					continue
				}
				channelModel := resolvePlazaChannelModel(channel, platform, publicName)
				addAccountTarget(platform, publicName, channelModel)
			}
		}
	}

	// A usage fallback already carries the original customer-facing request.
	// Apply it last so current live enumeration wins duplicate names, and do not
	// require its channel target to appear as another inventory row.
	for _, model := range live {
		if !model.customerFacing {
			continue
		}
		channelModel := resolvePlazaChannelModel(channel, model.Platform, model.Name)
		addPublic(model.Platform, model.Name, channelModel, model.UpstreamModel)
	}
	return out
}

func (s *ChannelService) plazaPricingForLiveModel(
	channel *Channel,
	group *Group,
	platform, requestedModel, channelModel, upstreamModel string,
) (*ChannelModelPricing, bool) {
	billingModel := requestedModel
	if channel != nil {
		switch channel.BillingModelSource {
		case BillingModelSourceRequested:
			billingModel = requestedModel
		case BillingModelSourceUpstream:
			billingModel = upstreamModel
		default:
			billingModel = channelModel
		}
	}
	pricing := plazaChannelPricing(channel, platform, billingModel)
	if channel != nil && channel.RestrictModels && pricing == nil {
		return nil, false
	}
	if pricingNeedsFallback(pricing) && s != nil && s.pricingService != nil {
		if official := s.pricingService.GetModelPricing(billingModel); official != nil {
			pricing = synthesizePricingFromLiteLLM(official, pricing)
		}
	}
	return plazaImageDisplayPricing(pricing, group), true
}

// plazaChannelPricing mirrors the channel cache lookup for a concrete
// platform, including Anthropic spelling normalization and suffix wildcards.
func plazaChannelPricing(channel *Channel, platform, model string) *ChannelModelPricing {
	if channel == nil {
		return nil
	}
	model = normalizeChannelPricingModelName(model)
	for i := range channel.ModelPricing {
		if channel.ModelPricing[i].Platform != platform {
			continue
		}
		for _, configured := range channel.ModelPricing[i].Models {
			if strings.HasSuffix(configured, "*") {
				continue
			}
			if normalizeChannelPricingModelName(configured) == model {
				pricing := channel.ModelPricing[i].Clone()
				return &pricing
			}
		}
	}
	for i := range channel.ModelPricing {
		if channel.ModelPricing[i].Platform != platform {
			continue
		}
		for _, configured := range channel.ModelPricing[i].Models {
			if !strings.HasSuffix(configured, "*") {
				continue
			}
			prefix := normalizeChannelPricingModelName(strings.TrimSuffix(configured, "*"))
			if strings.HasPrefix(model, prefix) {
				pricing := channel.ModelPricing[i].Clone()
				return &pricing
			}
		}
	}
	return nil
}

func resolvePlazaChannelModel(channel *Channel, platform, requested string) string {
	if channel == nil {
		return requested
	}
	mapping := channel.ModelMapping[platform]
	if len(mapping) == 0 {
		return requested
	}
	for source, target := range mapping {
		if strings.EqualFold(strings.TrimSpace(source), strings.TrimSpace(requested)) {
			if target = strings.TrimSpace(target); target != "" {
				return target
			}
			return requested
		}
	}
	requestedLower := strings.ToLower(strings.TrimSpace(requested))
	bestPrefix := ""
	bestTarget := ""
	for source, target := range mapping {
		source = strings.ToLower(strings.TrimSpace(source))
		prefix := strings.TrimSuffix(source, "*")
		if !strings.HasSuffix(source, "*") || !strings.HasPrefix(requestedLower, prefix) || len(prefix) <= len(bestPrefix) {
			continue
		}
		bestPrefix = prefix
		bestTarget = strings.TrimSpace(target)
	}
	if bestTarget != "" {
		return bestTarget
	}
	return requested
}

// liveSchedulableModelsByGroup returns the live upstream snapshot. Before the
// inventory reader is wired, it uses only successful recent usage and never
// treats configured model mappings as proof of upstream capability.
func (s *ChannelService) liveSchedulableModelsByGroup(ctx context.Context, groups []Group) map[int64][]plazaInventoryModel {
	out := make(map[int64][]plazaInventoryModel)
	if s == nil || s.accountRepo == nil {
		return out
	}
	if s.modelInventoryReader != nil {
		return s.liveUpstreamModelsByGroup(ctx, groups)
	}
	accounts, err := s.accountRepo.ListSchedulable(ctx)
	if err != nil {
		return out
	}
	groupAccountIDs := make(map[int64][]int64, len(groups))
	for _, account := range accounts {
		for _, groupID := range account.GroupIDs {
			groupAccountIDs[groupID] = append(groupAccountIDs[groupID], account.ID)
		}
	}
	if s.recentGroupModels != nil {
		end := time.Now().UTC()
		start := end.Add(-24 * time.Hour)
		for _, group := range groups {
			models, err := s.recentGroupModels.ListRecentModelsByGroup(ctx, group.ID, groupAccountIDs[group.ID], start, end)
			if err != nil {
				continue
			}
			seen := make(map[string]struct{}, len(models))
			for _, model := range models {
				model.Name = strings.TrimSpace(model.Name)
				if model.Name == "" {
					continue
				}
				key := strings.ToLower(model.Platform + "\x00" + model.Name)
				if _, ok := seen[key]; ok {
					continue
				}
				seen[key] = struct{}{}
				if model.UpstreamModel == "" {
					model.UpstreamModel = model.Name
				}
				out[group.ID] = append(out[group.ID], plazaInventoryModel{
					Name: model.Name, Platform: model.Platform, UpstreamModel: model.UpstreamModel, customerFacing: true,
				})
			}
		}
	}
	for groupID := range out {
		sort.SliceStable(out[groupID], func(i, j int) bool {
			if out[groupID][i].Name != out[groupID][j].Name {
				return out[groupID][i].Name < out[groupID][j].Name
			}
			return out[groupID][i].Platform < out[groupID][j].Platform
		})
	}
	return out
}

const plazaModelInventoryTTL = 15 * time.Minute
const (
	plazaModelInventoryWorkers = 8
	plazaModelInventoryTimeout = 8 * time.Second
)

func clonePlazaModelInventory(src map[int64][]plazaInventoryModel) map[int64][]plazaInventoryModel {
	out := make(map[int64][]plazaInventoryModel, len(src))
	for groupID, models := range src {
		out[groupID] = append([]plazaInventoryModel(nil), models...)
	}
	return out
}

// liveUpstreamModelsByGroup returns a periodically refreshed snapshot of the
// model IDs that schedulable accounts can actually serve. The refresh is
// bounded by a 15-minute TTL so public model-plaza and monitor traffic never
// probes upstreams on every request.
func (s *ChannelService) liveUpstreamModelsByGroup(ctx context.Context, groups []Group) map[int64][]plazaInventoryModel {
	now := time.Now().UTC()
	s.plazaInventoryMu.Lock()
	if s.plazaInventory != nil && now.Sub(s.plazaInventoryAt) < plazaModelInventoryTTL {
		cached := clonePlazaModelInventory(s.plazaInventory)
		s.plazaInventoryMu.Unlock()
		return cached
	}
	if s.plazaInventoryRefreshing {
		if s.plazaInventory != nil {
			cached := clonePlazaModelInventory(s.plazaInventory)
			s.plazaInventoryMu.Unlock()
			return cached
		}
	} else {
		s.plazaInventoryRefreshing = true
	}
	hadPrevious := s.plazaInventory != nil
	previous := clonePlazaModelInventory(s.plazaInventory)
	s.plazaInventoryMu.Unlock()

	result, err, _ := s.cacheSF.Do("plaza_upstream_inventory", func() (any, error) {
		return s.refreshUpstreamModelInventory(ctx, groups)
	})
	refreshed, _ := result.(map[int64][]plazaInventoryModel)
	if err != nil || refreshed == nil {
		s.plazaInventoryMu.Lock()
		s.plazaInventoryRefreshing = false
		if hadPrevious {
			s.plazaInventory = clonePlazaModelInventory(previous)
			s.plazaInventoryAt = time.Now().UTC()
		}
		cached := clonePlazaModelInventory(s.plazaInventory)
		s.plazaInventoryMu.Unlock()
		return cached
	}
	s.plazaInventoryMu.Lock()
	s.plazaInventory = clonePlazaModelInventory(refreshed)
	s.plazaInventoryAt = time.Now().UTC()
	s.plazaInventoryRefreshing = false
	cached := clonePlazaModelInventory(s.plazaInventory)
	s.plazaInventoryMu.Unlock()
	return cached
}

func (s *ChannelService) refreshUpstreamModelInventory(ctx context.Context, groups []Group) (map[int64][]plazaInventoryModel, error) {
	out := make(map[int64][]plazaInventoryModel)
	accounts, err := s.accountRepo.ListSchedulable(ctx)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	currentAccounts := accounts[:0]
	for i := range accounts {
		if accounts[i].IsSchedulableAt(now) {
			currentAccounts = append(currentAccounts, accounts[i])
		}
	}
	accounts = currentAccounts
	groupPlatforms := make(map[int64]string, len(groups))
	for _, group := range groups {
		groupPlatforms[group.ID] = group.Platform
	}
	seen := make(map[int64]map[string]struct{}, len(groups))
	// Live enumeration is account-scoped. If one account cannot be enumerated,
	// keep the successfully enumerated accounts and supplement only that
	// account's capabilities from recent successful usage. Wildcard request
	// mappings also need this fallback because they cannot be expanded into a
	// finite customer-facing model list from the upstream IDs alone.
	recentFallbackAccounts := make(map[int64]map[int64]struct{}, len(groups))
	var inventoryMu sync.Mutex
	markRecentFallback := func(account Account) {
		inventoryMu.Lock()
		defer inventoryMu.Unlock()
		for _, groupID := range account.GroupIDs {
			platform := groupPlatforms[groupID]
			if platform == "" || (platform != PlatformComposite && platform != account.Platform) {
				continue
			}
			if recentFallbackAccounts[groupID] == nil {
				recentFallbackAccounts[groupID] = make(map[int64]struct{})
			}
			recentFallbackAccounts[groupID][account.ID] = struct{}{}
		}
	}
	jobs := make(chan Account)
	workers := plazaModelInventoryWorkers
	if len(accounts) < workers {
		workers = len(accounts)
	}
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for account := range jobs {
				if len(account.GroupIDs) == 0 {
					continue
				}
				fetchCtx, cancel := context.WithTimeout(ctx, plazaModelInventoryTimeout)
				models, fetchErr := s.modelInventoryReader.FetchUpstreamSupportedModels(fetchCtx, &account)
				cancel()
				if fetchErr != nil || len(models) == 0 {
					markRecentFallback(account)
					continue
				}
				upstream := make(map[string]string, len(models))
				for _, model := range models {
					model = strings.TrimSpace(model)
					if model != "" && !strings.Contains(model, "*") {
						upstream[model] = model
					}
				}
				if len(upstream) == 0 {
					markRecentFallback(account)
					continue
				}
				mapping := account.GetModelMapping()
				if modelMappingNeedsRecentFallback(mapping) {
					markRecentFallback(account)
				}
				inventoryMu.Lock()
				for _, groupID := range account.GroupIDs {
					platform := groupPlatforms[groupID]
					if platform == "" || (platform != PlatformComposite && platform != account.Platform) {
						continue
					}
					if seen[groupID] == nil {
						seen[groupID] = make(map[string]struct{})
					}
					if len(mapping) == 0 || account.IsOpenAIPassthroughEnabled() {
						for _, model := range upstream {
							addLivePlazaModel(out, seen, groupID, account.Platform, model, model, false)
						}
						continue
					}
					for requested, target := range mapping {
						requested = strings.TrimSpace(requested)
						target = strings.TrimSpace(target)
						if requested == "" || target == "" || strings.Contains(requested, "*") || strings.Contains(target, "*") {
							continue
						}
						if upstreamModel, ok := upstream[target]; ok {
							addLivePlazaModel(out, seen, groupID, account.Platform, requested, upstreamModel, false)
						}
					}
				}
				inventoryMu.Unlock()
			}
		}()
	}
	for i := range accounts {
		if workers == 0 {
			break
		}
		jobs <- accounts[i]
	}
	close(jobs)
	wg.Wait()
	for _, group := range groups {
		if s.recentGroupModels == nil || len(recentFallbackAccounts[group.ID]) == 0 {
			continue
		}
		accountIDs := make([]int64, 0, len(recentFallbackAccounts[group.ID]))
		for accountID := range recentFallbackAccounts[group.ID] {
			accountIDs = append(accountIDs, accountID)
		}
		sort.Slice(accountIDs, func(i, j int) bool { return accountIDs[i] < accountIDs[j] })
		end := time.Now().UTC()
		models, err := s.recentGroupModels.ListRecentModelsByGroup(ctx, group.ID, accountIDs, end.Add(-24*time.Hour), end)
		if err != nil {
			continue
		}
		if seen[group.ID] == nil {
			seen[group.ID] = make(map[string]struct{})
		}
		for _, model := range models {
			model.Name = strings.TrimSpace(model.Name)
			if model.Name != "" {
				addLivePlazaModel(out, seen, group.ID, model.Platform, model.Name, model.UpstreamModel, true)
			}
		}
	}
	for groupID := range out {
		sort.SliceStable(out[groupID], func(i, j int) bool {
			if out[groupID][i].Name != out[groupID][j].Name {
				return out[groupID][i].Name < out[groupID][j].Name
			}
			if out[groupID][i].Platform != out[groupID][j].Platform {
				return out[groupID][i].Platform < out[groupID][j].Platform
			}
			if out[groupID][i].customerFacing != out[groupID][j].customerFacing {
				return !out[groupID][i].customerFacing
			}
			return out[groupID][i].UpstreamModel < out[groupID][j].UpstreamModel
		})
	}
	return out, nil
}

func modelMappingNeedsRecentFallback(mapping map[string]string) bool {
	for requested, target := range mapping {
		if strings.Contains(requested, "*") || strings.Contains(target, "*") {
			return true
		}
	}
	return false
}

// StartPlazaModelInventorySync refreshes the real upstream model catalog on
// startup and every TTL interval. Public requests read the snapshot and only
// perform an inline refresh if the background worker has not populated it yet.
func (s *ChannelService) StartPlazaModelInventorySync() {
	if s == nil || s.groupRepo == nil || s.accountRepo == nil || s.modelInventoryReader == nil {
		return
	}
	s.plazaInventoryMu.Lock()
	if s.plazaSyncCancel != nil {
		s.plazaInventoryMu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.plazaSyncCancel = cancel
	s.plazaSyncWG.Add(1)
	s.plazaInventoryMu.Unlock()
	go func() {
		defer s.plazaSyncWG.Done()
		s.syncPlazaModelInventory(ctx)
		ticker := time.NewTicker(plazaModelInventoryTTL)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.syncPlazaModelInventory(ctx)
			}
		}
	}()
}

func (s *ChannelService) syncPlazaModelInventory(ctx context.Context) {
	refreshCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	groups, err := s.groupRepo.ListActive(refreshCtx)
	if err != nil {
		return
	}
	s.plazaInventoryMu.Lock()
	s.plazaInventoryAt = time.Time{}
	s.plazaInventoryMu.Unlock()
	_ = s.liveUpstreamModelsByGroup(refreshCtx, groups)
}

// StopPlazaModelInventorySync stops the background catalog refresh worker.
func (s *ChannelService) StopPlazaModelInventorySync() {
	if s == nil {
		return
	}
	s.plazaInventoryMu.Lock()
	cancel := s.plazaSyncCancel
	s.plazaSyncCancel = nil
	s.plazaInventoryMu.Unlock()
	if cancel != nil {
		cancel()
		s.plazaSyncWG.Wait()
	}
}

func addLivePlazaModel(out map[int64][]plazaInventoryModel, seen map[int64]map[string]struct{}, groupID int64, platform, model, upstreamModel string, customerFacing bool) {
	key := strings.ToLower(fmt.Sprintf("%s\x00%s\x00%t", platform, model, customerFacing))
	if _, exists := seen[groupID][key]; exists {
		return
	}
	seen[groupID][key] = struct{}{}
	out[groupID] = append(out[groupID], plazaInventoryModel{
		Name: model, Platform: platform, UpstreamModel: upstreamModel, customerFacing: customerFacing,
	})
}

// plazaImageDisplayPricing 为图片计费模型合成展示定价，使档位价与实收口径一致：
// 每档（1K/2K/4K）单价 = 分组图片价 > 渠道同档位价 > 渠道默认按次价，无价的档不展示。
// 分组未配任何图片价、或定价非图片模式时原样返回。返回克隆，不修改入参
// （渠道定价指针指向缓存共享数据）。
func plazaImageDisplayPricing(p *ChannelModelPricing, g *Group) *ChannelModelPricing {
	if p == nil || g == nil || p.BillingMode != BillingModeImage {
		return p
	}
	if g.ImagePrice1K == nil && g.ImagePrice2K == nil && g.ImagePrice4K == nil {
		return p
	}
	channelTierPrice := func(label string) *float64 {
		for i := range p.Intervals {
			if p.Intervals[i].TierLabel == label && p.Intervals[i].PerRequestPrice != nil {
				return p.Intervals[i].PerRequestPrice
			}
		}
		return p.PerRequestPrice
	}
	tiers := []struct {
		label      string
		groupPrice *float64
	}{
		{"1K", g.ImagePrice1K},
		{"2K", g.ImagePrice2K},
		{"4K", g.ImagePrice4K},
	}
	clone := *p
	clone.Intervals = make([]PricingInterval, 0, len(tiers))
	for i, t := range tiers {
		price := t.groupPrice
		if price == nil {
			price = channelTierPrice(t.label)
		}
		if price == nil {
			continue
		}
		v := *price
		clone.Intervals = append(clone.Intervals, PricingInterval{
			TierLabel:       t.label,
			PerRequestPrice: &v,
			SortOrder:       i,
		})
	}
	return &clone
}

// lookupOfficialPricing 查询模型的 LiteLLM 官方参考价，带 memo 避免同名模型重复转换。
// pricingService 为 nil（测试场景）或查不到时返回 nil。
func (s *ChannelService) lookupOfficialPricing(modelName string, memo map[string]*PlazaOfficialPricing) *PlazaOfficialPricing {
	if s.pricingService == nil {
		return nil
	}
	if cached, ok := memo[modelName]; ok {
		return cached
	}
	var result *PlazaOfficialPricing
	if lp := s.pricingService.GetModelPricing(modelName); lp != nil && !lp.TokenPricingAbsent {
		result = &PlazaOfficialPricing{
			InputPrice:        nonZeroPtr(lp.InputCostPerToken),
			OutputPrice:       nonZeroPtr(lp.OutputCostPerToken),
			CacheWritePrice:   nonZeroPtr(lp.CacheCreationInputTokenCost),
			CacheWrite1hPrice: nonZeroPtr(lp.CacheCreationInputTokenCostAbove1hr),
			CacheReadPrice:    nonZeroPtr(lp.CacheReadInputTokenCost),
		}
		if result.InputPrice == nil && result.OutputPrice == nil &&
			result.CacheWritePrice == nil && result.CacheWrite1hPrice == nil && result.CacheReadPrice == nil {
			result = nil
		}
	}
	memo[modelName] = result
	return result
}
