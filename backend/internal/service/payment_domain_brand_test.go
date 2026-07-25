package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPaymentServiceValidateSubOrderRejectsCrossDomainPlan(t *testing.T) {
	client := newPaymentConfigServiceTestClient(t)
	plan, err := client.SubscriptionPlan.Create().
		SetGroupID(79).
		SetName("B plan").
		SetPrice(9.9).
		SetValidityDays(30).
		SetValidityUnit("days").
		SetForSale(true).
		Save(context.Background())
	require.NoError(t, err)

	svc := &PaymentService{configService: &PaymentConfigService{entClient: client}}
	ctx := WithDomainBrandProfile(context.Background(), DomainBrandProfile{
		Configured: true, AllowedGroupIDs: []int64{5},
	})

	_, err = svc.validateSubOrder(ctx, CreateOrderRequest{PlanID: int64(plan.ID)})
	require.Error(t, err)
	require.Contains(t, err.Error(), "plan not found or not for sale")
}
