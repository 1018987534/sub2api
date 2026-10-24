package repository

import (
	"errors"
	"fmt"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
	"testing"
)

func TestQQIdentityConflictTranslation(t *testing.T) {
	original := fmt.Errorf("profile update: %w", &pq.Error{Code: "23505", Constraint: "qq_activity_identity_owner_unique"})
	translated := translateUserPersistenceError(original, service.ErrUserNotFound, service.ErrEmailExists)
	var app *infraerrors.ApplicationError
	if !errors.As(translated, &app) || app.Code != 409 || app.Reason != "QQ_IDENTITY_EXISTS" {
		t.Fatalf("QQ ownership conflict not exposed as a specific 409: %v", translated)
	}
	if app.Message != "该 QQ 邮箱已被其他账号使用，请使用本人尚未关联的 QQ 邮箱。" {
		t.Fatal("unexpected conflict copy")
	}
	ordinary := &pq.Error{Code: "23505", Constraint: "users_email_key"}
	if !errors.Is(translateUserPersistenceError(ordinary, service.ErrUserNotFound, service.ErrEmailExists), service.ErrEmailExists) {
		t.Fatal("ordinary email conflicts changed")
	}
}
