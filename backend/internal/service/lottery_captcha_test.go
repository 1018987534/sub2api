package service

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type lotteryCaptchaMemoryStore struct {
	mu      sync.Mutex
	records map[string]LotteryCaptchaRecord
}

func newLotteryCaptchaMemoryStore() *lotteryCaptchaMemoryStore {
	return &lotteryCaptchaMemoryStore{records: make(map[string]LotteryCaptchaRecord)}
}

func (s *lotteryCaptchaMemoryStore) Save(_ context.Context, id string, record LotteryCaptchaRecord, _ time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records[id] = record
	return nil
}

func (s *lotteryCaptchaMemoryStore) Take(_ context.Context, id string) (LotteryCaptchaRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.records[id]
	if !ok {
		return LotteryCaptchaRecord{}, ErrLotteryCaptchaRecordNotFound
	}
	delete(s.records, id)
	return record, nil
}

func TestLotteryCaptchaGenerateAndConsumeOnce(t *testing.T) {
	store := newLotteryCaptchaMemoryStore()
	service, err := NewLotteryCaptchaService(store)
	require.NoError(t, err)

	challenge, err := service.Generate(context.Background(), 42, "203.0.113.42")
	require.NoError(t, err)
	require.NotEmpty(t, challenge.ID)
	require.Contains(t, challenge.Image, "data:image/jpeg;base64,")
	require.Contains(t, challenge.Thumb, "data:image/png;base64,")
	require.Positive(t, challenge.ThumbWidth)
	require.Positive(t, challenge.ThumbHeight)
	require.Equal(t, int(lotteryCaptchaTTL.Seconds()), challenge.ExpiresIn)

	record := store.records[challenge.ID]
	require.NoError(t, service.VerifyAndConsume(
		context.Background(), 42, "203.0.113.42", challenge.ID, record.TargetX, record.TargetY,
	))
	require.ErrorIs(t, service.VerifyAndConsume(
		context.Background(), 42, "203.0.113.42", challenge.ID, record.TargetX, record.TargetY,
	), ErrLotteryCaptchaExpired)
}

func TestLotteryCaptchaBindsChallengeToUserAndIP(t *testing.T) {
	store := newLotteryCaptchaMemoryStore()
	store.records["challenge-1"] = LotteryCaptchaRecord{
		UserID: 42, ClientIP: "203.0.113.42", TargetX: 150, TargetY: 80,
	}
	service := &LotteryCaptchaService{store: store}

	require.ErrorIs(t, service.VerifyAndConsume(
		context.Background(), 43, "203.0.113.42", "challenge-1", 150, 80,
	), ErrLotteryCaptchaInvalid)
	_, exists := store.records["challenge-1"]
	require.False(t, exists, "a failed attempt must still consume the challenge")
}

func TestLotteryCaptchaRejectsWrongSliderPosition(t *testing.T) {
	store := newLotteryCaptchaMemoryStore()
	store.records["challenge-2"] = LotteryCaptchaRecord{
		UserID: 42, ClientIP: "203.0.113.42", TargetX: 150, TargetY: 80,
	}
	service := &LotteryCaptchaService{store: store}

	require.ErrorIs(t, service.VerifyAndConsume(
		context.Background(), 42, "203.0.113.42", "challenge-2", 100, 80,
	), ErrLotteryCaptchaInvalid)
}
