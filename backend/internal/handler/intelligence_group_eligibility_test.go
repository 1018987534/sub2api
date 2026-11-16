package handler

import (
	"encoding/json"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/intelligence"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestIntelligenceRejectsIneligibleGroupSaveAndRun(t *testing.T) {
	for _, group := range []*service.Group{
		{ID: 111, Name: "013不降智", Status: "inactive"},
		{ID: 99, Name: "018不降智(超高缓存)", Status: "inactive"},
		{ID: 114, Name: "DeepSeek", Status: "active"},
	} {
		for _, action := range []string{"save", "run"} {
			t.Run(group.Name+"/"+action, func(t *testing.T) {
				h := &IntelligenceHandler{groups: &intelligencePickerGroups{group: group}}
				input := intelligence.DefaultConfig(group.ID)
				input.Enabled, input.APIKeyID = true, 42
				payload, err := json.Marshal(input)
				require.NoError(t, err)
				w := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(w)
				c.Request = httptest.NewRequest("PUT", "/config", strings.NewReader(string(payload)))
				c.Request.Header.Set("Content-Type", "application/json")
				c.Params = gin.Params{{Key: "groupID", Value: strconv.FormatInt(group.ID, 10)}}
				c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 9})
				if action == "save" {
					h.Save(c)
				} else {
					h.Run(c)
				}
				require.Equal(t, 400, w.Code)
				require.Contains(t, w.Body.String(), "仅支持名称包含不降智且状态为启用的分组")
			})
		}
	}
}
