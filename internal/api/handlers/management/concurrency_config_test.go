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

func TestPatchConcurrencyConfigUpdatesDefaultsAndEndpoints(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	cfg := &config.Config{}
	if data, err := json.Marshal(cfg); err != nil {
		t.Fatalf("Marshal() error = %v", err)
	} else if err := os.WriteFile(configPath, data, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	h := NewHandler(cfg, configPath, nil)
	manager := concurrency.NewManager()
	h.SetConcurrencyManager(manager)
	body := []byte(`{"enabled":true,"defaults":{"global-inflight":9,"client-api-key":3},"endpoints":[{"path":"/v1/responses","inflight":2}]}`)
	req := httptest.NewRequest(http.MethodPatch, "/v0/management/concurrency-config", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rr)
	c.Request = req
	h.PatchConcurrencyConfig(c)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	if !h.cfg.Concurrency.Enabled || h.cfg.Concurrency.Defaults.GlobalInFlight != 9 || h.cfg.Concurrency.Defaults.ClientAPIKey != 3 {
		t.Fatalf("unexpected concurrency defaults: %+v", h.cfg.Concurrency)
	}
	if len(h.cfg.Concurrency.Endpoints) != 1 || h.cfg.Concurrency.Endpoints[0].Path != "/v1/responses" {
		t.Fatalf("unexpected concurrency endpoints: %+v", h.cfg.Concurrency.Endpoints)
	}
}

func TestPatchConcurrencyConfigRejectsInvalidEndpoint(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewHandlerWithoutConfigFilePath(&config.Config{}, nil)
	body := []byte(`{"endpoints":[{"path":"   ","inflight":1}]}`)
	req := httptest.NewRequest(http.MethodPatch, "/v0/management/concurrency-config", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rr)
	c.Request = req
	h.PatchConcurrencyConfig(c)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rr.Code, rr.Body.String())
	}
}
