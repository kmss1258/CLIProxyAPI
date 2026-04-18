package middleware

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/quota"
)

func TestClientQuotaMiddlewareBlocksExhaustedKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	manager := quota.NewManager()
	tmpDir := t.TempDir()
	cfg := &config.Config{}
	cfg.SQLitePromptLog.Path = filepath.Join(tmpDir, "quota.sqlite")
	cfg.ClientAPIKeyPolicies = []config.ClientAPIKeyPolicy{{APIKey: "user-key", OutputTokenQuota: 5}}
	if err := manager.UpdateConfig(cfg, filepath.Join(tmpDir, "config.yaml")); err != nil {
		t.Fatalf("UpdateConfig() error = %v", err)
	}
	if err := manager.ApplyUsage("user-key", 5); err != nil {
		t.Fatalf("ApplyUsage() error = %v", err)
	}
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("apiKey", "user-key")
		c.Next()
	})
	r.Use(ClientQuotaMiddleware(manager))
	r.GET("/v1/models", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d body=%s", rr.Code, rr.Body.String())
	}
}
