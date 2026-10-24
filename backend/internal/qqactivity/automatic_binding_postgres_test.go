package qqactivity

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/lib/pq"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func autoBindingFixture(t *testing.T) (*sql.DB, *Store) {
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
	for _, file := range []string{"248_qq_activity.sql", "249_qq_activity_identity_owners.sql"} {
		data, e := migrations.FS.ReadFile(file)
		if os.IsNotExist(e) {
			continue
		}
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
	t.Run("initialization_without_email_or_confirmation_code", func(t *testing.T) {
		db, s := autoBindingFixture(t)
		autoAdd(t, db, "12345001@qq.com", "")
		autoAdd(t, db, "fallback@example.test", "12345002@qq.com")
		autoInitialize(t, s, "12345001", "12345002")
		autoCount(t, db, "SELECT count(*) FROM qq_activity_bindings", 2)
		autoCount(t, db, "SELECT count(*) FROM qq_activity_claims", 0)
		autoCount(t, db, "SELECT count(*) FROM qq_activity_rewards", 0)
	})
	t.Run("profile_update_and_registration_bind_without_group_command", func(t *testing.T) {
		db, s := autoBindingFixture(t)
		id := autoAdd(t, db, "profile@example.test", "")
		autoInitialize(t, s, "12345003", "12345004")
		autoAdd(t, db, "12345003@qq.com", "")
		if _, e := db.Exec("UPDATE users SET username=' 12345004@QQ.COM ' WHERE id=$1", id); e != nil {
			t.Fatal(e)
		}
		if e := s.Tick(ctx); e != nil {
			t.Fatal(e)
		}
		if e := s.Tick(ctx); e != nil {
			t.Fatal(e)
		}
		autoCount(t, db, "SELECT count(*) FROM qq_activity_bindings", 2)
		autoCount(t, db, "SELECT count(*) FROM qq_activity_claims", 0)
		autoCount(t, db, "SELECT count(*) FROM qq_activity_rewards", 0)
	})
	t.Run("plain_bind_command_needs_no_qq_mail_verification", func(t *testing.T) {
		db, s := autoBindingFixture(t)
		autoInitialize(t, s)
		autoAdd(t, db, "unverified@example.test", "12345005@qq.com")
		level := 7
		if e := s.Process(ctx, Event{ID: "plain-bind", GroupID: "87654321", QQ: "12345005", Kind: "command", Command: "绑定", Time: time.Now().Unix(), MemberConfirmed: true, Level: &level}); e != nil {
			t.Fatal(e)
		}
		autoCount(t, db, "SELECT count(*) FROM qq_activity_bindings", 1)
		autoCount(t, db, "SELECT count(*) FROM qq_activity_claims", 0)
	})
	t.Run("email_username_and_binding_share_unique_qq", func(t *testing.T) {
		db, s := autoBindingFixture(t)
		owner := autoAdd(t, db, "12345006@qq.com", "")
		other := autoAdd(t, db, "other@example.test", "")
		_, e := db.Exec("UPDATE users SET username=' 12345006@QQ.COM ' WHERE id=$1", other)
		t.Logf("rename_to_registered_qq_rejected=%t", e != nil)
		if pqe, ok := e.(*pq.Error); !ok || pqe.Code != "23505" {
			t.Errorf("expected unique rejection, got %v", e)
		}
		autoAdd(t, db, "alias@example.test", "12345007@qq.com")
		_, e = db.Exec("INSERT INTO users(email) VALUES('12345007@qq.com')")
		t.Logf("register_username_qq_rejected=%t", e != nil)
		if pqe, ok := e.(*pq.Error); !ok || pqe.Code != "23505" {
			t.Errorf("expected cross-field unique rejection, got %v", e)
		}
		_, e = db.Exec("UPDATE users SET username=' 12345006@QQ.COM ' WHERE id=$1", owner)
		if e != nil {
			t.Fatal("same account can retain own QQ alias", e)
		}
		autoInitialize(t, s, "12345006")
		if _, e = db.Exec("UPDATE users SET email='changed@example.test',username='' WHERE id=$1", owner); e != nil {
			t.Fatal(e)
		}
		_, e = db.Exec("UPDATE users SET username='12345006@qq.com' WHERE id=$1", other)
		t.Logf("bound_qq_reuse_rejected=%t", e != nil)
		if e == nil {
			t.Error("bound QQ identity reused")
		}
	})
	t.Run("concurrent_profile_claim_has_exactly_one_owner", func(t *testing.T) {
		db, _ := autoBindingFixture(t)
		a := autoAdd(t, db, "a@example.test", "")
		b := autoAdd(t, db, "b@example.test", "")
		var wg sync.WaitGroup
		errs := make(chan error, 2)
		for _, id := range []int64{a, b} {
			wg.Add(1)
			go func(id int64) {
				defer wg.Done()
				_, e := db.Exec("UPDATE users SET username='12345008@qq.com' WHERE id=$1", id)
				errs <- e
			}(id)
		}
		wg.Wait()
		close(errs)
		success, rejected := 0, 0
		for e := range errs {
			if e == nil {
				success++
			} else if pe, ok := e.(*pq.Error); ok && pe.Code == "23505" {
				rejected++
			} else {
				t.Error(e)
			}
		}
		t.Logf("concurrent_success=%d concurrent_unique_rejections=%d", success, rejected)
		if success != 1 || rejected != 1 {
			t.Errorf("expected 1 success/1 conflict")
		}
	})
}
