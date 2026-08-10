package securityaudit

import (
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// LegacyModerationAdapter 必须透传决策自带的错误码。审计链路故障（fail-closed）带
// content_moderation_unavailable，不能被压成 content_policy_violation —— 否则客户端
// 会把一次系统不可用当成自己触犯了内容策略。
func TestLegacyModerationAdapter_PropagatesDecisionErrorCode(t *testing.T) {
	t.Run("内容策略拦截回退到 content_policy_violation", func(t *testing.T) {
		decision := &service.ContentModerationDecision{
			Blocked: true, Flagged: true, StatusCode: http.StatusForbidden,
			Message: "blocked", Action: service.ContentModerationActionBlock,
		}
		got := legacyDecisionFromModeration(decision)
		require.Equal(t, "content_policy_violation", got.ErrorCode)
	})

	t.Run("审计链路故障保留独立错误码", func(t *testing.T) {
		decision := &service.ContentModerationDecision{
			Blocked: true, Flagged: false, StatusCode: http.StatusServiceUnavailable,
			Message:   service.ContentModerationUnavailableMessage,
			Action:    service.ContentModerationActionError,
			ErrorCode: service.ContentModerationErrorCodeUnavailable,
		}
		got := legacyDecisionFromModeration(decision)
		require.Equal(t, service.ContentModerationErrorCodeUnavailable, got.ErrorCode)
		require.False(t, got.Flagged, "系统故障不是内容违规")
	})
}

// prioritize 必须把 legacy 的 503 与错误码原样带到网关响应上，而不是压成 403。
func TestPrioritize_KeepsModerationUnavailableStatusAndCode(t *testing.T) {
	legacy := &LegacyDecision{
		Blocked: true, StatusCode: http.StatusServiceUnavailable,
		Message:   service.ContentModerationUnavailableMessage,
		ErrorCode: service.ContentModerationErrorCodeUnavailable,
		Action:    service.ContentModerationActionError,
	}

	decision := prioritize(legacy, nil)

	require.Equal(t, DecisionBlock, decision.Kind)
	require.Equal(t, http.StatusServiceUnavailable, decision.HTTPStatus)
	require.Equal(t, service.ContentModerationErrorCodeUnavailable, decision.ErrorCode)
	require.False(t, decision.AllowNextStage)
}
