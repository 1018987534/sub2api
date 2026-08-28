package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

const lotteryCaptchaKeyPrefix = "lottery:captcha:"

var takeLotteryCaptchaScript = redis.NewScript(`
local value = redis.call('GET', KEYS[1])
if value then
  redis.call('DEL', KEYS[1])
end
return value
`)

type lotteryCaptchaStore struct {
	rdb *redis.Client
}

func NewLotteryCaptchaStore(rdb *redis.Client) service.LotteryCaptchaStore {
	return &lotteryCaptchaStore{rdb: rdb}
}

func (s *lotteryCaptchaStore) Save(ctx context.Context, id string, record service.LotteryCaptchaRecord, ttl time.Duration) error {
	data, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("marshal lottery captcha record: %w", err)
	}
	if err := s.rdb.Set(ctx, lotteryCaptchaKeyPrefix+id, data, ttl).Err(); err != nil {
		return fmt.Errorf("save lottery captcha record: %w", err)
	}
	return nil
}

func (s *lotteryCaptchaStore) Take(ctx context.Context, id string) (service.LotteryCaptchaRecord, error) {
	value, err := takeLotteryCaptchaScript.Run(ctx, s.rdb, []string{lotteryCaptchaKeyPrefix + id}).Text()
	if err == redis.Nil {
		return service.LotteryCaptchaRecord{}, service.ErrLotteryCaptchaRecordNotFound
	}
	if err != nil {
		return service.LotteryCaptchaRecord{}, fmt.Errorf("take lottery captcha record: %w", err)
	}
	var record service.LotteryCaptchaRecord
	if err := json.Unmarshal([]byte(value), &record); err != nil {
		return service.LotteryCaptchaRecord{}, fmt.Errorf("decode lottery captcha record: %w", err)
	}
	return record, nil
}
