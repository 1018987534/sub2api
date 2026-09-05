package handler

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
)

const statusClientClosedRequest = 499

const transientRetryAfterSeconds = "5"

func markTransientRetryableResponse(c *gin.Context) {
	if c != nil {
		c.Header("Retry-After", transientRetryAfterSeconds)
	}
}

const (
	gatewayQueueFullCode        = "gateway_queue_full"
	gatewayConcurrencyLimitCode = "gateway_concurrency_limit"
)

func concurrencyErrorResponse(err error, slotType string) (int, string, string, string) {
	var waitQueueFullErr *WaitQueueFullError
	if errors.As(err, &waitQueueFullErr) {
		return http.StatusServiceUnavailable, "server_error", "",
			"Service temporarily unavailable, please retry later"
	}

	var concurrencyErr *ConcurrencyError
	if errors.As(err, &concurrencyErr) {
		if concurrencyErr.SlotType != "" {
			slotType = concurrencyErr.SlotType
		}
		return http.StatusServiceUnavailable, "server_error", "",
			fmt.Sprintf("Service temporarily unavailable: %s concurrency is busy, please retry later", slotType)
	}

	if errors.Is(err, context.Canceled) {
		return statusClientClosedRequest, "api_error", "", "context canceled"
	}

	return http.StatusServiceUnavailable, "api_error", "", "Service temporarily unavailable, please retry later"
}
