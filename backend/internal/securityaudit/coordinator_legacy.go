package securityaudit

import (
	"context"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type LegacyModerationAdapter struct {
	service *service.ContentModerationService
}

func NewLegacyModerationAdapter(svc *service.ContentModerationService) LegacyEngine {
	return &LegacyModerationAdapter{service: svc}
}

func (a *LegacyModerationAdapter) Check(ctx context.Context, req Request) (*LegacyDecision, error) {
	if a == nil || a.service == nil {
		return nil, nil
	}
	decision, err := a.service.Check(ctx, service.ContentModerationCheckInput{
		RequestID: req.RequestID, UserID: req.UserID, UserEmail: req.UserEmail,
		APIKeyID: req.APIKeyID, APIKeyName: req.APIKeyName, GroupID: cloneInt64Ptr(req.GroupID),
		GroupName: req.GroupName, Endpoint: req.Endpoint, Provider: req.Provider,
		Model: req.Model, Protocol: req.Protocol, Body: req.Body,
	})
	if err != nil || decision == nil {
		return nil, err
	}
	return legacyDecisionFromModeration(decision), nil
}

// legacyDecisionFromModeration 把内容审计决策映射成协调器决策。
//
// 审计链路故障（fail-closed）自带 content_moderation_unavailable；内容策略拦截不带
// 错误码，回退到 content_policy_violation。
func legacyDecisionFromModeration(decision *service.ContentModerationDecision) *LegacyDecision {
	if decision == nil {
		return nil
	}
	errorCode := strings.TrimSpace(decision.ErrorCode)
	if errorCode == "" {
		errorCode = "content_policy_violation"
	}
	return &LegacyDecision{
		Allowed: decision.Allowed, Blocked: decision.Blocked, Flagged: decision.Flagged,
		Message: decision.Message, StatusCode: decision.StatusCode,
		ErrorCode: errorCode, Action: decision.Action,
	}
}
