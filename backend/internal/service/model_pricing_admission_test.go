//go:build unit

package service

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestModelPricingAdmissionResolvesRates(t *testing.T) {
	ctx := context.Background()
	billing := newTestBillingServiceForResolver()
	const model = "gemini-4-neon"
	const groupID int64 = 118
	price := 3e-6
	zero := 0.0
	threshold := 200_000
	next := threshold + 1
	for _, tc := range []struct {
		name        string
		checkModel  string
		groupPrice  *ChannelModelPricing
		channelCard *ChannelModelPricing
		want        bool
	}{
		{name: "missing"},
		{name: "empty card", groupPrice: &ChannelModelPricing{}},
		{name: "input-only card", groupPrice: &ChannelModelPricing{InputPrice: &price}, want: true},
		{name: "explicitly free", groupPrice: &ChannelModelPricing{InputPrice: &zero, OutputPrice: &zero}, want: true},
		{name: "built-in fallback", checkModel: "claude-sonnet-4", want: true},
		{name: "empty override inherits fallback", checkModel: "claude-sonnet-4", groupPrice: &ChannelModelPricing{}, want: true},
		{name: "paid per request", groupPrice: &ChannelModelPricing{BillingMode: BillingModePerRequest, PerRequestPrice: &price}, want: true},
		{name: "free per request", groupPrice: &ChannelModelPricing{BillingMode: BillingModePerRequest, PerRequestPrice: &zero}, want: true},
		{name: "per request tier with a gap", groupPrice: &ChannelModelPricing{BillingMode: BillingModePerRequest, Intervals: []PricingInterval{{MinTokens: next, PerRequestPrice: &price}}}},
		{name: "complete channel tiers", channelCard: &ChannelModelPricing{Intervals: []PricingInterval{
			{MinTokens: 0, MaxTokens: &threshold, InputPrice: &price}, {MinTokens: threshold, InputPrice: &price},
		}}, want: true},
		{name: "channel tier gap", channelCard: &ChannelModelPricing{Intervals: []PricingInterval{
			{MinTokens: 0, MaxTokens: &threshold, InputPrice: &price}, {MinTokens: next, InputPrice: &price},
		}}},
		{name: "tier multiplier without a base", channelCard: &ChannelModelPricing{Intervals: []PricingInterval{{InputMultiplier: &price}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			checkModel := firstNonEmpty(tc.checkModel, model)
			group := &Group{ID: groupID, Platform: PlatformGemini, RateMultiplier: 1, LongContextPricingEnabled: true}
			if tc.groupPrice != nil {
				card := tc.groupPrice.Clone()
				card.Models = []string{checkModel}
				group.ModelPricing = []ChannelModelPricing{card}
			}
			key := &APIKey{Group: group, GroupID: &group.ID}
			resolver := NewModelPricingResolver(nil, billing)
			var actual bool
			if tc.channelCard == nil {
				actual = resolver.hasUsablePrice(ctx, key, nil, checkModel)
			} else {
				actual = resolver.resolveConfiguredPricing(tc.channelCard, checkModel, PricingSourceChannel).hasUsablePrice(true)
			}
			require.Equal(t, tc.want, actual)
		})
	}
}

func TestOpenAIForwardRejectsMissingPriceBeforeTransport(t *testing.T) {
	const alias = "external-pricing-admission-test"
	const model = "unpriced-model-admission-test"
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name, route, body string
		forward           func(*OpenAIGatewayService, context.Context, *gin.Context, *Account, []byte) (*OpenAIForwardResult, error)
	}{
		{"Responses", "/v1/responses", `{"model":"` + alias + `","input":"hi"}`, (*OpenAIGatewayService).Forward},
		{"Chat Completions", "/v1/chat/completions", `{"model":"` + alias + `","messages":[{"role":"user","content":"hi"}]}`, func(s *OpenAIGatewayService, ctx context.Context, c *gin.Context, a *Account, body []byte) (*OpenAIForwardResult, error) {
			return s.ForwardAsChatCompletions(ctx, c, a, body, "", "")
		}},
		{"Messages", "/v1/messages", `{"model":"` + alias + `","max_tokens":256,"messages":[{"role":"user","content":"hi"}]}`, func(s *OpenAIGatewayService, ctx context.Context, c *gin.Context, a *Account, body []byte) (*OpenAIForwardResult, error) {
			return s.ForwardAsAnthropic(ctx, c, a, body, "", "")
		}},
		{"Embeddings", "/v1/embeddings", `{"model":"` + alias + `","input":"hi"}`, func(s *OpenAIGatewayService, ctx context.Context, c *gin.Context, a *Account, body []byte) (*OpenAIForwardResult, error) {
			return s.ForwardEmbeddings(ctx, c, a, body, "")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte(tc.body)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, tc.route, bytes.NewReader(body))
			group := &Group{ID: 118, Platform: PlatformOpenAI, RateMultiplier: 1}
			key := &APIKey{Group: group, GroupID: &group.ID}
			account := forceChatResponsesFallbackAccount()
			account.Credentials["model_mapping"] = map[string]any{alias: model}
			billing := newTestBillingServiceForResolver()
			upstream := &httpUpstreamRecorder{}
			svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), billingService: billing, resolver: NewModelPricingResolver(nil, billing), httpUpstream: upstream}
			ctx := svc.WithModelPricingAdmission(c.Request.Context(), key, ChannelUsageFields{OriginalModel: alias})
			result, err := tc.forward(svc, ctx, c, account, body)
			require.ErrorIs(t, err, ErrModelPricingUnavailable)
			require.Nil(t, result)
			require.Nil(t, upstream.lastReq)
		})
	}
}

