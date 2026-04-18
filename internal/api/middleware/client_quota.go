package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/quota"
)

// ClientQuotaMiddleware blocks requests for exhausted client API keys.
func ClientQuotaMiddleware(manager *quota.Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		if manager == nil {
			c.Next()
			return
		}
		apiKey, _ := c.Get("apiKey")
		apiKeyStr, _ := apiKey.(string)
		allowed, status := manager.Allow(apiKeyStr)
		c.Set("clientAPIKeyQuotaStatus", status)
		if allowed {
			c.Next()
			return
		}
		c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
			"error":                   "client_api_key_quota_exceeded",
			"output_token_quota":      status.OutputTokenQuota,
			"used_output_tokens":      status.UsedOutputTokens,
			"remaining_output_tokens": status.Remaining,
		})
	}
}
