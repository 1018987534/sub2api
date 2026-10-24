// Package clientdownload admits installer transfers before Nginx serves bytes.
package clientdownload

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"path"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	GiB                  int64 = 1 << 30
	TicketLifetime             = 2 * time.Minute
	PublishedManifestURL       = "https://xiaohondou.com/downloads/clients/manifest.json"
)

var (
	ErrLimited       = errors.New("download budget exhausted")
	ErrUnavailable   = errors.New("download guard unavailable")
	ErrInvalidTicket = errors.New("invalid or consumed download ticket")
	filePath         = regexp.MustCompile(`^/downloads/clients/releases/[A-Za-z0-9_-][A-Za-z0-9._-]*/[A-Za-z0-9_-][A-Za-z0-9._-]*\.(dmg|msi|msix)$`)
	fileIDs          = map[string]bool{"ccswitch-macos": true, "ccswitch-windows-x64": true, "ccswitch-windows-arm64": true, "chatgpt-macos": true, "chatgpt-windows-x64": true, "chatgpt-windows-arm64": true}
)

type File struct {
	URL   string `json:"url"`
	Name  string `json:"name"`
	Bytes int64  `json:"bytes"`
}
type Manifest struct {
	Schema int             `json:"schema"`
	Files  map[string]File `json:"files"`
}
type ManifestLoader func(context.Context) (Manifest, error)

func validFile(f File) bool {
	return filePath.MatchString(f.URL) && !strings.Contains(f.URL, "..") && path.Base(f.URL) == f.Name && f.Bytes > 0 && f.Bytes <= 4*GiB
}

// NewPublishedManifestLoader only fetches the small catalog from our fixed host.
// It never proxies or downloads an installer and never accepts a caller URL.
func NewPublishedManifestLoader() ManifestLoader { return newManifestLoader(PublishedManifestURL) }
func newManifestLoader(url string) ManifestLoader {
	var mu sync.Mutex
	var cached Manifest
	var until time.Time
	client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return func(ctx context.Context) (Manifest, error) {
		mu.Lock()
		defer mu.Unlock()
		if time.Now().Before(until) {
			return cached, nil
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return Manifest{}, err
		}
		res, err := client.Do(req)
		if err != nil {
			return Manifest{}, err
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusOK {
			return Manifest{}, ErrUnavailable
		}
		body, err := io.ReadAll(io.LimitReader(res.Body, 64*1024+1))
		if err != nil || len(body) > 64*1024 {
			return Manifest{}, ErrUnavailable
		}
		var m Manifest
		if json.Unmarshal(body, &m) != nil || m.Schema != 1 || len(m.Files) == 0 {
			return Manifest{}, ErrUnavailable
		}
		for id, f := range m.Files {
			if !fileIDs[id] || !validFile(f) {
				return Manifest{}, ErrUnavailable
			}
		}
		cached = m
		until = time.Now().Add(time.Minute)
		return m, nil
	}
}

type Policy struct {
	UserDailyBytes, IPDailyBytes, DailyBytes, MonthlyBytes int64
	UserDailyStarts, IPDailyStarts                         int64
}

func DefaultPolicy() Policy { return Policy{2 * GiB, 5 * GiB, 10 * GiB, 100 * GiB, 3, 10} }

type Service struct {
	db     *sql.DB
	load   ManifestLoader
	policy Policy
	now    func() time.Time
}

func NewService(db *sql.DB, load ManifestLoader) *Service {
	return &Service{db: db, load: load, policy: DefaultPolicy(), now: time.Now}
}

type bucket struct {
	key           string
	bytes, starts int64
	expires       time.Time
}

// IPv6 privacy addresses in one /64 share the IP allowance.
func ipBucket(raw string) (string, error) {
	address := net.ParseIP(raw)
	if address == nil {
		return "", ErrUnavailable
	}
	if address.To4() == nil {
		address = address.Mask(net.CIDRMask(64, 128))
	}
	h := sha256.Sum256([]byte(address.String()))
	return hex.EncodeToString(h[:]), nil
}

func (s *Service) Issue(ctx context.Context, userID int64, id string) (string, error) {
	if s.db == nil || s.load == nil || userID <= 0 {
		return "", ErrUnavailable
	}
	if !fileIDs[id] {
		return "", ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	m, err := s.load(ctx)
	if err != nil {
		return "", fmt.Errorf("manifest: %w", ErrUnavailable)
	}
	f, ok := m.Files[id]
	if m.Schema != 1 || !ok || !validFile(f) {
		return "", ErrUnavailable
	}
	now := s.now().In(time.FixedZone("Asia/Shanghai", 8*3600))
	dayEnd := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, now.Location())
	monthEnd := time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, now.Location())
	day, month := now.Format("20060102"), now.Format("200601")
	buckets := []bucket{
		{fmt.Sprintf("user:%d:%s", userID, day), s.policy.UserDailyBytes, s.policy.UserDailyStarts, dayEnd.Add(24 * time.Hour)},
		{"global:day:" + day, s.policy.DailyBytes, 0, dayEnd.Add(24 * time.Hour)},
		{"global:month:" + month, s.policy.MonthlyBytes, 0, monthEnd.Add(24 * time.Hour)},
	}
	sort.Slice(buckets, func(i, j int) bool { return buckets[i].key < buckets[j].key })
	var raw [32]byte
	if _, err = rand.Read(raw[:]); err != nil {
		return "", ErrUnavailable
	}
	token := hex.EncodeToString(raw[:])
	digest := sha256.Sum256([]byte(token))
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	if err = reserve(ctx, tx, buckets, f.Bytes); err != nil {
		return "", err
	}
	// A browser has one outstanding transfer; abandoned reservations are never
	// refunded, so range requests, aborts and unused tickets cannot bypass caps.
	if _, err = tx.ExecContext(ctx, `DELETE FROM client_download_tickets WHERE user_id=$1 OR expires_at<=NOW()`, userID); err != nil {
		return "", err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM client_download_budgets WHERE expires_at<=NOW()`); err != nil {
		return "", err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO client_download_tickets(token_sha256,user_id,file_path,filename,bytes,expires_at) VALUES($1,$2,$3,$4,$5,$6)`, hex.EncodeToString(digest[:]), userID, f.URL, f.Name, f.Bytes, s.now().Add(TicketLifetime)); err != nil {
		return "", err
	}
	if err = tx.Commit(); err != nil {
		return "", err
	}
	return token, nil
}

