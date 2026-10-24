package clientdownload

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/migrations"
	_ "github.com/lib/pq"
)

func testManifest() Manifest {
	return Manifest{1, map[string]File{"ccswitch-macos": {URL: "/downloads/clients/releases/test-01/test.dmg", Name: "test.dmg", Bytes: 35 * 1024 * 1024}}}
}
func TestManifestAndIPValidation(t *testing.T) {
	for _, p := range []string{"/downloads/clients/releases/../test.dmg", "https://evil.test/test.dmg", "/downloads/clients/releases/test/../../passwd", "/_protected/client-downloads/test.dmg"} {
		f := testManifest().Files["ccswitch-macos"]
		f.URL = p
		if validFile(f) {
			t.Fatalf("accepted unsafe path %q", p)
		}
	}
	a, _ := ipBucket("2001:db8:1:2::1")
	b, _ := ipBucket("2001:db8:1:2::9876")
	c, _ := ipBucket("2001:db8:1:3::1")
	if a != b || a == c {
		t.Fatal("IPv6 /64 not enforced")
	}
	if _, err := ipBucket("forged"); err == nil {
		t.Fatal("invalid peer IP accepted")
	}
	var hits atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		fmt.Fprint(w, `{"schema":1,"files":{"ccswitch-macos":{"url":"/downloads/clients/releases/test-01/test.dmg","name":"test.dmg","bytes":100}}}`)
	}))
	defer server.Close()
	load := newManifestLoader(server.URL)
	for i := 0; i < 3; i++ {
		if _, err := load(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if hits.Load() != 1 {
		t.Fatal("small catalog not cached")
	}
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, server.URL, 302) }))
	defer bad.Close()
	if _, err := newManifestLoader(bad.URL)(context.Background()); err == nil {
		t.Fatal("catalog redirect accepted")
	}
	s := NewService(nil, func(context.Context) (Manifest, error) { return testManifest(), nil })
	if _, err := s.Issue(context.Background(), 1, "ccswitch-macos"); !errors.Is(err, ErrUnavailable) {
		t.Fatal("nil DB did not fail closed")
	}
}

func fixture(t *testing.T) (*sql.DB, *Service) {
	t.Helper()
	file := os.Getenv("CLIENT_DOWNLOAD_TEST_DSN_FILE")
	if file == "" {
		t.Skip("requires isolated PostgreSQL")
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal("DSN unavailable")
	}
	dsn := strings.TrimSpace(string(raw))
	admin, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	var dbname string
	if admin.QueryRow("SELECT current_database()").Scan(&dbname) != nil || !strings.HasPrefix(dbname, "qq_activity_verify_") {
		t.Fatal("refusing non-verification database")
	}
	schema := fmt.Sprintf("client_download_test_%d", time.Now().UnixNano())
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
	db.SetMaxOpenConns(24)
	t.Cleanup(func() { db.Close() })
	m, err := migrations.FS.ReadFile("251_client_download_guard.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(string(m)); err != nil {
		t.Fatal(err)
	}
	s := NewService(db, func(context.Context) (Manifest, error) { return testManifest(), nil })
	return db, s
}

