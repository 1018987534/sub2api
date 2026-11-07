package handler

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func feedbackTestContext(method, url, body, key string, userID int64, role string) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(method, url, strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set("Idempotency-Key", key)
	if userID != 0 {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: userID})
	}
	c.Set(string(middleware.ContextKeyUserRole), role)
	return c, recorder
}

func feedbackTestRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{"id", "user_id", "category", "subject", "content", "created_at"}).
		AddRow(41, 7, "problem", "请求出错", "使用时提示超时", time.Date(2026, 11, 7, 4, 0, 0, 0, time.UTC))
}

func TestFeedbackCreateValidation(t *testing.T) {
	valid := `{"category":"problem","subject":"请求出错","content":"使用时提示超时"}`
	for _, tc := range []struct {
		name, body, key string
		userID          int64
		status          int
	}{
		{"unauthenticated", valid, "key", 0, 401},
		{"invalid identity", valid, "key", -1, 401},
		{"malformed JSON", `{`, "key", 7, 400},
		{"invalid category", `{"category":"reply","subject":"title","content":"body"}`, "key", 7, 400},
		{"empty subject", `{"category":"problem","subject":"  ","content":"body"}`, "key", 7, 400},
		{"empty content", `{"category":"problem","subject":"title","content":" \n "}`, "key", 7, 400},
		{"long subject", `{"category":"problem","subject":"` + strings.Repeat("字", 101) + `","content":"body"}`, "key", 7, 400},
		{"long content", `{"category":"problem","subject":"title","content":"` + strings.Repeat("字", 5001) + `"}`, "key", 7, 400},
		{"missing key", valid, "", 7, 400},
		{"long key", valid, strings.Repeat("k", 129), 7, 400},
		{"oversized body", `{"category":"problem","subject":"title","content":"` + strings.Repeat("a", 33<<10) + `"}`, "key", 7, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, recorder := feedbackTestContext("POST", "/feedback", tc.body, tc.key, tc.userID, "user")
			NewFeedbackHandler(nil).Create(c)
			require.Equal(t, tc.status, recorder.Code)
		})
	}
}

func TestFeedbackCreateIsIndependentAndIdempotent(t *testing.T) {
	for _, outcome := range []string{"created", "retried", "conflict"} {
		t.Run(outcome, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			insert := mock.ExpectQuery(`INSERT INTO feedback_submissions .*ON CONFLICT`).
				WithArgs(int64(7), "problem", "请求出错", "使用时提示超时", "fixture-key")
			status := http.StatusCreated
			if outcome == "created" {
				insert.WillReturnRows(feedbackTestRows())
			} else {
				insert.WillReturnError(sql.ErrNoRows)
				rows := feedbackTestRows()
				status = http.StatusOK
				if outcome == "conflict" {
					rows = sqlmock.NewRows([]string{"id", "user_id", "category", "subject", "content", "created_at"}).AddRow(41, 7, "problem", "另一个问题", "使用时提示超时", time.Now())
					status = http.StatusConflict
				}
				mock.ExpectQuery(`SELECT .* FROM feedback_submissions WHERE user_id=\$1 AND idempotency_key=\$2`).WithArgs(int64(7), "fixture-key").WillReturnRows(rows)
			}
			c, recorder := feedbackTestContext("POST", "/feedback", `{"category":"problem","subject":"  请求出错  ","content":"  使用时提示超时  "}`, "fixture-key", 7, "user")
			NewFeedbackHandler(db).Create(c)
			require.Equal(t, status, recorder.Code, recorder.Body.String())
			require.NoError(t, mock.ExpectationsWereMet()) // Any support-chat query would fail the request.
		})
	}
}

func TestFeedbackAdminListRequiresAdministrator(t *testing.T) {
	for _, tc := range []struct {
		userID int64
		role   string
		status int
	}{{0, "", 401}, {7, "user", 403}, {7, "", 403}} {
		c, recorder := feedbackTestContext("GET", "/admin/feedback", "", "", tc.userID, tc.role)
		NewFeedbackHandler(nil).AdminList(c)
		require.Equal(t, tc.status, recorder.Code)
	}
}

func TestFeedbackAdminListIsPaginatedAndReadOnly(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM feedback_submissions`).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(21))
	mock.ExpectQuery(`SELECT .* FROM feedback_submissions f LEFT JOIN users u .* ORDER BY f.created_at DESC,f.id DESC LIMIT \$1 OFFSET \$2`).
		WithArgs(20, 20).WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "category", "subject", "content", "created_at", "email", "username"}).
		AddRow(41, 7, "problem", "请求出错", "使用时提示超时", time.Now(), "fixture@example.test", "测试用户"))
	c, recorder := feedbackTestContext("GET", "/admin/feedback?page=2&page_size=20", "", "", 1, "admin")
	NewFeedbackHandler(db).AdminList(c)
	require.Equal(t, 200, recorder.Code, recorder.Body.String())
	require.Contains(t, recorder.Body.String(), `"page":2`)
	require.Contains(t, recorder.Body.String(), `"total":21`)
	require.Contains(t, recorder.Body.String(), `"user_email":"fixture@example.test"`)
	require.NoError(t, mock.ExpectationsWereMet())
}
