package service

import (
	"context"
	"time"
)

// TempUnschedState 临时不可调度状态
type TempUnschedState struct {
	Source                string  `json:"source,omitempty"`
	UntilUnix             int64   `json:"until_unix"`                       // 解除时间（Unix 时间戳）
	TriggeredAtUnix       int64   `json:"triggered_at_unix"`                // 触发时间（Unix 时间戳）
	StatusCode            int     `json:"status_code"`                      // 触发的错误码
	MatchedKeyword        string  `json:"matched_keyword"`                  // 匹配的关键词
	RuleIndex             int     `json:"rule_index"`                       // 触发的规则索引
	ErrorMessage          string  `json:"error_message"`                    // 错误消息
	TriggerCount          int64   `json:"trigger_count,omitempty"`          // 本次触发累计命中次数
	TriggerThreshold      int     `json:"trigger_threshold,omitempty"`      // 触发阈值
	TriggerWindowMinutes  int     `json:"trigger_window_minutes,omitempty"` // 计数窗口（分钟）
	TriggerMode           string  `json:"trigger_mode,omitempty"`
	FailureCount          int64   `json:"failure_count,omitempty"`
	FailureThreshold      int     `json:"failure_threshold,omitempty"`
	WindowSeconds         int     `json:"window_seconds,omitempty"`
	FirstTokenMs          int     `json:"first_token_ms,omitempty"`
	FirstTokenThresholdMs int     `json:"first_token_threshold_ms,omitempty"`
	SampleCount           int64   `json:"sample_count,omitempty"`
	SlowSampleCount       int64   `json:"slow_sample_count,omitempty"`
	ObservedPercent       float64 `json:"observed_percent,omitempty"`
	TriggerPercent        float64 `json:"trigger_percent,omitempty"`
	PauseMinutes          int     `json:"pause_minutes,omitempty"`
}

// TempUnschedCache 临时不可调度缓存接口
type TempUnschedCache interface {
	SetTempUnsched(ctx context.Context, accountID int64, state *TempUnschedState) error
	GetTempUnsched(ctx context.Context, accountID int64) (*TempUnschedState, error)
	DeleteTempUnsched(ctx context.Context, accountID int64) error
}

// TempUnschedFailureCounterCache tracks independent sliding failure windows for
// every account rule. eventID makes repeated policy checks for one request idempotent.
type TempUnschedFailureCounterCache interface {
	RecordFailure(ctx context.Context, accountID int64, ruleKey string, windowSeconds int, eventID string) (int64, error)
	ResetFailures(ctx context.Context, accountID int64) error
}

// TimeoutCounterCache 超时计数器缓存接口
type TimeoutCounterCache interface {
	// IncrementTimeoutCount 增加账户的超时计数，返回当前计数值
	// windowMinutes 是计数窗口时间（分钟），超过此时间计数器会自动重置
	IncrementTimeoutCount(ctx context.Context, accountID int64, windowMinutes int) (int64, error)
	// GetTimeoutCount 获取账户当前的超时计数
	GetTimeoutCount(ctx context.Context, accountID int64) (int64, error)
	// ResetTimeoutCount 重置账户的超时计数
	ResetTimeoutCount(ctx context.Context, accountID int64) error
	// GetTimeoutCountTTL 获取计数器剩余过期时间
	GetTimeoutCountTTL(ctx context.Context, accountID int64) (time.Duration, error)
}

type FirstTokenLatencySampleCounts struct {
	Total int64
	Slow  int64
}

// FirstTokenLatencyCounterCache tracks all measured and slow first-token samples
// independently for each global rule and coordinates one pause writer across nodes.
type FirstTokenLatencyCounterCache interface {
	RecordFirstTokenSample(ctx context.Context, accountID int64, ruleKey string, windowSeconds int, eventID string, slow bool) (FirstTokenLatencySampleCounts, error)
	ClaimFirstTokenPause(ctx context.Context, accountID int64, pauseSeconds int) (bool, error)
	ReleaseFirstTokenPauseClaim(ctx context.Context, accountID int64) error
	ResetFirstTokenSamples(ctx context.Context, accountID int64) error
}
