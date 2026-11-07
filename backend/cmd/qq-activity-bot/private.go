package main

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/qqactivity"
)

func (b *bot) privateCipher() (cipher.AEAD, error) {
	if len(b.bridgeToken) < 48 {
		return nil, errors.New("private queue encryption credential unavailable")
	}
	key := sha256.Sum256([]byte("qq-activity-private-queue:v1:" + b.bridgeToken))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// Private OTPs must never enter the plaintext group-event spool. The existing
// bridge secret derives a purpose-specific authenticated encryption key; its
// value is never logged. Event IDs are keyed too, avoiding low-entropy hashes.
func (b *bot) recordPrivate(e rawEvent, raw []byte) error {
	if e.MessageID.String() == "" || e.Time <= 0 || len(eventText(e)) > 512 {
		return nil
	}
	aead, err := b.privateCipher()
	if err != nil {
		return err
	}
	mac := hmac.New(sha256.New, []byte(b.bridgeToken))
	_, _ = mac.Write([]byte("private:" + b.self + ":" + e.UserID.String() + ":" + e.MessageID.String() + ":" + fmt.Sprint(e.Time)))
	id := hex.EncodeToString(mac.Sum(nil))
	queued, err := filepath.Glob(filepath.Join(b.cfg.StateDir, "incoming", "*-"+id+".private"))
	if err != nil {
		return err
	}
	if len(queued) > 0 {
		return nil
	}
	// Reception order matters for several messages in the same second.
	path := filepath.Join(b.cfg.StateDir, "incoming", fmt.Sprintf("%020d-%s.private", time.Now().UnixNano(), id))
	nonce := make([]byte, aead.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return err
	}
	data := aead.Seal(nonce, nonce, raw, []byte(filepath.Base(path)))
	return atomicWrite(path, data)
}

func (b *bot) consumePrivate(ctx context.Context, path string, data []byte) error {
	aead, err := b.privateCipher()
	if err != nil {
		return err
	}
	if len(data) < aead.NonceSize()+aead.Overhead() {
		return errors.New("private event authentication failed")
	}
	raw, err := aead.Open(nil, data[:aead.NonceSize()], data[aead.NonceSize():], []byte(filepath.Base(path)))
	if err != nil {
		return errors.New("private event authentication failed")
	}
	var e rawEvent
	if err = decode(raw, &e); err != nil {
		return err
	}
	if e.PostType != "message" || e.MessageType != "private" || b.robot(e.UserID.String()) || !qqactivity.ValidQQ(e.UserID.String()) {
		return os.Remove(path)
	}
	// Delayed encrypted input cannot approve a newer conversation's challenge.
	if e.Time < time.Now().Add(-10*time.Minute).Unix() {
		return os.Remove(path)
	}
	parts := strings.Split(strings.TrimSuffix(filepath.Base(path), ".private"), "-")
	if len(parts) != 2 {
		return errors.New("invalid private queue identity")
	}
	event := qqactivity.PrivateEvent{ID: "private:" + parts[1], QQ: e.UserID.String(), Text: eventText(e), Time: e.Time}
	for _, group := range b.cfg.GroupIDs {
		member, err := b.membership(ctx, group, event.QQ)
		if err != nil {
			return err
		}
		if member {
			event.GroupID, event.MemberConfirmed = group, true
			break
		}
	}
	if event.MemberConfirmed {
		event.Level = b.level(ctx, event.QQ)
	}
	if err = b.bridge(ctx, "private", map[string]any{"private": event}, nil); err != nil {
		return err
	}
	return os.Remove(path)
}

func (b *bot) isFriend(ctx context.Context, qq string) (bool, error) {
	var friends []struct {
		ID json.Number `json:"user_id"`
	}
	if err := b.onebot(ctx, "get_friend_list", map[string]any{"no_cache": true}, &friends); err != nil {
		return false, err
	}
	for _, friend := range friends {
		if friend.ID.String() == qq {
			return true, nil
		}
	}
	return false, nil
}

func (b *bot) consumeFriend(ctx context.Context, path string, e rawEvent) error {
	qq := e.UserID.String()
	friend, err := b.isFriend(ctx, qq)
	if err != nil {
		return err
	}
	if !friend && e.Time < time.Now().Add(-24*time.Hour).Unix() {
		return os.Remove(path)
	}
	if !friend && e.PostType == "request" && e.RequestType == "friend" {
		if err = b.onebot(ctx, "set_friend_add_request", map[string]any{"flag": e.Flag, "approve": true}, nil); err != nil {
			return err
		}
		friend, err = b.isFriend(ctx, qq)
		if err != nil {
			return err
		}
	}
	if !friend {
		return errors.New("friendship confirmation pending")
	}
	if err = b.bridge(ctx, "friend", map[string]any{"qq": qq}, nil); err != nil {
		return err
	}
	return os.Remove(path)
}

func (b *bot) outputPrivate(ctx context.Context, m qqactivity.Outbox) error {
	if !qqactivity.ValidQQ(m.QQ) || b.robot(m.QQ) || m.GroupID != "" {
		return errors.New("invalid private recipient")
	}
	var receipt struct {
		MessageID json.Number `json:"message_id"`
	}
	if b.cfg.StateDir == "" {
		return errors.New("private receipt directory unavailable")
	}
	path := filepath.Join(b.cfg.StateDir, "receipts", "private-"+qqactivity.Hash(fmt.Sprintf("%d:%s:%s", m.ID, m.QQ, m.Text))+".json")
	confirmed := false
	if data, err := os.ReadFile(path); err == nil {
		if decode(data, &receipt) != nil {
			return errors.New("private receipt unreadable")
		}
		confirmed = receipt.MessageID.String() != "" && receipt.MessageID.String() != "0"
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	var err error
	if !confirmed {
		err = b.onebot(ctx, "send_private_msg", map[string]any{"user_id": m.QQ, "message": []any{map[string]any{"type": "text", "data": map[string]string{"text": qqactivity.MaskText(m.Text)}}}}, &receipt)
	}
	sent := err == nil && receipt.MessageID.String() != "" && receipt.MessageID.String() != "0"
	if sent && !confirmed {
		if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return err
		}
		data, err := json.Marshal(receipt)
		if err != nil {
			return err
		}
		if err = atomicWrite(path, data); err != nil {
			return err
		}
	}
	if err = b.bridge(ctx, "ack", map[string]any{"id": m.ID, "delivered": sent}, nil); err != nil {
		return err
	}
	if !sent {
		return errors.New("private notification deferred")
	}
	return nil
}
