package handler

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/config"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// All credentials and proofs are synthetic fixtures; no provider call is made.
type lotteryCaptchaSettings struct {
	service.SettingRepository
	values map[string]string
	err    error
}

func (s *lotteryCaptchaSettings) GetMultiple(context.Context, []string) (map[string]string, error) {
	return s.values, s.err
}

type lotteryLoginVerifier struct {
	valid    bool
	calls    int
	token    string
	randstr  string
	remoteIP string
}

func (v *lotteryLoginVerifier) VerifyToken(_ context.Context, _, token, remoteIP string) (*service.TurnstileVerifyResponse, error) {
	v.calls++
	v.token, v.remoteIP = token, remoteIP
	return &service.TurnstileVerifyResponse{Success: v.valid}, nil
}

func (v *lotteryLoginVerifier) VerifyTicket(_ context.Context, _ service.TencentCaptchaCredentials, proof service.TencentCaptchaProof, remoteIP string) (*service.TencentCaptchaVerifyResponse, error) {
	v.calls++
	v.token, v.randstr, v.remoteIP = proof.Ticket, proof.Randstr, remoteIP
	code := int64(0)
	if v.valid {
		code = 1
	}
	return &service.TencentCaptchaVerifyResponse{CaptchaCode: code}, nil
}

func (v *lotteryLoginVerifier) VerifyCaptcha(_ context.Context, _ service.AliyunCaptchaCredentials, token string) (*service.AliyunCaptchaVerifyResult, error) {
	v.calls++
	v.token = token
	return &service.AliyunCaptchaVerifyResult{VerifyResult: v.valid}, nil
}

func lotteryProviderSettings(provider string) map[string]string {
	switch provider {
	case "turnstile":
		return map[string]string{service.SettingKeyTurnstileEnabled: "true", service.SettingKeyTurnstileSecretKey: "fixture-secret"}
	case "tencent":
		return map[string]string{
			service.SettingKeyTencentCaptchaEnabled: "true", service.SettingKeyTencentCaptchaAppID: "123456789",
			service.SettingKeyTencentCaptchaAppSecretKey: "fixture-secret", service.SettingKeyTencentCaptchaCloudSecretID: "fixture-id", service.SettingKeyTencentCaptchaCloudSecretKey: "fixture-key",
		}
	case "aliyun":
		return map[string]string{
			service.SettingKeyAliyunCaptchaEnabled: "true", service.SettingKeyAliyunCaptchaAccessKeyID: "fixture-id",
			service.SettingKeyAliyunCaptchaAccessKeySecret: "fixture-key", service.SettingKeyAliyunCaptchaSceneID: "fixture-scene",
		}
	default:
		return map[string]string{}
	}
}

func newLotteryCaptchaHandler(t *testing.T, settings *lotteryCaptchaSettings, required bool, verifier *lotteryLoginVerifier) (*LotteryHandler, sqlmock.Sqlmock) {
	t.Helper()
	cfg := &config.Config{Server: config.ServerConfig{Mode: "release"}, Turnstile: config.TurnstileConfig{Required: required}}
	settingService := service.NewSettingService(settings, cfg)
	auth := service.NewAuthService(nil, nil, nil, nil, cfg, settingService, nil, service.NewTurnstileService(settingService, verifier), nil, nil, nil, nil, nil)
	auth.SetTencentCaptchaService(service.NewTencentCaptchaService(settingService, verifier))
	auth.SetAliyunCaptchaService(service.NewAliyunCaptchaService(settingService, verifier))
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return NewLotteryHandler(service.NewLotteryService(db, nil, nil), auth), mock
}

func newLotteryJoinContext(body string, authenticated bool) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/lottery/join", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.RemoteAddr = "203.0.113.10:12345"
	if authenticated {
		c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 42})
	}
	return c, recorder
}

func expectLotteryJoin(mock sqlmock.Sqlmock) {
	now := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)
	mock.ExpectBegin()
	mock.ExpectQuery("(?s)FROM lottery_config WHERE id = 1").WillReturnRows(
		sqlmock.NewRows([]string{"enabled", "threshold", "count", "amount", "draw", "next", "recharge", "min_amount", "age", "days", "updated"}).
			AddRow(true, 50, 2, 5, "manual", "manual", false, 0, 0, 0, now))
	mock.ExpectQuery("(?s)FROM lottery_rounds r WHERE r.status='open' FOR UPDATE OF r").WillReturnRows(
		sqlmock.NewRows([]string{"id", "round", "status", "threshold", "prizes", "amount", "draw", "next", "recharge", "min", "age", "days", "count", "manual", "real", "winners", "ips", "started", "drawn", "updated"}).
			AddRow(3, 3, "open", 50, 2, 5, "manual", "manual", false, 0, 0, 0, 4, 0, 4, 0, 4, now, nil, now))
	mock.ExpectQuery("SELECT joined_at FROM lottery_participants").WithArgs(int64(3), int64(42)).WillReturnRows(sqlmock.NewRows([]string{"joined_at"}))
	mock.ExpectQuery("(?s)FROM users u LEFT JOIN redeem_codes").WithArgs(int64(42)).WillReturnRows(sqlmock.NewRows([]string{"created_at", "total", "last"}).AddRow(now.Add(-24*time.Hour), 0, nil))
	mock.ExpectQuery("INSERT INTO lottery_participants").WithArgs(int64(3), int64(42), "203.0.113.10").WillReturnRows(sqlmock.NewRows([]string{"joined_at"}).AddRow(now))
	mock.ExpectQuery("UPDATE lottery_rounds SET participant_count").WithArgs(int64(3)).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(5))
	mock.ExpectCommit()
}

