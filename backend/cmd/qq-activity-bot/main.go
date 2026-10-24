// qq-activity-bot bridges a pinned, local NapCat instance to the control API.
// Durable input and transactional output queues keep retries from issuing money twice.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/qqactivity"
	"github.com/gorilla/websocket"
)

type configuration struct {
	HTTPURL               string   `json:"napcat_http_url"`
	WSURL                 string   `json:"napcat_ws_url"`
	TokenFile             string   `json:"napcat_token_file"`
	BridgeURL             string   `json:"bridge_url"`
	BridgeTokenFile       string   `json:"bridge_token_file"`
	StateDir              string   `json:"state_dir"`
	GroupIDs              []string `json:"group_ids"`
	RobotQQs              []string `json:"robot_qqs"`
	SourceVersion         string   `json:"napcat_source_version"`
	InitializeAndActivate bool     `json:"initialize_and_activate"`
}
type bot struct {
	cfg           configuration
	client        *http.Client
	napcatToken   string
	bridgeToken   string
	self          string
	mu            sync.RWMutex
	connected     bool
	lastHeartbeat time.Time
	cooldown      map[string]time.Time
}
type rawEvent struct {
	PostType      string          `json:"post_type"`
	MessageType   string          `json:"message_type"`
	NoticeType    string          `json:"notice_type"`
	SubType       string          `json:"sub_type"`
	MetaEventType string          `json:"meta_event_type"`
	GroupID       json.Number     `json:"group_id"`
	UserID        json.Number     `json:"user_id"`
	OperatorID    json.Number     `json:"operator_id"`
	MessageID     json.Number     `json:"message_id"`
	Time          int64           `json:"time"`
	RawMessage    string          `json:"raw_message"`
	Message       json.RawMessage `json:"message"`
}

func readSecret(path string) (string, error) {
	b, e := os.ReadFile(path)
	if e != nil {
		return "", e
	}
	v := strings.TrimSpace(string(b))
	if len(v) < 20 {
		return "", errors.New("credential file invalid")
	}
	return v, nil
}
func atomicWrite(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".pending-*")
	if err != nil {
		return err
	}
	temp := f.Name()
	defer os.Remove(temp)
	if err = f.Chmod(0600); err != nil {
		f.Close()
		return err
	}
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(temp, path)
}
func decode(data []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	return d.Decode(v)
}
func hash(data []byte) string { s := sha256.Sum256(data); return hex.EncodeToString(s[:]) }
func (b *bot) call(ctx context.Context, endpoint, token string, payload any, result any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := b.client.Do(req)
	if err != nil {
		return errors.New("upstream unavailable")
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return errors.New("upstream read failed")
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("upstream HTTP %d", resp.StatusCode)
	}
	if result != nil {
		return decode(body, result)
	}
	return nil
}
func (b *bot) onebot(ctx context.Context, action string, params any, result any) error {
	var r struct {
		Status  string          `json:"status"`
		Retcode int             `json:"retcode"`
		Data    json.RawMessage `json:"data"`
	}
	if err := b.call(ctx, strings.TrimRight(b.cfg.HTTPURL, "/")+"/"+action, b.napcatToken, params, &r); err != nil {
		return err
	}
	if r.Status != "ok" || r.Retcode != 0 {
		return errors.New("NapCat API pending")
	}
	if result != nil {
		return decode(r.Data, result)
	}
	return nil
}
func (b *bot) bridge(ctx context.Context, action string, fields map[string]any, result any) error {
	if fields == nil {
		fields = map[string]any{}
	}
	fields["action"] = action
	var r struct {
		Code int             `json:"code"`
		Data json.RawMessage `json:"data"`
	}
	if err := b.call(ctx, b.cfg.BridgeURL, b.bridgeToken, fields, &r); err != nil {
		return err
	}
	if r.Code != 0 {
		return errors.New("control operation pending")
	}
	if result != nil {
		return decode(r.Data, result)
	}
	return nil
}
func (b *bot) robot(qq string) bool {
	if qq == b.self {
		return true
	}
	for _, id := range b.cfg.RobotQQs {
		if id == qq {
			return true
		}
	}
	return false
}
func (b *bot) configuredGroup(group string) bool {
	for _, id := range b.cfg.GroupIDs {
		if id == group {
			return true
		}
	}
	return false
}
func (b *bot) membership(ctx context.Context, group, qq string) (bool, error) {
	var member struct {
		ID json.Number `json:"user_id"`
	}
	err := b.onebot(ctx, "get_group_member_info", map[string]any{"group_id": group, "user_id": qq, "no_cache": true}, &member)
	if err != nil {
		// A queued member may have left before processing. A fresh successful
		// full-list read distinguishes absence from an API outage so it cannot
		// permanently block the durable queue.
		var members []struct {
			ID json.Number `json:"user_id"`
		}
		if listErr := b.onebot(ctx, "get_group_member_list", map[string]any{"group_id": group, "no_cache": true}, &members); listErr != nil {
			return false, err
		}
		for _, m := range members {
			if m.ID.String() == qq {
				return true, nil
			}
		}
		return false, nil
	}
	return member.ID.String() == qq, nil
}
func (b *bot) level(ctx context.Context, qq string) *int {
	var info struct {
		Level *int `json:"qqLevel"`
	}
	if b.onebot(ctx, "get_stranger_info", map[string]any{"user_id": qq, "no_cache": true}, &info) != nil || info.Level == nil || *info.Level <= 0 {
		return nil
	}
	return info.Level
}