func TestPostgresDownloadBudgetsAndTickets(t *testing.T) {
	ctx := context.Background()
	t.Run("account_starts_full_size_and_restart_persistence", func(t *testing.T) {
		db, s := fixture(t)
		var last string
		for i := 0; i < 3; i++ {
			token, err := s.Issue(ctx, 1, "ccswitch-macos")
			if err != nil {
				t.Fatal(err)
			}
			last = token
		}
		restarted := NewService(db, s.load)
		if _, err := restarted.Issue(ctx, 1, "ccswitch-macos"); !errors.Is(err, ErrLimited) {
			t.Fatalf("restart reset allowance: %v", err)
		}
		f, err := s.Consume(ctx, last, "198.51.100.1")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.Consume(ctx, last, "198.51.100.1"); !errors.Is(err, ErrInvalidTicket) {
			t.Fatalf("replayed ticket: %v", err)
		}
		var used int64
		if err = db.QueryRow("SELECT used_bytes FROM client_download_budgets WHERE bucket LIKE 'user:1:%'").Scan(&used); err != nil {
			t.Fatal(err)
		}
		if used != 3*f.Bytes {
			t.Fatalf("full reservation lost: %d", used)
		}
		t.Logf("starts=3 fourth=limited restart=limited reserved_bytes=%d replay=denied", used)
	})
	t.Run("account_byte_cap_and_rollback_without_partial_counters", func(t *testing.T) {
		db, s := fixture(t)
		s.policy.UserDailyBytes = 35 * 1024 * 1024
		if _, err := s.Issue(ctx, 1, "ccswitch-macos"); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Issue(ctx, 1, "ccswitch-macos"); !errors.Is(err, ErrLimited) {
			t.Fatal(err)
		}
		var count int
		if err := db.QueryRow("SELECT used_count FROM client_download_budgets WHERE bucket LIKE 'global:day:%'").Scan(&count); err != nil || count != 1 {
			t.Fatalf("denied request changed counters: %d %v", count, err)
		}
		t.Log("account byte cap denied second request; no partial global increments")
	})
	t.Run("actual_transfer_ip_starts_bytes_and_ipv6_prefix", func(t *testing.T) {
		_, s := fixture(t)
		s.policy.IPDailyStarts = 2
		s.policy.IPDailyBytes = 2 * 35 * 1024 * 1024
		for i := 1; i <= 3; i++ {
			token, err := s.Issue(ctx, int64(i), "ccswitch-macos")
			if err != nil {
				t.Fatal(err)
			}
			_, err = s.Consume(ctx, token, fmt.Sprintf("2001:db8:1:2::%d", i))
			if i < 3 && err != nil {
				t.Fatal(err)
			}
			if i == 3 && !errors.Is(err, ErrLimited) {
				t.Fatalf("IP allowance bypassed: %v", err)
			}
		}
		t.Log("two transfers allowed; third account on another address in same IPv6 /64 denied")
	})
	t.Run("concurrent_global_daily_reservations_never_exceed_budget", func(t *testing.T) {
		db, s := fixture(t)
		s.policy.DailyBytes = 2 * 35 * 1024 * 1024
		var wg sync.WaitGroup
		var accepted, limited atomic.Int64
		for i := 1; i <= 20; i++ {
			wg.Add(1)
			go func(id int64) {
				defer wg.Done()
				_, err := s.Issue(ctx, id, "ccswitch-macos")
				if err == nil {
					accepted.Add(1)
				} else if errors.Is(err, ErrLimited) {
					limited.Add(1)
				} else {
					t.Errorf("reservation: %v", err)
				}
			}(int64(i))
		}
		wg.Wait()
		if accepted.Load() != 2 || limited.Load() != 18 {
			t.Fatalf("accepted=%d limited=%d", accepted.Load(), limited.Load())
		}
		var used int64
		if err := db.QueryRow("SELECT used_bytes FROM client_download_budgets WHERE bucket LIKE 'global:day:%'").Scan(&used); err != nil || used != s.policy.DailyBytes {
			t.Fatalf("global overspend: %d %v", used, err)
		}
		t.Logf("20 simultaneous starts: accepted=%d denied=%d reserved=%d", accepted.Load(), limited.Load(), used)
	})
	t.Run("monthly_budget_survives_next_day", func(t *testing.T) {
		_, s := fixture(t)
		s.policy.MonthlyBytes = 2 * 35 * 1024 * 1024
		if _, err := s.Issue(ctx, 1, "ccswitch-macos"); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Issue(ctx, 2, "ccswitch-macos"); err != nil {
			t.Fatal(err)
		}
		current := time.Now()
		s.now = func() time.Time { return current.Add(24 * time.Hour) }
		if _, err := s.Issue(ctx, 3, "ccswitch-macos"); !errors.Is(err, ErrLimited) {
			t.Fatalf("monthly budget reset next day: %v", err)
		}
		t.Log("new day cannot bypass exhausted monthly budget")
	})
	t.Run("concurrent_replay_expiry_and_replacement", func(t *testing.T) {
		db, s := fixture(t)
		old, err := s.Issue(ctx, 1, "ccswitch-macos")
		if err != nil {
			t.Fatal(err)
		}
		token, err := s.Issue(ctx, 1, "ccswitch-macos")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.Consume(ctx, old, "198.51.100.2"); !errors.Is(err, ErrInvalidTicket) {
			t.Fatal("replaced token still valid")
		}
		var wg sync.WaitGroup
		var accepted atomic.Int64
		for i := 0; i < 12; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, err := s.Consume(ctx, token, "198.51.100.2")
				if err == nil {
					accepted.Add(1)
				} else if !errors.Is(err, ErrInvalidTicket) {
					t.Errorf("consume: %v", err)
				}
			}()
		}
		wg.Wait()
		if accepted.Load() != 1 {
			t.Fatalf("concurrent replay accepted=%d", accepted.Load())
		}
		token, err = s.Issue(ctx, 2, "ccswitch-macos")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec("UPDATE client_download_tickets SET expires_at=NOW()-INTERVAL '1 second'"); err != nil {
			t.Fatal(err)
		}
		if _, err = s.Consume(ctx, token, "198.51.100.2"); !errors.Is(err, ErrInvalidTicket) {
			t.Fatal("expired token valid")
		}
		t.Log("concurrent replay=1/12 authorized; replaced and expired tickets denied")
	})
}
