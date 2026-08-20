package service

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
)

type apiKeyGroupRoutingContext struct {
	routes []APIKeyGroupRoute
	user   *User
}

type apiKeyGroupRoutingContextKey struct{}
type apiKeyGroupRouteAttemptContextKey struct{}

// ContextWithAPIKeyGroupRoutes makes an authenticated key's explicit ordered
// routes available to the scheduler. Legacy keys intentionally do not install
// a routing context and keep their single group_id behavior.
func ContextWithAPIKeyGroupRoutes(ctx context.Context, apiKey *APIKey) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if apiKey == nil || len(apiKey.GroupRoutes) == 0 {
		return ctx
	}
	return context.WithValue(ctx, apiKeyGroupRoutingContextKey{}, apiKeyGroupRoutingContext{
		routes: append([]APIKeyGroupRoute(nil), apiKey.GroupRoutes...),
		user:   apiKey.User,
	})
}

func apiKeyGroupRoutesFromContext(ctx context.Context, primaryGroupID *int64) (apiKeyGroupRoutingContext, bool) {
	if ctx == nil || primaryGroupID == nil || *primaryGroupID <= 0 {
		return apiKeyGroupRoutingContext{}, false
	}
	if _, attempt := ctx.Value(apiKeyGroupRouteAttemptContextKey{}).(struct{}); attempt {
		return apiKeyGroupRoutingContext{}, false
	}
	routing, ok := ctx.Value(apiKeyGroupRoutingContextKey{}).(apiKeyGroupRoutingContext)
	if !ok || len(routing.routes) == 0 || routing.routes[0].GroupID != *primaryGroupID {
		return apiKeyGroupRoutingContext{}, false
	}
	return routing, true
}

func withAPIKeyGroupRouteAttempt(ctx context.Context) context.Context {
	return context.WithValue(ctx, apiKeyGroupRouteAttemptContextKey{}, struct{}{})
}

func apiKeyRoutePricingAt(ctx context.Context, openAI bool) time.Time {
	if openAI {
		if at, ok := openAIPricingAtFromContext(ctx); ok {
			return at
		}
	} else if at, ok := gatewayTokenRequestPricingAtFromContext(ctx); ok {
		return at
	}
	return timezone.Now()
}

func apiKeyRouteAllowedForUser(user *User, group *Group) bool {
	if group == nil || !group.IsActive() {
		return false
	}
	if user == nil {
		return true
	}
	if group.IsSubscriptionType() {
		return true
	}
	return user.CanBindGroup(group.ID, group.IsExclusive)
}

func apiKeyRouteWithinRateCap(ctx context.Context, route APIKeyGroupRoute, group *Group, resolveUserRate func(context.Context, int64, int64, float64) float64, openAI bool) bool {
	if route.MaxRateMultiplier == nil {
		return true
	}
	effective := group.RateMultiplier
	if routing, ok := ctx.Value(apiKeyGroupRoutingContextKey{}).(apiKeyGroupRoutingContext); ok && routing.user != nil && resolveUserRate != nil {
		effective = resolveUserRate(ctx, routing.user.ID, group.ID, group.RateMultiplier)
	}
	effective *= group.PeakMultiplierAt(apiKeyRoutePricingAt(ctx, openAI))
	return effective <= *route.MaxRateMultiplier || math.Abs(effective-*route.MaxRateMultiplier) <= 1e-9
}

func resolveAPIKeyRouteBillingEligibility(ctx context.Context, repo UserSubscriptionRepository, user *User, group *Group) (*UserSubscription, bool, error) {
	if group == nil {
		return nil, false, nil
	}
	if !group.IsSubscriptionType() {
		if user != nil && user.Balance <= 0 {
			return nil, false, nil
		}
		return nil, true, nil
	}
	if repo == nil || user == nil {
		return nil, false, nil
	}
	subscription, err := repo.GetActiveByUserIDAndGroupID(ctx, user.ID, group.ID)
	if err != nil {
		if errors.Is(err, ErrSubscriptionNotFound) {
			return nil, false, nil
		}
		return nil, false, err
	}
	validator := &SubscriptionService{userSubRepo: repo, now: time.Now}
	needsMaintenance, validateErr := validator.ValidateAndCheckLimits(subscription, group)
	if validateErr != nil {
		return nil, false, nil
	}
	if needsMaintenance {
		refreshed, maintenanceErr := validator.EnsureWindowMaintenance(ctx, subscription)
		if maintenanceErr != nil {
			return nil, false, maintenanceErr
		}
		subscription = refreshed
		if _, validateErr = validator.ValidateAndCheckLimits(subscription, group); validateErr != nil {
			return nil, false, nil
		}
	}
	return subscription, true, nil
}

func contextWithSelectedAPIKeyGroup(ctx context.Context, group *Group) context.Context {
	if ctx == nil || !IsGroupContextValid(group) {
		return ctx
	}
	return context.WithValue(ctx, ctxkey.Group, group)
}

// APIKeyForBilling returns a request-local key view whose group reflects the
// route that actually selected the account.
func (r *AccountSelectionResult) APIKeyForBilling(apiKey *APIKey) *APIKey {
	if apiKey == nil || r == nil || !IsGroupContextValid(r.Group) {
		return apiKey
	}
	clone := *apiKey
	groupID := r.Group.ID
	clone.GroupID = &groupID
	clone.Group = r.Group
	return &clone
}

func (r *AccountSelectionResult) attachAPIKeyRoute(group *Group, subscription *UserSubscription, index int) {
	if r == nil || r.Account == nil || !IsGroupContextValid(group) {
		return
	}
	r.Group = group
	r.Subscription = subscription
	r.GroupRouteIndex = index
	account := *r.Account
	account.SelectedAPIKeyGroup = group
	account.SelectedAPIKeySubscription = subscription
	r.Account = &account
}

// AccountForSelectedRoute preserves request-local route metadata when a
// handler refreshes an account after waiting for a concurrency slot.
func (r *AccountSelectionResult) AccountForSelectedRoute(account *Account) *Account {
	if r == nil || account == nil || !IsGroupContextValid(r.Group) {
		return account
	}
	clone := *account
	clone.SelectedAPIKeyGroup = r.Group
	clone.SelectedAPIKeySubscription = r.Subscription
	return &clone
}

func apiKeyAndSubscriptionForSelectedAccount(apiKey *APIKey, subscription *UserSubscription, account *Account) (*APIKey, *UserSubscription) {
	if apiKey == nil || account == nil || !IsGroupContextValid(account.SelectedAPIKeyGroup) {
		return apiKey, subscription
	}
	clone := *apiKey
	groupID := account.SelectedAPIKeyGroup.ID
	clone.GroupID = &groupID
	clone.Group = account.SelectedAPIKeyGroup
	return &clone, account.SelectedAPIKeySubscription
}

func (r *AccountSelectionResult) SubscriptionForBilling(fallback *UserSubscription) *UserSubscription {
	if r == nil || r.Group == nil {
		return fallback
	}
	if !r.Group.IsSubscriptionType() {
		return nil
	}
	return r.Subscription
}
