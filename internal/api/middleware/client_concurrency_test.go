package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/concurrency"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
)

func TestClientConcurrencyMiddlewareRejectsClientLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mgr := concurrency.NewManager()
	mgr.UpdateConfig(&config.Config{
		SDKConfig:   config.SDKConfig{ClientAPIKeyPolicies: []config.ClientAPIKeyPolicy{{APIKey: "known-key", Concurrency: 1}}},
		Concurrency: config.ConcurrencyConfig{Enabled: true},
	})
	firstRelease, exceeded := mgr.AcquireHTTP("known-key", "/v1/chat/completions")
	if exceeded != nil {
		t.Fatalf("expected pre-acquire to succeed, got %v", exceeded)
	}
	defer firstRelease()

	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("apiKey", "known-key")
		c.Next()
	})
	router.Use(ClientConcurrencyMiddleware(mgr))
	router.POST("/v1/chat/completions", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)
	if resp.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", resp.Code)
	}
}

func TestClientConcurrencyMiddlewareReleasesAfterRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mgr := concurrency.NewManager()
	mgr.UpdateConfig(&config.Config{
		Concurrency: config.ConcurrencyConfig{Enabled: true, Endpoints: []config.EndpointConcurrencyLimit{{Path: "/v1/responses", InFlight: 1}}},
	})

	router := gin.New()
	router.Use(ClientConcurrencyMiddleware(mgr))
	router.POST("/v1/responses", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
		resp := httptest.NewRecorder()
		router.ServeHTTP(resp, req)
		if resp.Code != http.StatusOK {
			t.Fatalf("request %d expected 200, got %d", i+1, resp.Code)
		}
	}
}
