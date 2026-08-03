package repository

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestAffiliateUserOverviewSQLIncludesMaturedFrozenQuota(t *testing.T) {
	query := strings.Join(strings.Fields(affiliateUserOverviewSQL), " ")

	require.Contains(t, query, "ua.aff_quota + COALESCE(matured.matured_frozen_quota, 0)")
	require.Contains(t, query, "frozen_until <= NOW()")
}

func TestAffiliateRecordQueriesUseLedgerAuditFields(t *testing.T) {
	source, err := os.ReadFile("affiliate_repo.go")
	require.NoError(t, err)
	content := string(source)

	require.Contains(t, content, "JOIN payment_orders po ON po.id = ual.source_order_id")
	require.Contains(t, content, "ual.amount::double precision")
	require.Contains(t, content, "ual.balance_after::double precision")
	require.NotContains(t, content, "parseAffiliateRebateAmount")
	require.NotContains(t, content, `"current_balance": "u.balance"`)
}

func TestAffiliateInviteQueriesUseBindingMetadata(t *testing.T) {
	source, err := os.ReadFile("affiliate_repo.go")
	require.NoError(t, err)
	content := string(source)

	require.Contains(t, content, "inviter_bound_at = NOW()")
	require.Contains(t, content, "inviter_bind_source = $2")
	require.Contains(t, content, "COALESCE(ua.inviter_bound_at, ua.created_at)")
}

func TestAffiliatePaidInviteeGateCountsDistinctInvitedAccountsWithCompletedPayments(t *testing.T) {
	source, err := os.ReadFile("affiliate_repo.go")
	require.NoError(t, err)
	content := string(source)

	require.Contains(t, content, "COUNT(DISTINCT invitee.user_id)")
	require.Contains(t, content, "invitee.inviter_id = $1")
	require.Contains(t, content, "po.user_id = invitee.user_id")
	require.Contains(t, content, "po.status = 'completed'")
	require.Contains(t, content, "NewAffiliatePaidInviteesLowError")
}

func TestCountPaidInviteesReturnsDistinctCompletedPaymentCount(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	mock.ExpectQuery(`SELECT COUNT\(DISTINCT invitee.user_id\)::integer`).
		WithArgs(int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(5))

	count, err := countPaidInvitees(context.Background(), db, 42)
	require.NoError(t, err)
	require.Equal(t, 5, count)
	require.NoError(t, mock.ExpectationsWereMet())
}
