package service

import (
	"context"
	"fmt"
	"sort"
	"strings"
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
// 与 AvailableGroupRef 相比多了 Description 与 Models；生产环境中 Models
// 来自该分组近 24 小时的实际成功调用记录。
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

// ListPlazaGroups 返回模型广场数据：每个活跃分组附带近 24 小时实际成功
// 调用过的模型与官方参考定价。
//
// 生产环境注入 recentGroupModels 后，usage_logs 是唯一模型清单来源；不枚举
// 上游 /models，也不回退到渠道静态配置。其余聚合口径与 ListAvailable 一致，
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
	recentModels, err := s.recentModelsByGroup(ctx, groups)
	if err != nil {
		return nil, fmt.Errorf("list recent group models: %w", err)
	}

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
		recent := recentModels[gid]
		if s.recentGroupModels != nil {
			if len(recent) == 0 {
				continue
			}
			pg.Models = s.recentPlazaModelsForChannel(recent, channelByGroup[gid], groupEnt[gid])
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

func (s *ChannelService) recentPlazaModelsForChannel(
	recent []RecentGroupModel,
	channel *Channel,
	group *Group,
) []PlazaModel {
	out := make([]PlazaModel, 0, len(recent))
	for _, model := range recent {
		name := strings.TrimSpace(model.Name)
		platform := strings.TrimSpace(model.Platform)
		upstreamModel := strings.TrimSpace(model.UpstreamModel)
		if name == "" || platform == "" {
			continue
		}
		if upstreamModel == "" {
			upstreamModel = name
		}
		channelModel := resolvePlazaChannelModel(channel, platform, name)
		// A recent successful call is authoritative for catalog membership.
		// Current channel restrictions may still enrich its pricing, but must not
		// hide a model that was actually used during the requested window.
		pricing, _ := s.plazaPricingForLiveModel(channel, group, platform, name, channelModel, upstreamModel)
		out = append(out, PlazaModel{
			Name:          name,
			Platform:      platform,
			Pricing:       pricing,
			officialModel: upstreamModel,
		})
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

func (s *ChannelService) recentModelsByGroup(ctx context.Context, groups []Group) (map[int64][]RecentGroupModel, error) {
	out := make(map[int64][]RecentGroupModel, len(groups))
	if s == nil || s.recentGroupModels == nil {
		return out, nil
	}
	end := time.Now().UTC()
	start := end.Add(-24 * time.Hour)
	groupIDs := make([]int64, 0, len(groups))
	for _, group := range groups {
		groupIDs = append(groupIDs, group.ID)
	}
	modelsByGroup, err := s.recentGroupModels.ListRecentModelsByGroups(ctx, groupIDs, start, end)
	if err != nil {
		return nil, err
	}
	for _, group := range groups {
		models := modelsByGroup[group.ID]
		seen := make(map[string]struct{}, len(models))
		for _, model := range models {
			model.Name = strings.TrimSpace(model.Name)
			model.Platform = strings.TrimSpace(model.Platform)
			model.UpstreamModel = strings.TrimSpace(model.UpstreamModel)
			if model.Name == "" || model.Platform == "" {
				continue
			}
			key := strings.ToLower(model.Platform + "\x00" + model.Name)
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			if model.UpstreamModel == "" {
				model.UpstreamModel = model.Name
			}
			out[group.ID] = append(out[group.ID], model)
		}
		sort.SliceStable(out[group.ID], func(i, j int) bool {
			if out[group.ID][i].Name != out[group.ID][j].Name {
				return out[group.ID][i].Name < out[group.ID][j].Name
			}
			return out[group.ID][i].Platform < out[group.ID][j].Platform
		})
	}
	return out, nil
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
