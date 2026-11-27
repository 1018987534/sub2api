package service

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/domain"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

func (s *adminServiceImpl) validatesActiveModelPricing(group *Group) bool {
	return s.modelPricingResolver != nil && group.IsActive() && group.RateMultiplier != 0 &&
		(s.cfg == nil || s.cfg.RunMode != config.RunModeSimple)
}

func concretePricingModel(model string) bool {
	return strings.TrimSpace(model) != "" && !strings.Contains(model, "*")
}

// These models can be published on dedicated media endpoints, which use
// their own unit prices. Generation through a token endpoint is checked there.
func hasDedicatedMediaBilling(model string) bool {
	return isOpenAIImageGenerationModel(model) || isGeminiImageOutputModel(model) || isGrokVideoBillingModel(model) ||
		strings.HasPrefix(strings.ToLower(strings.TrimPrefix(model, "models/")), "imagen-") || model == "grok-voice-latest"
}

func (s *adminServiceImpl) validateActiveGroupModelPricing(ctx context.Context, group *Group, copiedAccountIDs []int64) error {
	if !s.validatesActiveModelPricing(group) {
		return nil
	}
	var accounts []*Account
	if len(copiedAccountIDs) > 0 {
		var err error
		accounts, err = s.accountRepo.GetByIDs(ctx, copiedAccountIDs)
		if err != nil {
			return fmt.Errorf("load copied accounts for model pricing validation: %w", err)
		}
	} else if copiedAccountIDs == nil && group.ID > 0 {
		members, err := s.accountRepo.ListByGroup(ctx, group.ID)
		if err != nil {
			return fmt.Errorf("load group accounts for model pricing validation: %w", err)
		}
		for i := range members {
			accounts = append(accounts, &members[i])
		}
	}
	covered := make(map[string]bool)
	routed := make(map[string]bool)
	if group.Platform == PlatformComposite && group.ID > 0 && s.compositeRouteRepo != nil {
		routes, err := s.compositeRouteRepo.ListByGroup(ctx, group.ID, false)
		if err != nil {
			return fmt.Errorf("load composite routes for model pricing validation: %w", err)
		}
		for i := range routes {
			route := &routes[i]
			if err := s.validateCompositeRouteModelPricing(ctx, group, route, accounts); err != nil {
				return err
			}
			if route.Enabled && route.MatchType == CompositeRouteMatchExact {
				routed[strings.ToLower(route.PublicModel)] = true
			}
		}
	}
	for _, account := range accounts {
		if !groupCanPublishAccountModels(group, account) {
			continue
		}
		for _, model := range accountPricingPublicationModels(group, account) {
			if routed[strings.ToLower(model)] {
				continue
			}
			if err := s.validatePublishedModelPrice(ctx, group, account, account.Platform, model, model); err != nil {
				return err
			}
			covered[strings.ToLower(model)] = true
		}
	}
	if group.ModelAllowlist.Enabled {
		for _, model := range group.ModelAllowlist.Models {
			if concretePricingModel(model) && !covered[strings.ToLower(model)] && !routed[strings.ToLower(model)] {
				if err := s.validatePublishedModelPrice(ctx, group, nil, group.Platform, model, model); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func groupCanPublishAccountModels(group *Group, account *Account) bool {
	return account != nil && account.IsActive() && account.Schedulable &&
		(group.Platform == account.Platform || group.Platform == PlatformComposite ||
			(group.Platform == PlatformAnthropic || group.Platform == PlatformGemini) && account.Platform == PlatformAntigravity && account.IsMixedSchedulingEnabled()) &&
		(!group.RequireOAuthOnly || !groupSupportsOAuthOnlyFilter(group.Platform) || account.Type != AccountTypeAPIKey)
}

func accountPricingPublicationModels(group *Group, account *Account) []string {
	var models []string
	// Use explicitly configured mappings, without enumerating provider defaults.
	// Default models and wildcard-only account mappings are checked on request.
	for model := range stringMappingFromRaw(account.Credentials["model_mapping"]) {
		if concretePricingModel(model) && group.ModelAllowlist.Allows(model) {
			models = append(models, model)
		}
	}
	if group.ModelAllowlist.Enabled {
		for _, model := range group.ModelAllowlist.Models {
			if concretePricingModel(model) && account.IsModelSupported(model) {
				models = append(models, model)
			}
		}
	}
	slices.Sort(models)
	return slices.Compact(models)
}

func (s *adminServiceImpl) validateCompositeRouteModelPricing(ctx context.Context, group *Group, route *CompositeModelRoute, accounts []*Account) error {
	if !s.validatesActiveModelPricing(group) || !route.Enabled || route.MatchType != CompositeRouteMatchExact || !group.ModelAllowlist.Allows(route.PublicModel) {
		return nil
	}
	model := firstNonEmpty(route.UpstreamModel, route.PublicModel)
	matched := false
	for _, account := range accounts {
		if groupCanPublishAccountModels(group, account) && account.Platform == route.TargetPlatform && account.IsModelSupported(model) {
			if err := s.validatePublishedModelPrice(ctx, group, account, route.TargetPlatform, route.PublicModel, model); err != nil {
				return err
			}
			matched = true
		}
	}
	if matched {
		return nil
	}
	return s.validatePublishedModelPrice(ctx, group, nil, route.TargetPlatform, route.PublicModel, model)
}

func (s *adminServiceImpl) validatePublishedModelPrice(ctx context.Context, group *Group, account *Account, platform, model, routeModel string) error {
	resolver := s.modelPricingResolver
	// Use the prospective group platform when editing an existing group. Its
	// cached channel record can still contain the platform from before the edit.
	if group.Platform != PlatformComposite {
		platform = group.Platform
	}
	ctx = context.WithValue(ctx, ctxkey.ForcePlatform, platform)
	mapped := ChannelMappingResult{MappedModel: routeModel}
	if resolver.channelService != nil && group.ID > 0 {
		mapped = resolver.channelService.ResolveChannelMapping(ctx, group.ID, routeModel)
	}
	upstream := mapped.MappedModel
	if account != nil {
		upstream = account.GetMappedModel(upstream)
		if domain.UsesOpenAIGateway(account.Platform) {
			upstream = normalizeOpenAIModelForUpstream(account, upstream)
		}
	}
	if hasDedicatedMediaBilling(upstream) {
		return nil
	}
	groupKey := &APIKey{Group: group}
	if group.ID > 0 {
		groupKey.GroupID = &group.ID
	}
	fields := mapped.ToUsageFields(model, upstream)
	if domain.UsesOpenAIGateway(platform) {
		gateway := OpenAIGatewayService{resolver: resolver, billingService: resolver.billingService}
		for _, candidate := range gateway.openAIUsageBillingModels(ctx, groupKey, account, routeModel, upstream, "", fields) {
			if resolver.hasUsablePrice(ctx, groupKey, account, candidate) {
				return nil
			}
		}
	} else {
		gateway := GatewayService{resolver: resolver, billingService: resolver.billingService}
		result := &ForwardResult{Model: routeModel, UpstreamModel: upstream}
		billingModel := gateway.recordUsageBillingModel(ctx, groupKey, result, fields)
		if resolver.hasUsablePrice(ctx, groupKey, nil, billingModel) {
			return nil
		}
	}
	return infraerrors.BadRequest("MODEL_PRICING_UNAVAILABLE", fmt.Sprintf("Configure a billing price for model %q (upstream %q) before enabling it in group %q.", model, upstream, group.Name))
}

func (s *adminServiceImpl) validateAccountModelPublication(ctx context.Context, account *Account, groupIDs []int64) error {
	if s.modelPricingResolver == nil || !account.IsActive() || !account.Schedulable ||
		(s.cfg != nil && s.cfg.RunMode == config.RunModeSimple) {
		return nil
	}
	for _, id := range groupIDs {
		group, err := s.groupRepo.GetByIDLite(ctx, id)
		if err != nil {
			return err
		}
		if !s.validatesActiveModelPricing(group) || !groupCanPublishAccountModels(group, account) {
			continue
		}
		// Composite group account admission may be scoped by an endpoint route.
		// That route is validated against its matching accounts when enabled.
		if group.Platform == PlatformComposite {
			continue
		}
		for _, model := range accountPricingPublicationModels(group, account) {
			if err := s.validatePublishedModelPrice(ctx, group, account, account.Platform, model, model); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *adminServiceImpl) validateCompositeRoutePublication(ctx context.Context, route *CompositeModelRoute) error {
	if s.modelPricingResolver == nil || !route.Enabled || route.MatchType != CompositeRouteMatchExact {
		return nil
	}
	group, err := s.groupRepo.GetByIDLite(ctx, route.GroupID)
	if err != nil || !s.validatesActiveModelPricing(group) {
		return err
	}
	members, err := s.accountRepo.ListByGroup(ctx, group.ID)
	if err != nil {
		return err
	}
	accounts := make([]*Account, 0, len(members))
	for i := range members {
		accounts = append(accounts, &members[i])
	}
	return s.validateCompositeRouteModelPricing(ctx, group, route, accounts)
}