func reserve(ctx context.Context, tx *sql.Tx, buckets []bucket, fileBytes int64) error {
	for _, b := range buckets {
		if _, err := tx.ExecContext(ctx, `INSERT INTO client_download_budgets(bucket,expires_at) VALUES($1,$2) ON CONFLICT DO NOTHING`, b.key, b.expires); err != nil {
			return err
		}
		var used, count int64
		if err := tx.QueryRowContext(ctx, `SELECT used_bytes,used_count FROM client_download_budgets WHERE bucket=$1 FOR UPDATE`, b.key).Scan(&used, &count); err != nil {
			return err
		}
		if b.bytes <= 0 || used > b.bytes-fileBytes || (b.starts > 0 && count >= b.starts) {
			return ErrLimited
		}
	}
	for _, b := range buckets {
		if _, err := tx.ExecContext(ctx, `UPDATE client_download_budgets SET used_bytes=used_bytes+$1,used_count=used_count+1 WHERE bucket=$2`, fileBytes, b.key); err != nil {
			return err
		}
	}
	return nil
}

// Consume is a single atomic DELETE/RETURNING: even concurrent replay can
// authorize only one response. GET ranges reserve the entire file at issuance.
func (s *Service) Consume(ctx context.Context, token, clientIP string) (File, error) {
	if s.db == nil {
		return File{}, ErrUnavailable
	}
	if len(token) != 64 {
		return File{}, ErrInvalidTicket
	}
	if _, err := hex.DecodeString(token); err != nil {
		return File{}, ErrInvalidTicket
	}
	ipHash, err := ipBucket(clientIP)
	if err != nil {
		return File{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return File{}, err
	}
	defer tx.Rollback()
	digest := sha256.Sum256([]byte(token))
	var f File
	err = tx.QueryRowContext(ctx, `DELETE FROM client_download_tickets WHERE token_sha256=$1 AND expires_at>NOW() RETURNING file_path,filename,bytes`, hex.EncodeToString(digest[:])).Scan(&f.URL, &f.Name, &f.Bytes)
	if errors.Is(err, sql.ErrNoRows) {
		return File{}, ErrInvalidTicket
	}
	if err != nil {
		return File{}, err
	}
	if !validFile(f) {
		return File{}, ErrUnavailable
	}
	now := s.now().In(time.FixedZone("Asia/Shanghai", 8*3600))
	expires := time.Date(now.Year(), now.Month(), now.Day()+2, 0, 0, 0, 0, now.Location())
	if err = reserve(ctx, tx, []bucket{{"ip:" + ipHash + ":" + now.Format("20060102"), s.policy.IPDailyBytes, s.policy.IPDailyStarts, expires}}, f.Bytes); err != nil {
		return File{}, err
	}
	if err = tx.Commit(); err != nil {
		return File{}, err
	}
	return f, nil
}
