package repository

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

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

func TestHasRecentCompletedPaymentChecksTheInviterOwnOrder(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	since := time.Date(2026, time.August, 1, 12, 0, 0, 0, time.UTC)
	mock.ExpectQuery(`SELECT EXISTS`).
		WithArgs(int64(42), since).
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))

	exists, err := hasRecentCompletedPayment(context.Background(), db, 42, since)
	require.NoError(t, err)
	require.True(t, exists)
	require.NoError(t, mock.ExpectationsWereMet())

	source, err := os.ReadFile("affiliate_repo.go")
	require.NoError(t, err)
	content := string(source)
	require.Contains(t, content, "payment.user_id = $1")
	require.Contains(t, content, "payment.status = 'completed'")
	require.Contains(t, content, "COALESCE(payment.completed_at, payment.paid_at) >= $2")
}

// TestAffiliateRebateRecordsQueryKeepsNonOrderAccruals 锁定返利记录列出全部
// accrue 流水：订单与被邀请人均为 LEFT JOIN，且不按 source_order_id 过滤，
// 兑换码、管理员充值来源的返利才会出现在明细里。
func TestAffiliateRebateRecordsQueryKeepsNonOrderAccruals(t *testing.T) {
	source, err := os.ReadFile("affiliate_repo.go")
	require.NoError(t, err)
	content := string(source)

	require.Contains(t, content, "LEFT JOIN payment_orders po ON po.id = ual.source_order_id")
	require.Contains(t, content, "LEFT JOIN users invitee ON invitee.id = ual.source_user_id")
	require.NotContains(t, content, "\nJOIN payment_orders po ON po.id = ual.source_order_id")
	require.NotContains(t, content, "\nJOIN users invitee ON invitee.id = ual.source_user_id")
	require.NotContains(t, content, "AND ual.source_order_id IS NOT NULL")
}

// TestAffiliateTransferRecordsQueryIncludesOfflineWithdrawals 锁定提取记录同时
// 列出转入余额与线下提现两类额度流出。
func TestAffiliateTransferRecordsQueryIncludesOfflineWithdrawals(t *testing.T) {
	source, err := os.ReadFile("affiliate_repo.go")
	require.NoError(t, err)
	content := string(source)

	require.Contains(t, content, "WHERE ual.action IN ('transfer', 'withdraw')")
}

// TestAffiliateWithdrawClaimsOperationBeforeDeducting 锁定线下提现的幂等形态：
// 同一事务内先按 operation_id 唯一约束写入占位流水，冲突时不扣减，
// 且唯一约束由迁移建立。
func TestAffiliateWithdrawClaimsOperationBeforeDeducting(t *testing.T) {
	source, err := os.ReadFile("affiliate_repo.go")
	require.NoError(t, err)
	content := string(source)

	require.Contains(t, content, "ON CONFLICT (operation_id) WHERE operation_id IS NOT NULL DO NOTHING")
	withdraw := content[strings.Index(content, "func (r *affiliateRepository) WithdrawQuota("):]
	withdraw = withdraw[:strings.Index(withdraw, "\n}\n")]
	claimAt := strings.Index(withdraw, "claimAffiliateWithdrawLedger(")
	deductAt := strings.Index(withdraw, "SET aff_quota = aff_quota - $1")
	require.Positive(t, claimAt)
	require.Greater(t, deductAt, claimAt, "operation claim must precede the quota deduction")

	migration, err := os.ReadFile("../../migrations/240_affiliate_ledger_operation_id.sql")
	require.NoError(t, err)
	require.Contains(t, string(migration), "CREATE UNIQUE INDEX IF NOT EXISTS idx_user_affiliate_ledger_operation_id")
	require.Contains(t, string(migration), "WHERE operation_id IS NOT NULL")
}
