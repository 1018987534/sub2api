package qqactivity

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/Wei-Shaw/sub2api/migrations"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

func autoBindingFixture(t *testing.T) (*sql.DB, *Store) { return autoBindingFixtureAt(t, true) }

func autoBindingFixtureAt(t *testing.T, mail bool) (*sql.DB, *Store) {
	t.Helper()
	file := os.Getenv("QQ_ACTIVITY_TEST_DSN_FILE")
	if file == "" {
		t.Skip("requires isolated PostgreSQL verification database")
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal("test DSN unavailable")
	}
	dsn := strings.TrimSpace(string(raw))
	admin, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	var name string
	if err = admin.QueryRow("SELECT current_database()").Scan(&name); err != nil || !strings.HasPrefix(name, "qq_activity_verify_") {
		t.Fatal("refusing non-verification database")
	}
	schema := fmt.Sprintf("qq_auto_%d", time.Now().UnixNano())
	if _, err = admin.Exec("CREATE SCHEMA " + schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { admin.Exec("DROP SCHEMA " + schema + " CASCADE"); admin.Close() })
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		u, e := url.Parse(dsn)
		if e != nil {
			t.Fatal(e)
		}
		q := u.Query()
		q.Set("search_path", schema)
		u.RawQuery = q.Encode()
		dsn = u.String()
	} else {
		dsn += " search_path=" + schema
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(8)
	t.Cleanup(func() { db.Close() })
	queries := []string{
		"CREATE TABLE users(id BIGSERIAL PRIMARY KEY,email TEXT NOT NULL,username TEXT DEFAULT '',status TEXT DEFAULT 'active',balance NUMERIC(20,8) DEFAULT 0,created_at TIMESTAMPTZ DEFAULT clock_timestamp(),updated_at TIMESTAMPTZ DEFAULT NOW(),deleted_at TIMESTAMPTZ)",
		"CREATE TABLE auth_identities(user_id BIGINT,provider_type TEXT,provider_subject TEXT,verified_at TIMESTAMPTZ,metadata JSONB)",
		"CREATE TABLE user_affiliates(user_id BIGINT PRIMARY KEY,aff_code TEXT UNIQUE,aff_quota NUMERIC(20,8) DEFAULT 0,aff_history_quota NUMERIC(20,8) DEFAULT 0,created_at TIMESTAMPTZ DEFAULT NOW(),updated_at TIMESTAMPTZ DEFAULT NOW())",
		"CREATE TABLE user_affiliate_ledger(id BIGSERIAL PRIMARY KEY,user_id BIGINT,action TEXT,amount NUMERIC(20,8),source_user_id BIGINT)",
	}
	for _, q := range queries {
		if _, err = db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	files := []string{"248_qq_activity.sql", "249_qq_activity_identity_owners.sql", "250_qq_activity_numeric_username.sql"}
	if mail {
		files = append(files, "251_qq_activity_email_binding.sql")
	}
	files = append(files, "252_qq_activity_binding_display.sql")
	for _, file := range files {
		data, e := migrations.FS.ReadFile(file)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = db.Exec(string(data)); e != nil {
			t.Fatal(e)
		}
	}
	return db, NewStore(db, nil)
}
func autoAdd(t *testing.T, db *sql.DB, email, username string) int64 {
	t.Helper()
	var id int64
	if e := db.QueryRow("INSERT INTO users(email,username) VALUES($1,$2) RETURNING id", email, username).Scan(&id); e != nil {
		t.Fatal(e)
	}
	return id
}
func autoCount(t *testing.T, db *sql.DB, q string, want int, args ...any) {
	t.Helper()
	var n int
	if e := db.QueryRow(q, args...).Scan(&n); e != nil {
		t.Fatal(e)
	}
	t.Logf("observed=%d expected=%d query=%s", n, want, q)
	if n != want {
		t.Errorf("observed=%d expected=%d", n, want)
	}
}
func autoInitialize(t *testing.T, s *Store, qqs ...string) {
	t.Helper()
	members := []MemberSnapshot{{QQ: "99999999", Robot: true}}
	for _, qq := range qqs {
		members = append(members, MemberSnapshot{QQ: qq})
	}
	if e := s.Initialize(context.Background(), []GroupSnapshot{{GroupID: "87654321", BotQQ: "99999999", ExpectedCount: len(members), Members: members}}, true); e != nil {
		t.Fatal(e)
	}
}
func TestPostgresAutomaticBindingAndIdentityUniqueness(t *testing.T) {
	ctx := context.Background()
	db, s := autoBindingFixture(t)
	registered := autoAdd(t, db, "12345001@qq.com", "")
	alias := autoAdd(t, db, "fallback@example.test", "12345002@qq.com")
	historical := autoAdd(t, db, "legacy@example.test", "")
	if _, err := db.Exec("INSERT INTO qq_activity_bindings(qq,user_id,source) VALUES('12345003',$1,'matched_username')", historical); err != nil {
		t.Fatal(err)
	}
	autoInitialize(t, s, "12345001", "12345002", "12345003", "12345004")
	autoCount(t, db, "SELECT count(*) FROM qq_activity_bindings", 2)
	autoCount(t, db, "SELECT count(*) FROM qq_activity_bindings WHERE user_id=$1", 1, registered)
	autoCount(t, db, "SELECT count(*) FROM qq_activity_bindings WHERE user_id=$1", 0, alias)
	autoCount(t, db, "SELECT count(*) FROM qq_activity_bindings WHERE user_id=$1", 1, historical)
	if _, err := db.Exec("UPDATE users SET username='12345004' WHERE id=$1", alias); err != nil {
		t.Fatal(err)
	}
	if err := s.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	autoCount(t, db, "SELECT count(*) FROM qq_activity_bindings", 2)
	autoAdd(t, db, "12345004@qq.com", "")
	if err := s.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	autoCount(t, db, "SELECT count(*) FROM qq_activity_bindings WHERE qq='12345004' AND source='matched_email'", 1)
	autoCount(t, db, "SELECT count(*) FROM qq_activity_claims", 0)
}

func TestPostgresHistoricalBindingsSurviveEmailMigration(t *testing.T) {
	db, s := autoBindingFixtureAt(t, false)
	legacy := autoAdd(t, db, "legacy@example.test", "12345002@qq.com")
	autoAdd(t, db, "unmatched@example.test", "12345003@qq.com")
	if _, err := db.Exec("INSERT INTO qq_activity_bindings(qq,user_id,source) VALUES('12345002',$1,'matched_username')", legacy); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO qq_activity_rewards(kind,recipient_qq,recipient_user_id,subject_qq,subject_user_id,status,paid_at) VALUES('newcomer','12345002',$1,'12345002',$1,'paid',NOW())", legacy); err != nil {
		t.Fatal(err)
	}
	b, err := migrations.FS.ReadFile("251_qq_activity_email_binding.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(string(b)); err != nil {
		t.Fatal(err)
	}
	autoInitialize(t, s, "12345002", "12345003")
	autoCount(t, db, "SELECT count(*) FROM qq_activity_bindings WHERE user_id=$1 AND source='matched_username'", 1, legacy)
	autoCount(t, db, "SELECT count(*) FROM qq_activity_binding_history WHERE user_id=$1 AND unbound_at IS NULL", 1, legacy)
	autoCount(t, db, "SELECT count(*) FROM qq_activity_rewards WHERE status='paid'", 1)
	autoCount(t, db, "SELECT count(*) FROM qq_activity_identity_owners WHERE qq='12345003'", 0)
	autoCount(t, db, "SELECT count(*) FROM qq_activity_identity_owners WHERE qq='12345002'", 1)
}