func TestLotteryJoinAcceptsLoginCaptchaProviders(t *testing.T) {
	for _, provider := range []string{"turnstile", "tencent", "aliyun", "disabled"} {
		t.Run(provider, func(t *testing.T) {
			verifier := &lotteryLoginVerifier{valid: true}
			handler, mock := newLotteryCaptchaHandler(t, &lotteryCaptchaSettings{values: lotteryProviderSettings(provider)}, provider != "disabled", verifier)
			expectLotteryJoin(mock)
			c, recorder := newLotteryJoinContext(`{"turnstile_token":"fixture-token","tencent_captcha_ticket":"fixture-ticket","tencent_captcha_randstr":"fixture-randstr"}`, true)
			handler.Join(c)
			require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
			require.Contains(t, recorder.Body.String(), `"participant_count":5`)
			if provider == "disabled" {
				require.Zero(t, verifier.calls)
			} else {
				require.Equal(t, 1, verifier.calls)
			}
			if provider == "tencent" {
				require.Equal(t, "fixture-ticket", verifier.token)
				require.Equal(t, "fixture-randstr", verifier.randstr)
			}
			if provider == "turnstile" || provider == "aliyun" {
				require.Equal(t, "fixture-token", verifier.token)
			}
			if provider == "turnstile" || provider == "tencent" {
				require.Equal(t, "203.0.113.10", verifier.remoteIP)
			}
			require.NoError(t, mock.ExpectationsWereMet())
			fmt.Printf("LOTTERY_JOIN provider=%s status=%d participants=5 verifier_calls=%d\n", provider, recorder.Code, verifier.calls)
		})
	}
}

func TestLotteryJoinRejectsMissingOrInvalidLoginProofBeforeParticipation(t *testing.T) {
	for _, provider := range []string{"turnstile", "tencent", "aliyun"} {
		for _, body := range []string{`{}`, `{"turnstile_token":"bad","tencent_captcha_ticket":"bad","tencent_captcha_randstr":"bad"}`, `{"captcha_id":"old-slider","captcha_x":150,"captcha_y":80}`} {
			t.Run(provider+body, func(t *testing.T) {
				handler, mock := newLotteryCaptchaHandler(t, &lotteryCaptchaSettings{values: lotteryProviderSettings(provider)}, true, &lotteryLoginVerifier{})
				c, recorder := newLotteryJoinContext(body, true)
				handler.Join(c)
				require.Equal(t, http.StatusBadRequest, recorder.Code, recorder.Body.String())
				require.NoError(t, mock.ExpectationsWereMet())
			})
		}
	}
}

func TestLotteryJoinFailsClosedOnUnavailableCaptchaConfiguration(t *testing.T) {
	for _, scenario := range []string{"required-unconfigured", "settings-unavailable", "provider-conflict"} {
		t.Run(scenario, func(t *testing.T) {
			settings := &lotteryCaptchaSettings{values: map[string]string{}}
			switch scenario {
			case "settings-unavailable":
				settings.err = errors.New("fixture unavailable")
			case "provider-conflict":
				settings.values = lotteryProviderSettings("tencent")
				settings.values[service.SettingKeyTurnstileEnabled] = "true"
			}
			handler, mock := newLotteryCaptchaHandler(t, settings, true, &lotteryLoginVerifier{valid: true})
			c, recorder := newLotteryJoinContext(`{}`, true)
			handler.Join(c)
			require.Equal(t, http.StatusServiceUnavailable, recorder.Code, recorder.Body.String())
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestLotteryJoinRejectsUnauthenticatedAndMalformedRequests(t *testing.T) {
	for _, authenticated := range []bool{false, true} {
		handler := NewLotteryHandler(nil, nil)
		c, recorder := newLotteryJoinContext(`{`, authenticated)
		handler.Join(c)
		expected := http.StatusUnauthorized
		if authenticated {
			expected = http.StatusBadRequest
		}
		require.Equal(t, expected, recorder.Code)
	}
}
