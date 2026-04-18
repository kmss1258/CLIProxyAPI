package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/concurrency"
)

// ClientConcurrencyMiddleware blocks requests when configured in-flight concurrency limits are exhausted.
func ClientConcurrencyMiddleware(manager *concurrency.Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		if manager == nil {
			c.Next()
			return
		}
		apiKey, _ := c.Get("apiKey")
		apiKeyStr, _ := apiKey.(string)
		release, exceeded := manager.AcquireHTTP(apiKeyStr, c.FullPath())
		if exceeded != nil {
			status := http.StatusServiceUnavailable
			if exceeded.Code == "client_concurrency_limit_exceeded" {
				status = http.StatusTooManyRequests
			}
			payload := gin.H{
				"error":    exceeded.Code,
				"limit":    exceeded.Limit,
				"inflight": exceeded.InFlight,
			}
			if exceeded.Path != "" {
				payload["path"] = exceeded.Path
			}
			c.AbortWithStatusJSON(status, payload)
			return
		}
		defer release()
		c.Next()
	}
}
