package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
)

func (h *GatewayHandler) rejectUnpricedModel(c *gin.Context, streamStarted bool) {
	service.SetOpsLocalErrorType(c, "api_error", service.ModelPricingUnavailableCode)
	if !streamStarted && (inboundIsResponses(c) || GetInboundEndpoint(c) == EndpointChatCompletions) {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{
			"type": "api_error", "code": service.ModelPricingUnavailableCode, "message": service.ModelPricingUnavailableMessage,
		}})
		return
	}
	h.handleStreamingAwareErrorWithCode(c, http.StatusServiceUnavailable, "api_error", service.ModelPricingUnavailableCode, service.ModelPricingUnavailableMessage, streamStarted)
}

func (h *OpenAIGatewayHandler) rejectUnpricedModel(c *gin.Context, streamStarted bool) {
	service.SetOpsLocalErrorType(c, "api_error", service.ModelPricingUnavailableCode)
	h.handleStreamingAwareErrorWithCode(c, http.StatusServiceUnavailable, "api_error", service.ModelPricingUnavailableCode, service.ModelPricingUnavailableMessage, streamStarted, false)
}

func rejectUnpricedWebSocketModel(ctx context.Context, c *gin.Context, conn *coderws.Conn) {
	service.SetOpsLocalErrorType(c, "api_error", service.ModelPricingUnavailableCode)
	payload, _ := json.Marshal(gin.H{"type": "error", "error": gin.H{
		"type": "api_error", "code": service.ModelPricingUnavailableCode, "message": service.ModelPricingUnavailableMessage,
	}})
	writeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	_ = conn.Write(writeCtx, coderws.MessageText, payload)
	cancel()
	closeOpenAIClientWS(conn, coderws.StatusTryAgainLater, service.ModelPricingUnavailableCode)
}
