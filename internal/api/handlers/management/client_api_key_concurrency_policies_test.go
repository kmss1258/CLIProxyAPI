package management

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	gin "github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/concurrency"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
)

func TestPatchClientAPIKeyConcurrencyPoliciesCreatesOverride(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	cfg := &config.Config{SDKConfig: config.SDKConfig{APIKeys: []string{"known-key"}}}
	if data, err := json.Marshal(cfg); err != nil {
		t.Fatalf("Marshal() error = %v", err)
	} else if err := os.WriteFile(configPath, data, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	h := NewHandler(cfg, configPath, nil)
	h.SetConcurrencyManager(concurrency.NewManager())
	body := []byte(`{"value":{"api-key":"known-key","concurrency":5}}`)
	req := httptest.NewRequest(http.MethodPatch, "/v0/management/client-api-key-concurrency-policies", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rr)
	c.Request = req
	h.PatchClientAPIKeyConcurrencyPolicies(c)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	if len(h.cfg.ClientAPIKeyPolicies) != 1 || h.cfg.ClientAPIKeyPolicies[0].Concurrency != 5 {
		t.Fatalf("unexpected policies: %+v", h.cfg.ClientAPIKeyPolicies)
	}
}

func TestDeleteClientAPIKeyConcurrencyPoliciesClearsOnlyConcurrency(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	cfg := &config.Config{SDKConfig: config.SDKConfig{APIKeys: []string{"known-key"}, ClientAPIKeyPolicies: []config.ClientAPIKeyPolicy{{APIKey: "known-key", Alias: "Notebook", Concurrency: 5}}}}
	if data, err := json.Marshal(cfg); err != nil {
		t.Fatalf("Marshal() error = %v", err)
	} else if err := os.WriteFile(configPath, data, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	h := NewHandler(cfg, configPath, nil)
	h.SetConcurrencyManager(concurrency.NewManager())
	req := httptest.NewRequest(http.MethodDelete, "/v0/management/client-api-key-concurrency-policies?api-key=known-key", nil)
	rr := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rr)
	c.Request = req
	h.DeleteClientAPIKeyConcurrencyPolicies(c)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	if len(h.cfg.ClientAPIKeyPolicies) != 1 || h.cfg.ClientAPIKeyPolicies[0].Alias != "Notebook" || h.cfg.ClientAPIKeyPolicies[0].Concurrency != 0 {
		t.Fatalf("unexpected policies after delete: %+v", h.cfg.ClientAPIKeyPolicies)
	}
}
