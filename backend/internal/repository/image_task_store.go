package repository

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

const (
	imageTaskKeyPrefix      = "image_task:"
	imageTaskIndexKeyPrefix = "image_tasks:api_key:"
)

type imageTaskStore struct {
	rdb *redis.Client
}

func NewImageTaskStore(rdb *redis.Client) service.ImageTaskStore {
	return &imageTaskStore{rdb: rdb}
}

func (s *imageTaskStore) Save(ctx context.Context, task *service.ImageTaskRecord, ttl time.Duration) error {
	data, err := json.Marshal(task)
	if err != nil {
		return err
	}
	_, err = s.rdb.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
		pipe.Set(ctx, imageTaskKey(task.ID), data, ttl)
		indexKey := imageTaskIndexKey(task.APIKeyID)
		pipe.ZAdd(ctx, indexKey, redis.Z{Score: float64(task.CreatedAt), Member: task.ID})
		pipe.Expire(ctx, indexKey, ttl)
		return nil
	})
	return err
}

func (s *imageTaskStore) Get(ctx context.Context, id string) (*service.ImageTaskRecord, error) {
	data, err := s.rdb.Get(ctx, imageTaskKey(id)).Bytes()
	if err != nil {
		if err == redis.Nil {
			return nil, service.ErrImageTaskNotFound
		}
		return nil, err
	}
	var task service.ImageTaskRecord
	if err := json.Unmarshal(data, &task); err != nil {
		return nil, err
	}
	return &task, nil
}

func (s *imageTaskStore) List(ctx context.Context, owner service.ImageTaskOwner, limit int) ([]*service.ImageTaskRecord, error) {
	if limit <= 0 {
		return []*service.ImageTaskRecord{}, nil
	}
	ids, err := s.rdb.ZRevRange(ctx, imageTaskIndexKey(owner.APIKeyID), 0, int64(limit-1)).Result()
	if err != nil || len(ids) == 0 {
		return []*service.ImageTaskRecord{}, err
	}
	keys := make([]string, 0, len(ids))
	for _, id := range ids {
		keys = append(keys, imageTaskKey(id))
	}
	values, err := s.rdb.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, err
	}
	records := make([]*service.ImageTaskRecord, 0, len(values))
	staleIDs := make([]any, 0)
	for index, value := range values {
		encoded, ok := value.(string)
		if !ok || encoded == "" {
			staleIDs = append(staleIDs, ids[index])
			continue
		}
		var task service.ImageTaskRecord
		if json.Unmarshal([]byte(encoded), &task) != nil {
			staleIDs = append(staleIDs, ids[index])
			continue
		}
		if task.UserID != owner.UserID || task.APIKeyID != owner.APIKeyID {
			continue
		}
		records = append(records, &task)
	}
	if len(staleIDs) > 0 {
		_ = s.rdb.ZRem(ctx, imageTaskIndexKey(owner.APIKeyID), staleIDs...).Err()
	}
	return records, nil
}

func (s *imageTaskStore) Delete(ctx context.Context, task *service.ImageTaskRecord) error {
	_, err := s.rdb.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
		pipe.Del(ctx, imageTaskKey(task.ID))
		pipe.ZRem(ctx, imageTaskIndexKey(task.APIKeyID), task.ID)
		return nil
	})
	return err
}

func imageTaskKey(id string) string {
	return imageTaskKeyPrefix + strings.TrimSpace(id)
}

func imageTaskIndexKey(apiKeyID int64) string {
	return imageTaskIndexKeyPrefix + strconv.FormatInt(apiKeyID, 10)
}