func directInviter(e rawEvent, version string) (string, string, string) {
	// v4.18.41 parseSysMessage maps GroupChange.decreaseType==131 to invite.
	// approve means an approver/operator, and is NEVER an invitation attribution.
	id := e.OperatorID.String()
	if version == "v4.18.41" && e.NoticeType == "group_increase" && e.SubType == "invite" && qqactivity.ValidQQ(id) && id != e.UserID.String() {
		return id, "direct_invite_pinned", fmt.Sprintf("napcat:%s:%s:%d:%s", e.GroupID, e.UserID, e.Time, id)
	}
	return "", "", ""
}
func eventText(e rawEvent) string {
	var segments []struct {
		Type string `json:"type"`
		Data struct {
			Text string `json:"text"`
		} `json:"data"`
	}
	if json.Unmarshal(e.Message, &segments) == nil {
		var t strings.Builder
		for _, s := range segments {
			if s.Type == "text" {
				t.WriteString(s.Data.Text)
			}
		}
		return strings.TrimSpace(t.String())
	}
	return strings.TrimSpace(e.RawMessage)
}
func (b *bot) record(raw []byte) error {
	var e rawEvent
	if decode(raw, &e) != nil {
		return nil
	}
	if e.MetaEventType == "heartbeat" {
		b.mu.Lock()
		b.lastHeartbeat = time.Now()
		b.mu.Unlock()
		return nil
	}
	if !b.configuredGroup(e.GroupID.String()) || !qqactivity.ValidQQ(e.UserID.String()) || b.robot(e.UserID.String()) {
		return nil
	}
	if e.PostType == "message" && e.MessageType == "group" {
		command, code, ok := qqactivity.RecognizeCommand(eventText(e))
		if !ok {
			return nil
		}
		if command != "绑定" || code == "" {
			key := e.UserID.String() + ":" + command
			last := b.cooldown[key]
			if time.Since(last) < 30*time.Second {
				return nil
			}
			b.cooldown[key] = time.Now()
		}
	} else if e.PostType != "notice" || (e.NoticeType != "group_increase" && e.NoticeType != "group_decrease") {
		return nil
	}
	path := filepath.Join(b.cfg.StateDir, "incoming", fmt.Sprintf("%020d-%s.json", e.Time, hash(raw)))
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	return atomicWrite(path, raw)
}
func (b *bot) consume(ctx context.Context, path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var e rawEvent
	if err = decode(raw, &e); err != nil {
		return err
	}
	event := qqactivity.Event{ID: hash(raw), GroupID: e.GroupID.String(), QQ: e.UserID.String(), Time: e.Time}
	if e.NoticeType == "group_decrease" {
		event.Kind = "leave"
	} else {
		event.MemberConfirmed, err = b.membership(ctx, event.GroupID, event.QQ)
		if err != nil {
			return err
		}
		if !event.MemberConfirmed {
			return os.Remove(path)
		}
		event.Level = b.level(ctx, event.QQ)
		if e.NoticeType == "group_increase" {
			event.Kind = "join"
			event.InviterQQ, event.EvidenceType, event.EvidenceKey = directInviter(e, b.cfg.SourceVersion)
		} else {
			event.Kind = "command"
			event.Command, event.Code, _ = qqactivity.RecognizeCommand(eventText(e))
		}
	}
	if err = b.bridge(ctx, "event", map[string]any{"event": event}, nil); err != nil {
		return err
	}
	return os.Remove(path)
}
func (b *bot) snapshot(ctx context.Context) ([]qqactivity.GroupSnapshot, error) {
	groups := []qqactivity.GroupSnapshot{}
	for _, id := range b.cfg.GroupIDs {
		var before, after struct {
			Count int `json:"member_count"`
		}
		if err := b.onebot(ctx, "get_group_info", map[string]any{"group_id": id, "no_cache": true}, &before); err != nil {
			return nil, err
		}
		var members []struct {
			ID     json.Number `json:"user_id"`
			Joined int64       `json:"join_time"`
		}
		if err := b.onebot(ctx, "get_group_member_list", map[string]any{"group_id": id, "no_cache": true}, &members); err != nil {
			return nil, err
		}
		if err := b.onebot(ctx, "get_group_info", map[string]any{"group_id": id, "no_cache": true}, &after); err != nil {
			return nil, err
		}
		if before.Count != after.Count || len(members) != after.Count {
			return nil, errors.New("member snapshot changing; retry")
		}
		g := qqactivity.GroupSnapshot{GroupID: id, BotQQ: b.self, ExpectedCount: after.Count}
		for _, m := range members {
			g.Members = append(g.Members, qqactivity.MemberSnapshot{QQ: m.ID.String(), Robot: b.robot(m.ID.String()), JoinedAt: m.Joined})
		}
		groups = append(groups, g)
	}
	return groups, nil
}
func (b *bot) ensureInitialized(ctx context.Context) error {
	var status struct {
		Config qqactivity.Config `json:"config"`
	}
	if err := b.bridge(ctx, "status", nil, &status); err != nil {
		return err
	}
	if status.Config.InitializedAt != nil {
		return nil
	}
	if !b.cfg.InitializeAndActivate {
		return errors.New("activity not initialized")
	}
	b.mu.RLock()
	connected := b.connected
	b.mu.RUnlock()
	if !connected {
		return errors.New("event stream not ready")
	}
	groups, err := b.snapshot(ctx)
	if err != nil {
		return err
	}
	if err = b.bridge(ctx, "initialize", map[string]any{"groups": groups, "activate": true}, nil); err != nil {
		return err
	}
	log.Printf("activity initialized with %d verified group snapshot(s); no baseline rewards issued", len(groups))
	return nil
}
func (b *bot) outputs(ctx context.Context) error {
	var messages []qqactivity.Outbox
	if err := b.bridge(ctx, "outbox", nil, &messages); err != nil {
		return err
	}
	for _, m := range messages {
		if !b.configuredGroup(m.GroupID) {
			continue
		}
		inGroup, err := b.membership(ctx, m.GroupID, m.QQ)
		if err != nil {
			return err
		}
		if !inGroup {
			if err = b.bridge(ctx, "ack", map[string]any{"id": m.ID, "delivered": true}, nil); err != nil {
				return err
			}
			continue
		}
		params := map[string]any{"group_id": m.GroupID, "message": []any{map[string]any{"type": "at", "data": map[string]string{"qq": m.QQ}}, map[string]any{"type": "text", "data": map[string]string{"text": " " + qqactivity.MaskText(m.Text)}}}}
		sent := b.onebot(ctx, "send_group_msg", params, nil) == nil
		if err = b.bridge(ctx, "ack", map[string]any{"id": m.ID, "delivered": sent}, nil); err != nil {
			return err
		}
		if !sent {
			return errors.New("group notification deferred")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(1200 * time.Millisecond):
		}
	}
	return nil
}
func (b *bot) levelChecks(ctx context.Context) error {
	var events []qqactivity.Event
	if err := b.bridge(ctx, "levels", nil, &events); err != nil {
		return err
	}
	for _, e := range events {
		ok, err := b.membership(ctx, e.GroupID, e.QQ)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		e.ID = fmt.Sprintf("level:%s:%d", e.QQ, time.Now().Unix()/60)
		e.Time = time.Now().Unix()
		e.MemberConfirmed = true
		e.Level = b.level(ctx, e.QQ)
		if err = b.bridge(ctx, "event", map[string]any{"event": e}, nil); err != nil {
			return err
		}
	}
	return nil
}
func (b *bot) heartbeatFile(healthy bool) {
	b.mu.RLock()
	connected, last := b.connected, b.lastHeartbeat
	b.mu.RUnlock()
	entries, _ := os.ReadDir(filepath.Join(b.cfg.StateDir, "incoming"))
	data, _ := json.Marshal(map[string]any{"updated_at": time.Now().UTC(), "connected": connected, "last_heartbeat": last, "control_ready": healthy, "queue_length": len(entries), "groups": len(b.cfg.GroupIDs)})
	_ = atomicWrite(filepath.Join(b.cfg.StateDir, "status.json"), data)
}
func (b *bot) worker(ctx context.Context) {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	lastLevels := time.Time{}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			func() {
				operation, cancel := context.WithTimeout(ctx, 50*time.Second)
				defer cancel()
				if err := b.ensureInitialized(operation); err != nil {
					b.heartbeatFile(false)
					return
				}
				entries, err := filepath.Glob(filepath.Join(b.cfg.StateDir, "incoming", "*.json"))
				if err != nil {
					return
				}
				sort.Strings(entries)
				for i, p := range entries {
					if i >= 30 {
						break
					}
					if err = b.consume(operation, p); err != nil {
						log.Print("queued activity event deferred")
						b.heartbeatFile(false)
						return
					}
				}
				if err = b.bridge(operation, "tick", nil, nil); err != nil {
					b.heartbeatFile(false)
					return
				}
				if time.Since(lastLevels) >= time.Minute {
					if err = b.levelChecks(operation); err != nil {
						b.heartbeatFile(false)
						return
					}
					lastLevels = time.Now()
				}
				if err = b.outputs(operation); err != nil {
					b.heartbeatFile(false)
					return
				}
				b.heartbeatFile(true)
			}()
		}
	}
}
func (b *bot) stream(ctx context.Context) {
	for ctx.Err() == nil {
		u, err := url.Parse(b.cfg.WSURL)
		if err != nil {
			return
		}
		header := http.Header{}
		header.Set("Authorization", "Bearer "+b.napcatToken)
		connection, _, err := websocket.DefaultDialer.DialContext(ctx, u.String(), header)
		if err == nil {
			connection.SetReadLimit(2 << 20)
			_ = connection.SetReadDeadline(time.Now().Add(90 * time.Second))
			b.mu.Lock()
			b.connected = true
			b.lastHeartbeat = time.Now()
			b.mu.Unlock()
			log.Print("NapCat event stream connected")
			done := make(chan struct{})
			go func() {
				select {
				case <-ctx.Done():
					connection.Close()
				case <-done:
				}
			}()
			for ctx.Err() == nil {
				_, raw, e := connection.ReadMessage()
				if e != nil {
					break
				}
				_ = connection.SetReadDeadline(time.Now().Add(90 * time.Second))
				if e = b.record(raw); e != nil {
					log.Print("durable event recording failed; reconnecting")
					break
				}
			}
			close(done)
			connection.Close()
			b.mu.Lock()
			b.connected = false
			b.mu.Unlock()
		}
		b.heartbeatFile(false)
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}
	}
}
func main() {
	configPath := flag.String("config", "/etc/qq-activity/config.json", "private daemon configuration")
	probe := flag.Bool("probe", false, "read-only NapCat and activity checks")
	flag.Parse()
	data, err := os.ReadFile(*configPath)
	if err != nil {
		log.Fatal("configuration unavailable")
	}
	var cfg configuration
	if json.Unmarshal(data, &cfg) != nil || len(cfg.GroupIDs) == 0 || cfg.SourceVersion != "v4.18.41" {
		log.Fatal("invalid or unverified configuration")
	}
	for _, endpoint := range []string{cfg.HTTPURL, cfg.WSURL, cfg.BridgeURL} {
		u, e := url.Parse(endpoint)
		if e != nil || u.Hostname() != "127.0.0.1" {
			log.Fatal("daemon endpoints must be loopback")
		}
	}
	for _, id := range cfg.GroupIDs {
		if !qqactivity.ValidQQ(id) {
			log.Fatal("invalid group scope")
		}
	}
	napcatToken, err := readSecret(cfg.TokenFile)
	if err != nil {
		log.Fatal("NapCat credential unavailable")
	}
	bridgeToken, err := readSecret(cfg.BridgeTokenFile)
	if err != nil {
		log.Fatal("bridge credential unavailable")
	}
	b := &bot{cfg: cfg, client: &http.Client{Timeout: 20 * time.Second}, napcatToken: napcatToken, bridgeToken: bridgeToken, cooldown: map[string]time.Time{}}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	var login struct {
		ID json.Number `json:"user_id"`
	}
	if b.onebot(ctx, "get_login_info", map[string]any{}, &login) != nil || !qqactivity.ValidQQ(login.ID.String()) {
		log.Fatal("QQ login not ready")
	}
	b.self = login.ID.String()
	if *probe {
		var status any
		if err = b.bridge(ctx, "status", nil, &status); err != nil {
			log.Fatal("activity control unavailable")
		}
		j, _ := json.Marshal(status)
		fmt.Println("napcat_login=ready group_scope=" + strconv.Itoa(len(cfg.GroupIDs)))
		fmt.Println(string(j))
		return
	}
	if err = os.MkdirAll(filepath.Join(cfg.StateDir, "incoming"), 0700); err != nil {
		log.Fatal("state directory unavailable")
	}
	go b.worker(ctx)
	b.stream(ctx)
}
