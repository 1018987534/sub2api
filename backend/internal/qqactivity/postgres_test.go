package qqactivity

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/migrations"
	_ "github.com/lib/pq"
)

func TestPostgresActivityScenarios(t *testing.T) {
	file := os.Getenv("QQ_ACTIVITY_TEST_DSN_FILE")
	if file == "" {
		t.Skip("requires isolated PostgreSQL verification database")
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal("test DSN file unavailable")
	}
	db, err := sql.Open("postgres", strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	ctx := context.Background()
	schema := fmt.Sprintf("qq_activity_test_%d", time.Now().UnixNano())
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			t.Fatalf("SQL fixture failed: %v", err)
		}
	}
	var dbname string
	if err = db.QueryRow(`SELECT current_database()`).Scan(&dbname); err != nil || !strings.HasPrefix(dbname, "qq_activity_verify_") {
		t.Fatal("refusing fixtures outside dedicated verification database")
	}
	exec(`CREATE SCHEMA ` + schema)
	defer db.ExecContext(ctx, `DROP SCHEMA `+schema+` CASCADE`)
	exec(`SET search_path TO ` + schema)
	exec(`CREATE TABLE users(id BIGSERIAL PRIMARY KEY,email TEXT NOT NULL,username TEXT DEFAULT '',status TEXT DEFAULT 'active',balance NUMERIC(20,8) DEFAULT 0,created_at TIMESTAMPTZ DEFAULT clock_timestamp(),updated_at TIMESTAMPTZ DEFAULT NOW(),deleted_at TIMESTAMPTZ)`)
	exec(`CREATE TABLE auth_identities(user_id BIGINT,provider_type TEXT,provider_subject TEXT,verified_at TIMESTAMPTZ,metadata JSONB)`)
	exec(`CREATE TABLE user_affiliates(user_id BIGINT PRIMARY KEY,aff_code TEXT UNIQUE,aff_quota NUMERIC(20,8) DEFAULT 0,aff_history_quota NUMERIC(20,8) DEFAULT 0,created_at TIMESTAMPTZ DEFAULT NOW(),updated_at TIMESTAMPTZ DEFAULT NOW())`)
	exec(`CREATE TABLE user_affiliate_ledger(id BIGSERIAL PRIMARY KEY,user_id BIGINT,action TEXT,amount NUMERIC(20,8),source_user_id BIGINT)`)
	migration, err := migrations.FS.ReadFile("248_qq_activity.sql")
	if err != nil {
		t.Fatal(err)
	}
	exec(string(migration))
	exec(string(migration)) // additive and idempotent
	identityMigration, err := migrations.FS.ReadFile("249_qq_activity_identity_owners.sql")
	if err != nil {
		t.Fatal(err)
	}
	exec(string(identityMigration))
	store := NewStore(db, nil)
	addUser := func(qq, email, username string, verified bool) int64 {
		t.Helper()
		var id int64
		if err := db.QueryRow(`INSERT INTO users(email,username) VALUES($1,$2) RETURNING id`, email, username).Scan(&id); err != nil {
			t.Fatal(err)
		}
		if verified {
			exec(`INSERT INTO auth_identities VALUES($1,'email',$2,NOW(),'{"email_ownership_verified":true}')`, id, qq+"@qq.com")
		}
		return id
	}
	inviter := addUser("12345678", "12345678@qq.com", "", true)
	oldInvitee := addUser("23456789", "23456789@qq.com", "", true)
	baselineMember := addUser("24456789", "24456789@qq.com", "", true)
	addUser("77777777", "other@example.com", "", false)
	groups := []GroupSnapshot{{GroupID: "87654321", BotQQ: "99999999", ExpectedCount: 4, Members: []MemberSnapshot{{QQ: "99999999", Robot: true}, {QQ: "12345678"}, {QQ: "24456789"}, {QQ: "88888888"}}}}
	if err = store.Initialize(ctx, groups, true); err != nil {
		t.Fatal(err)
	}
	requireNumber := func(query string, want float64, args ...any) {
		t.Helper()
		var got float64
		if e := db.QueryRow(query, args...).Scan(&got); e != nil {
			t.Fatal(e)
		}
		if got != want {
			t.Fatalf("number got=%v want=%v SQL=%s", got, want, query)
		}
	}
	requireNumber(`SELECT COUNT(*) FROM qq_activity_baseline_users`, 4)
	requireNumber(`SELECT COUNT(*) FROM qq_activity_members`, 4)
	requireNumber(`SELECT COUNT(*) FROM qq_activity_bindings`, 2)
	requireNumber(`SELECT COUNT(*) FROM qq_activity_rewards`, 0)
	if store.Initialize(ctx, groups, true) == nil {
		t.Fatal("baseline was reset after launch")
	}
	process := func(id, qq, kind, command string, level *int, inviterQQ string) {
		t.Helper()
		event := Event{ID: id, GroupID: "87654321", QQ: qq, Kind: kind, Command: command, Time: time.Now().Unix(), Level: level, MemberConfirmed: true}
		if inviterQQ != "" {
			event.InviterQQ = inviterQQ
			event.EvidenceType = "direct_invite_pinned"
			event.EvidenceKey = "fixture:" + id
		}
		if e := store.Process(ctx, event); e != nil {
			t.Fatalf("event %s failed: %v", id, e)
		}
	}
	l7, l15, l16, l21 := 7, 15, 16, 21
	// Ensure subsequent integer-second join times are strictly after T0.
	exec(`UPDATE qq_activity_config SET started_at=started_at-INTERVAL '2 seconds'`)
	process("inviter-low", "12345678", "command", "绑定", &l7, "")
	process("baseline-member-left", "24456789", "leave", "", &l21, "")
	process("baseline-member-rejoined", "24456789", "join", "", &l21, "12345678")
	requireNumber(`SELECT balance FROM users WHERE id=$1`, 0, baselineMember)
	requireNumber(`SELECT COUNT(*) FROM qq_activity_rewards`, 0)
	requireNumber(`SELECT COUNT(*) FROM qq_activity_invitations WHERE invitee_qq='24456789'`, 0)
	if err = store.VerifyInvitation(ctx, "test-admin", "24456789", "12345678", "87654321", "fixture rejoin cannot become a new invitation"); err == nil {
		t.Fatal("manual review created an invitation for a baseline group member")
	}
	t.Log("baseline group member leaves/rejoins: newcomer=0 invitation=0; manual review cannot override")
	process("old-invitee-join", "23456789", "join", "", &l21, "12345678")
	requireNumber(`SELECT balance FROM users WHERE id=$1`, 0, oldInvitee)
	requireNumber(`SELECT aff_quota FROM user_affiliates WHERE user_id=$1`, 2, inviter)
	process("old-invitee-join", "23456789", "join", "", &l21, "12345678")
	process("old-invitee-rejoin", "23456789", "join", "", &l21, "12345678")
	requireNumber(`SELECT COUNT(*) FROM qq_activity_rewards`, 1)
	t.Log("existing website user first joins + inviter 7: newcomer=0 invitation=2; duplicate/rejoin no second payout")

	// A genuinely new account and post-launch join receive the independent awards.
	newcomer := addUser("34567890", "34567890@qq.com", "", true)
	process("new-join", "34567890", "join", "", &l16, "12345678")
	requireNumber(`SELECT balance FROM users WHERE id=$1`, 2, newcomer)
	requireNumber(`SELECT aff_quota FROM user_affiliates WHERE user_id=$1`, 4, inviter)
	process("repeat-bind", "34567890", "command", "绑定", &l16, "")
	requireNumber(`SELECT balance FROM users WHERE id=$1`, 2, newcomer)
	exec(`INSERT INTO qq_activity_groups(group_id,bot_qq) VALUES('76543210','99999999')`)
	if err = store.Process(ctx, Event{ID: "cross-group-rejoin", GroupID: "76543210", QQ: "34567890", Kind: "join", Time: time.Now().Unix(), Level: &l21, MemberConfirmed: true, InviterQQ: "33445566", EvidenceType: "direct_invite_pinned", EvidenceKey: "fixture-other-group"}); err != nil {
		t.Fatal(err)
	}
	requireNumber(`SELECT COUNT(*) FROM qq_activity_rewards WHERE subject_user_id=$1`, 2, newcomer)
	requireNumber(`SELECT COUNT(*) FROM qq_activity_invitations WHERE invitee_qq='34567890' AND inviter_qq='12345678' AND group_id='87654321'`, 1)
	t.Log("same QQ joins another activity group with another inviter: no extra rewards; first attribution retained")

	low := addUser("45678901", "45678901@qq.com", "", true)
	process("low-join", "45678901", "join", "", &l15, "12345678")
	requireNumber(`SELECT balance FROM users WHERE id=$1`, 0, low)
	requireNumber(`SELECT COUNT(*) FROM qq_activity_bindings WHERE user_id=$1`, 1, low)
	process("low-left", "45678901", "leave", "", &l15, "")
	process("low-rejoined-other-inviter", "45678901", "join", "", &l15, "33445566")
	requireNumber(`SELECT COUNT(*) FROM qq_activity_invitations WHERE invitee_qq='45678901' AND inviter_qq='12345678'`, 1)
	if err = store.VerifyInvitation(ctx, "test-admin", "45678901", "33445566", "87654321", "fixture cannot change first inviter before qualification"); err == nil {
		t.Fatal("manual review replaced an unqualified first inviter")
	}
	process("low-upgraded", "45678901", "command", "绑定", &l21, "")
	requireNumber(`SELECT balance FROM users WHERE id=$1`, 2, low)
	t.Log("level 15 binds without rewards; level 21 recheck grants exactly once")

	unknown := addUser("56789012", "56789012@qq.com", "", true)
	process("unknown-join", "56789012", "join", "", nil, "12345678")
	requireNumber(`SELECT balance FROM users WHERE id=$1`, 0, unknown)
	process("unknown-retry", "56789012", "level", "", &l21, "")
	requireNumber(`SELECT balance FROM users WHERE id=$1`, 2, unknown)

	// QQ email and profile fallback both bind without verification or a code.
	fallback := addUser("67890123", "fallback@example.com", "67890123@qq.com", false)
	process("fallback-join", "67890123", "join", "", &l21, "")
	requireNumber(`SELECT COUNT(*) FROM qq_activity_bindings WHERE user_id=$1`, 1, fallback)
	requireNumber(`SELECT balance FROM users WHERE id=$1`, 2, fallback)
	process("repeat-fallback", "67890123", "command", "绑定", &l21, "")
	requireNumber(`SELECT balance FROM users WHERE id=$1`, 2, fallback)
	unverified := addUser("78901234", "78901234@qq.com", "", false)
	process("unverified-join", "78901234", "join", "", &l21, "")
	requireNumber(`SELECT COUNT(*) FROM qq_activity_bindings WHERE user_id=$1`, 1, unverified)
	requireNumber(`SELECT COUNT(*) FROM qq_activity_claims`, 0)
	t.Log("unverified QQ email and profile username bind directly; repeat binding cannot duplicate awards")

	if err = store.Control(ctx, "test-admin", "paused"); err != nil {
		t.Fatal(err)
	}
	paused := addUser("89012345", "89012345@qq.com", "", true)
	process("paused-join", "89012345", "join", "", &l21, "12345678")
	requireNumber(`SELECT balance FROM users WHERE id=$1`, 0, paused)
	requireNumber(`SELECT COUNT(*) FROM qq_activity_rewards WHERE subject_user_id=$1 AND status='pending'`, 2, paused)
	if err = store.Control(ctx, "test-admin", "active"); err != nil {
		t.Fatal(err)
	}
	requireNumber(`SELECT balance FROM users WHERE id=$1`, 2, paused)
	t.Log("close pauses payment but retains binding/qualified rows; reopen settles pending once")

	off := false
	on := true
	if err = store.UpdateSettings(ctx, "test-admin", SettingsUpdate{NewcomerEnabled: &off, InvitationEnabled: &off, GroupMessagesEnabled: &off}); err != nil {
		t.Fatal(err)
	}
	switched := addUser("90123456", "90123456@qq.com", "", true)
	process("switches-off", "90123456", "join", "", &l21, "12345678")
	requireNumber(`SELECT balance FROM users WHERE id=$1`, 0, switched)
	requireNumber(`SELECT COUNT(*) FROM qq_activity_outbox WHERE delivered_at IS NULL`, 0)
	if err = store.UpdateSettings(ctx, "test-admin", SettingsUpdate{NewcomerEnabled: &on, InvitationEnabled: &on}); err != nil {
		t.Fatal(err)
	}
	requireNumber(`SELECT balance FROM users WHERE id=$1`, 2, switched)

	// Distinct event IDs arriving concurrently still pay once per subject.
	concurrent := addUser("11223344", "11223344@qq.com", "", true)
	e := Event{ID: "concurrent", GroupID: "87654321", QQ: "11223344", Kind: "join", Time: time.Now().Unix(), Level: &l21, MemberConfirmed: true, InviterQQ: "12345678", EvidenceType: "direct_invite_pinned", EvidenceKey: "fixture-concurrent"}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		duplicate := e
		duplicate.ID = fmt.Sprintf("concurrent-%d", i)
		go func(event Event) { defer wg.Done(); errs <- store.Process(ctx, event) }(duplicate)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	requireNumber(`SELECT balance FROM users WHERE id=$1`, 2, concurrent)
	requireNumber(`SELECT COUNT(*) FROM qq_activity_rewards WHERE subject_user_id=$1`, 2, concurrent)
	process("concurrent-0", "11223344", "join", "", &l21, "12345678")
	requireNumber(`SELECT COUNT(*) FROM qq_activity_rewards WHERE subject_user_id=$1`, 2, concurrent)
	t.Log("8 distinct concurrent join events plus exact replay: newcomer=2 invitation=2 total once")

	// A real direct invitation may qualify before the inviter has a website account.
	waiting := addUser("22334455", "22334455@qq.com", "", true)
	process("waiting-inviter", "22334455", "join", "", &l21, "33445566")
	requireNumber(`SELECT COUNT(*) FROM qq_activity_rewards WHERE subject_user_id=$1 AND kind='invitation' AND recipient_user_id IS NULL`, 1, waiting)
	later := addUser("33445566", "33445566@qq.com", "", true)
	process("inviter-after", "33445566", "join", "", &l7, "")
	requireNumber(`SELECT aff_quota FROM user_affiliates WHERE user_id=$1`, 2, later)
	requireNumber(`SELECT balance FROM users WHERE id=$1`, 0, later)

	// Missing invitation proof cannot pay the operator or the registration referrer.
	noEvidence := addUser("44556677", "44556677@qq.com", "", true)
	process("unproven-join", "44556677", "join", "", &l21, "")
	requireNumber(`SELECT COUNT(*) FROM qq_activity_rewards WHERE subject_user_id=$1 AND kind='invitation'`, 0, noEvidence)
	process("unproven-rejoin-other-inviter", "44556677", "join", "", &l21, "33445566")
	requireNumber(`SELECT COUNT(*) FROM qq_activity_invitations WHERE invitee_qq='44556677' AND inviter_qq=''`, 1)
	if err = store.VerifyInvitation(ctx, "test-admin", "44556677", "12345678", "87654321", "fixture verified actual invitation record 20261024"); err != nil {
		t.Fatal(err)
	}
	requireNumber(`SELECT COUNT(*) FROM qq_activity_rewards WHERE subject_user_id=$1 AND kind='invitation' AND status='paid'`, 1, noEvidence)

	report, err := store.AdminReport(ctx)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(report)
	if strings.Contains(string(data), "12345678@qq.com") || strings.Contains(string(data), "fallback@example.com") {
		t.Fatal("raw email leaked in admin/export")
	}
	summary, err := store.UserSummary(ctx, oldInvitee)
	if err != nil {
		t.Fatal(err)
	}
	if summary.WelcomeText != NotEligible {
		t.Fatal("legacy denial explains personal eligibility")
	}
	if err = store.Control(ctx, "test-admin", "ended"); err != nil {
		t.Fatal(err)
	}
	afterEnd := addUser("55667788", "55667788@qq.com", "", true)
	process("after-end", "55667788", "join", "", &l21, "12345678")
	requireNumber(`SELECT COUNT(*) FROM qq_activity_bindings WHERE user_id=$1`, 1, afterEnd)
	requireNumber(`SELECT COUNT(*) FROM qq_activity_rewards WHERE subject_user_id=$1`, 0, afterEnd)
	t.Log("ended campaign still binds, cannot establish new awards; all reports and exports mask emails")
}