func TestModelPricingAdmissionPerRequestSettlement(t *testing.T) {
	billing := newTestBillingServiceForResolver()
	resolver := NewModelPricingResolver(nil, billing)
	paid, free := 0.05, 0.0
	maxTokens := 100
	card := &ChannelModelPricing{BillingMode: BillingModePerRequest, PerRequestPrice: &paid,
		Intervals: []PricingInterval{{MaxTokens: &maxTokens, PerRequestPrice: &free}}}
	input := CostInput{Model: "per-request-model", RateMultiplier: 1, Resolver: resolver,
		Resolved: resolver.resolveConfiguredPricing(card, "per-request-model", PricingSourceChannel)}
	input.Tokens = UsageTokens{InputTokens: maxTokens}
	cost, err := billing.CalculateCostUnified(input)
	require.NoError(t, err)
	require.Zero(t, cost.ActualCost)
	input.Tokens.InputTokens++
	cost, err = billing.CalculateCostUnified(input)
	require.NoError(t, err)
	require.Equal(t, paid, cost.ActualCost)
	card.PerRequestPrice = nil
	input.Resolved = resolver.resolveConfiguredPricing(card, input.Model, PricingSourceChannel)
	_, err = billing.CalculateCostUnified(input)
	require.ErrorIs(t, err, ErrModelPricingUnavailable)
}

func TestOpenAIModelPricingAdmissionUsesBillingCandidates(t *testing.T) {
	ctx := context.Background()
	billing := newTestBillingServiceForResolver()
	gateway := &OpenAIGatewayService{billingService: billing, resolver: NewModelPricingResolver(nil, billing)}
	group := &Group{ID: 118, RateMultiplier: 1, Platform: PlatformOpenAI}
	key := &APIKey{Group: group, GroupID: &group.ID}
	const upstream = "unpriced-upstream"
	account := &Account{Platform: PlatformOpenAI}
	fields := ChannelUsageFields{OriginalModel: "external-alias", ChannelMappedModel: "mapped-alias", BillingModelSource: BillingModelSourceRequested}
	check := func() error {
		return gateway.CheckModelPricingAdmission(ctx, key, account, fields, fields.OriginalModel, upstream, "")
	}
	require.ErrorIs(t, check(), ErrModelPricingUnavailable)
	free := 0.0
	group.ModelPricing = []ChannelModelPricing{{Models: []string{fields.ChannelMappedModel}, InputPrice: &free}}
	require.NoError(t, check(), "settlement can charge the configured channel-mapped fallback candidate")
	group.ModelPricing = nil
	account.Platform = PlatformKimi
	fields.OriginalModel = "claude-sonnet-4"
	require.ErrorIs(t, check(), ErrModelPricingUnavailable, "a native provider may not borrow the Claude catalog rate")
	gateway.cfg = &config.Config{RunMode: config.RunModeSimple}
	require.NoError(t, check(), "simple mode explicitly disables usage billing")
}

