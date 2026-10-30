package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestOpenAIGatewayServiceForward_GroupPolicyRemovesPassiveImageTools(t *testing.T) {
	gin.SetMode(gin.TestMode)
	payloads := []struct {
		name string
		lite bool
		body string
	}{
		{
			name: "namespace",
			body: `{"model":"gpt-5.5","stream":false,"tools":[{"type":"namespace","name":"image_gen","tools":[{"type":"function","name":"imagegen"}]},{"type":"function","name":"shell","parameters":{"type":"object"}}],"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"write code"},{"type":"input_image","image_url":"https://example.com/reference.png"}]}],"tool_choice":"auto"}`,
		},
		{
			name: "responses lite",
			lite: true,
			body: `{"model":"gpt-5.5","stream":false,"tools":[{"type":"function","name":"shell","parameters":{"type":"object"}}],"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"write code"},{"type":"input_image","image_url":"https://example.com/reference.png"}]},{"type":"additional_tools","tools":[{"type":"namespace","name":"image_gen","tools":[{"type":"function","name":"imagegen"}]}]}],"tool_choice":"auto"}`,
		},
	}
	for _, passthrough := range []bool{false, true} {
		for _, allowImages := range []bool{false, true} {
			for _, userAgent := range []string{"Codex Desktop/0.163.0-alpha.10.2", "unit-test-sdk/1.0"} {
				for _, payload := range payloads {
					t.Run(fmt.Sprintf("passthrough=%t/allowed=%t/%s/%s", passthrough, allowImages, userAgent, payload.name), func(t *testing.T) {
						upstream := &httpUpstreamRecorder{resp: &http.Response{
							StatusCode: http.StatusOK,
							Header:     http.Header{"Content-Type": []string{"application/json"}},
							Body:       io.NopCloser(strings.NewReader(`{"id":"resp_text","model":"gpt-5.5","usage":{"input_tokens":3,"output_tokens":2}}`)),
						}}
						svc := newOpenAIImageGenerationControlTestService(upstream)
						svc.cfg.Gateway.CodexImageGenerationBridgeEnabled = true
						c, recorder := newOpenAIImageGenerationControlTestContext(allowImages, userAgent)
						SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)
						// Reproduce the old handler's false hint; the permission boundary
						// must still sanitize the payload independently of the hint.
						if !allowImages {
							SetOpenAIImageIntentHint(c, false)
						}
						if payload.lite {
							c.Request.Header.Set(responsesLiteHeader, "true")
						}
						account := newOpenAIImageGenerationControlTestAccount()
						account.Extra = map[string]any{"openai_passthrough": passthrough}

						result, err := svc.Forward(context.Background(), c, account, []byte(payload.body))

						require.NoError(t, err)
						require.NotNil(t, result)
						require.Equal(t, http.StatusOK, recorder.Code)
						require.NotNil(t, upstream.lastReq)
						require.Equal(t, allowImages, openAIRequestBodyHasImageGenerationDeclaration(upstream.lastBody))
						require.True(t, gjson.GetBytes(upstream.lastBody, `tools.#(name=="shell")`).Exists())
						require.Equal(t, "write code", gjson.GetBytes(upstream.lastBody, "input.0.content.0.text").String())
						require.Equal(t, "https://example.com/reference.png", gjson.GetBytes(upstream.lastBody, "input.0.content.1.image_url").String())
						require.Equal(t, "auto", gjson.GetBytes(upstream.lastBody, "tool_choice").String())
						require.Zero(t, result.ImageCount)
					})
				}
			}
		}
	}
}

func TestOpenAIGatewayServiceForward_DisabledImagesRejectExplicitIntentWithFalseHint(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name string
		body string
	}{
		{name: "hosted image tool", body: `{"model":"gpt-5.5","input":"draw","tools":[{"type":"image_generation"}]}`},
		{name: "image model", body: `{"model":"gpt-image-2","input":"draw"}`},
		{name: "mapped image model", body: `{"model":"draw-alias","input":"draw"}`},
		{name: "explicit namespace choice", body: `{"model":"gpt-5.5","input":"draw","tools":[{"type":"namespace","name":"image_gen","tools":[{"type":"function","name":"imagegen"}]}],"tool_choice":{"type":"function","name":"image_gen.imagegen"}}`},
	}
	for _, passthrough := range []bool{false, true} {
		for _, tt := range tests {
			t.Run(fmt.Sprintf("passthrough=%t/%s", passthrough, tt.name), func(t *testing.T) {
				upstream := &httpUpstreamRecorder{}
				svc := newOpenAIImageGenerationControlTestService(upstream)
				c, recorder := newOpenAIImageGenerationControlTestContext(false, "Codex Desktop/0.163.0-alpha.10.2")
				SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)
				SetOpenAIImageIntentHint(c, false)
				account := newOpenAIImageGenerationControlTestAccount()
				account.Credentials["model_mapping"] = map[string]any{"draw-alias": "gpt-image-2"}
				account.Extra = map[string]any{
					"openai_passthrough":                             passthrough,
					featureKeyCodexImageGenerationExplicitToolPolicy: codexImageGenerationExplicitToolPolicyStrip,
				}

				result, err := svc.Forward(context.Background(), c, account, []byte(tt.body))

				require.Error(t, err)
				require.Nil(t, result)
				require.Equal(t, http.StatusForbidden, recorder.Code)
				require.Equal(t, "permission_error", gjson.GetBytes(recorder.Body.Bytes(), "error.type").String())
				require.Equal(t, ImageGenerationPermissionMessage(), gjson.GetBytes(recorder.Body.Bytes(), "error.message").String())
				require.Nil(t, upstream.lastReq, "disabled explicit image requests must never reach upstream")
			})
		}
	}
}
