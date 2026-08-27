package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const (
	gatewayNodeCapacityKeyPrefix       = "gateway:routing:capacity:"
	gatewayNodeCapacityLeaseTTL        = 2 * time.Minute
	gatewayNodeCapacityRefreshInterval = 30 * time.Second
	gatewayNodeCapacityOperationTO     = 2 * time.Second
)

var (
	ErrGatewayNodeCapacityLeaseLost = errors.New("gateway node capacity lease lost")

	gatewayNodeCapacityAcquireScript = redis.NewScript(`
		redis.replicate_commands()
		local key = KEYS[1]
		local limit = tonumber(ARGV[1])
		local ttl = tonumber(ARGV[2])
		local leaseID = ARGV[3]
		local now = tonumber(redis.call('TIME')[1])
		redis.call('ZREMRANGEBYSCORE', key, '-inf', now - ttl)
		if redis.call('ZSCORE', key, leaseID) ~= false then
			redis.call('ZADD', key, now, leaseID)
			redis.call('EXPIRE', key, ttl)
			return {1, redis.call('ZCARD', key)}
		end
		local count = redis.call('ZCARD', key)
		if count >= limit then
			return {0, count}
		end
		redis.call('ZADD', key, now, leaseID)
		redis.call('EXPIRE', key, ttl)
		return {1, count + 1}
	`)

	gatewayNodeCapacityRefreshScript = redis.NewScript(`
		redis.replicate_commands()
		local key = KEYS[1]
		local ttl = tonumber(ARGV[1])
		local leaseID = ARGV[2]
		local now = tonumber(redis.call('TIME')[1])
		redis.call('ZREMRANGEBYSCORE', key, '-inf', now - ttl)
		if redis.call('ZSCORE', key, leaseID) == false then
			return 0
		end
		redis.call('ZADD', key, now, leaseID)
		redis.call('EXPIRE', key, ttl)
		return 1
	`)

	gatewayNodeCapacityCountScript = redis.NewScript(`
		redis.replicate_commands()
		local key = KEYS[1]
		local ttl = tonumber(ARGV[1])
		local now = tonumber(redis.call('TIME')[1])
		redis.call('ZREMRANGEBYSCORE', key, '-inf', now - ttl)
		return redis.call('ZCARD', key)
	`)
)

// GatewayNodeCapacityStore owns distributed node-level concurrency leases.
// Every application instance uses the shared Redis, keyed by instance_id.
type GatewayNodeCapacityStore struct {
	rdb *redis.Client
}

func NewGatewayNodeCapacityStore(rdb *redis.Client) *GatewayNodeCapacityStore {
	return &GatewayNodeCapacityStore{rdb: rdb}
}

func gatewayNodeCapacityKey(nodeID string) string {
	return gatewayNodeCapacityKeyPrefix + strings.ToLower(strings.TrimSpace(nodeID))
}

func (s *GatewayNodeCapacityStore) Acquire(ctx context.Context, nodeID string, limit int) (*GatewayNodeCapacityLease, int, error) {
	if s == nil || s.rdb == nil {
		return nil, 0, errors.New("gateway node capacity store is unavailable")
	}
	if strings.TrimSpace(nodeID) == "" || limit <= 0 {
		return nil, 0, errors.New("gateway node capacity lease requires a node and positive limit")
	}
	leaseID := uuid.NewString()
	result, err := gatewayNodeCapacityAcquireScript.Run(
		ctx,
		s.rdb,
		[]string{gatewayNodeCapacityKey(nodeID)},
		limit,
		int(gatewayNodeCapacityLeaseTTL.Seconds()),
		leaseID,
	).Slice()
	if err != nil {
		return nil, 0, fmt.Errorf("acquire gateway node capacity: %w", err)
	}
	if len(result) != 2 {
		return nil, 0, errors.New("acquire gateway node capacity returned an invalid result")
	}
	acquired, err := redisScriptInt64(result[0])
	if err != nil {
		return nil, 0, fmt.Errorf("parse gateway node capacity result: %w", err)
	}
	current, err := redisScriptInt64(result[1])
	if err != nil {
		return nil, 0, fmt.Errorf("parse gateway node capacity count: %w", err)
	}
	if acquired != 1 {
		return nil, int(current), nil
	}

	leaseCtx, cancel := context.WithCancelCause(ctx)
	lease := &GatewayNodeCapacityLease{
		ctx:         leaseCtx,
		cancel:      cancel,
		store:       s,
		nodeID:      nodeID,
		leaseID:     leaseID,
		stopCh:      make(chan struct{}),
		refreshDone: make(chan struct{}),
	}
	go lease.refreshLoop()
	return lease, int(current), nil
}

func redisScriptInt64(value any) (int64, error) {
	switch typed := value.(type) {
	case int64:
		return typed, nil
	case string:
		return strconv.ParseInt(typed, 10, 64)
	case []byte:
		return strconv.ParseInt(string(typed), 10, 64)
	default:
		return 0, fmt.Errorf("unexpected Redis result type %T", value)
	}
}

func (s *GatewayNodeCapacityStore) Current(ctx context.Context, nodeID string) (int, error) {
	if s == nil || s.rdb == nil {
		return 0, errors.New("gateway node capacity store is unavailable")
	}
	count, err := gatewayNodeCapacityCountScript.Run(
		ctx,
		s.rdb,
		[]string{gatewayNodeCapacityKey(nodeID)},
		int(gatewayNodeCapacityLeaseTTL.Seconds()),
	).Int()
	if err != nil {
		return 0, fmt.Errorf("get gateway node capacity: %w", err)
	}
	return count, nil
}

type GatewayNodeCapacityLease struct {
	ctx     context.Context
	cancel  context.CancelCauseFunc
	store   *GatewayNodeCapacityStore
	nodeID  string
	leaseID string

	stopOnce    sync.Once
	stopCh      chan struct{}
	refreshDone chan struct{}
}

func (l *GatewayNodeCapacityLease) Context() context.Context {
	if l == nil || l.ctx == nil {
		return context.Background()
	}
	return l.ctx
}

func (l *GatewayNodeCapacityLease) refreshLoop() {
	defer close(l.refreshDone)
	ticker := time.NewTicker(gatewayNodeCapacityRefreshInterval)
	defer ticker.Stop()
	lastConfirmed := time.Now()
	for {
		select {
		case <-l.stopCh:
			return
		case <-l.ctx.Done():
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), gatewayNodeCapacityOperationTO)
			refreshed, err := gatewayNodeCapacityRefreshScript.Run(
				ctx,
				l.store.rdb,
				[]string{gatewayNodeCapacityKey(l.nodeID)},
				int(gatewayNodeCapacityLeaseTTL.Seconds()),
				l.leaseID,
			).Int()
			cancel()
			if err == nil && refreshed == 1 {
				lastConfirmed = time.Now()
				continue
			}
			if err == nil || time.Since(lastConfirmed) >= gatewayNodeCapacityLeaseTTL {
				l.cancel(ErrGatewayNodeCapacityLeaseLost)
				return
			}
		}
	}
}

func (l *GatewayNodeCapacityLease) Release() {
	if l == nil {
		return
	}
	l.stopOnce.Do(func() {
		close(l.stopCh)
		<-l.refreshDone
		l.cancel(context.Canceled)
		ctx, cancel := context.WithTimeout(context.Background(), gatewayNodeCapacityOperationTO)
		defer cancel()
		if l.store != nil && l.store.rdb != nil {
			_ = l.store.rdb.ZRem(ctx, gatewayNodeCapacityKey(l.nodeID), l.leaseID).Err()
		}
	})
}