type pricingAdmissionAdminAccounts struct {
	AccountRepository
	accounts []Account
}

func (r *pricingAdmissionAdminAccounts) ListByGroup(context.Context, int64) ([]Account, error) {
	return r.accounts, nil
}

func (r *pricingAdmissionAdminAccounts) GetByIDs(context.Context, []int64) ([]*Account, error) {
	accounts := make([]*Account, 0, len(r.accounts))
	for i := range r.accounts {
		accounts = append(accounts, &r.accounts[i])
	}
	return accounts, nil
}

func TestAdminModelPricingAdmissionBeforeWrites(t *testing.T) {
	ctx := context.Background()
	const model = "gemini-4-neon"
	const alias = "external-alias"
	resolver := NewModelPricingResolver(nil, newTestBillingServiceForResolver())
	group := &Group{ID: 118, Name: "Gemini Ultra", Platform: PlatformGemini, Status: StatusDisabled, RateMultiplier: 0.59}
	repo := &groupRepoStubForAdmin{getByID: group}
	account := Account{Platform: PlatformGemini, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true,
		Credentials: map[string]any{"model_mapping": map[string]any{alias: model}}}
	accounts := &pricingAdmissionAdminAccounts{accounts: []Account{account}}
	svc := &adminServiceImpl{groupRepo: repo, accountRepo: accounts, modelPricingResolver: resolver}

	allowlist := GroupModelAllowlist{Enabled: true, Models: []string{alias}}
	_, err := svc.UpdateGroup(ctx, group.ID, &UpdateGroupInput{Status: StatusActive, ModelAllowlist: &allowlist})
	require.Equal(t, "MODEL_PRICING_UNAVAILABLE", infraerrors.Reason(err))
	require.ErrorContains(t, err, model)
	require.Nil(t, repo.updated, "activation must fail before persistence")
	require.Equal(t, StatusDisabled, group.Status)

	_, err = svc.UpdateGroup(ctx, group.ID, &UpdateGroupInput{ModelAllowlist: &allowlist})
	require.NoError(t, err, "inactive groups can save an unpriced draft")
	require.Equal(t, StatusDisabled, repo.updated.Status)

	paid, zero := 3e-6, 0.0
	for _, price := range []float64{paid, zero} {
		card := []ChannelModelPricing{{Models: []string{model}, InputPrice: &price}}
		updated, err := svc.UpdateGroup(ctx, group.ID, &UpdateGroupInput{Status: StatusActive, ModelAllowlist: &allowlist, ModelPricing: &card})
		require.NoError(t, err)
		require.Equal(t, StatusActive, updated.Status)
	}

	repo.getAccountIDsByGroupIDsFn = func([]int64) ([]int64, error) { return []int64{1}, nil }
	_, err = svc.CreateGroup(ctx, &CreateGroupInput{
		Name: "create with copied routes", Platform: PlatformGemini, RateMultiplier: 1, CopyAccountsFromGroupIDs: []int64{group.ID},
	})
	require.Equal(t, "MODEL_PRICING_UNAVAILABLE", infraerrors.Reason(err))
	require.Nil(t, repo.created)
}
