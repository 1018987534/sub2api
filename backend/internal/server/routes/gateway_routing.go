package routes

import (
	"context"
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

const gatewayRoutingRuntimeTokenHeader = "X-Gateway-Routing-Token"

type gatewayRoutingRuntimeProvider interface {
	GetGatewayRoutingRuntime(context.Context) (*service.GatewayRoutingRuntime, error)
}

// RegisterGatewayRoutingRuntimeRoutes exposes only the current read-only edge
// routing result. Administrator writes remain under /admin/settings.
func RegisterGatewayRoutingRuntimeRoutes(v1 *gin.RouterGroup, settingService gatewayRoutingRuntimeProvider, cfg *config.Config) {
	v1.GET("/gateway-routing/runtime", func(c *gin.Context) {
		expectedToken := ""
		if cfg != nil {
			expectedToken = strings.TrimSpace(cfg.GatewayRoutingRuntimeToken)
		}
		providedToken := c.GetHeader(gatewayRoutingRuntimeTokenHeader)
		if expectedToken == "" {
			c.AbortWithStatus(http.StatusNotFound)
			return
		}
		if len(providedToken) != len(expectedToken) || subtle.ConstantTimeCompare([]byte(providedToken), []byte(expectedToken)) != 1 {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		runtime, err := settingService.GetGatewayRoutingRuntime(c.Request.Context())
		if err != nil {
			response.ErrorFrom(c, err)
			return
		}
		c.Header("Cache-Control", "no-store")
		response.Success(c, runtime)
	})
}
