package management

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	gin "github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/concurrency"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
)

func TestGetConcurrencyStatusReturnsConfiguredAndRuntimeState(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{
		SDKConfig: config.SDKConfig{ClientAPIKeyPolicies: []config.ClientAPIKeyPolicy{{APIKey: "k1", Alias: "Notebook", Concurrency: 2}}},
		Concurrency: config.ConcurrencyConfig{
			Enabled:   true,
			Defaults:  config.ConcurrencyDefaults{GlobalInFlight: 10, ClientAPIKey: 4, UpstreamAccount: 1},
			Endpoints: []config.EndpointConcurrencyLimit{{Path: "/v1/chat/completions", InFlight: 5}},
		},
	}
	manager := concurrency.NewManager()
	manager.UpdateConfig(cfg)
	releaseHTTP, exceeded := manager.AcquireHTTP("k1", "/v1/chat/completions")
	if exceeded != nil {
		t.Fatalf("AcquireHTTP() error = %v", exceeded)
	}
	defer releaseHTTP()
	authManager := coreauth.NewManager(nil, nil, nil)
	authEntry := &coreauth.Auth{ID: "claude:apikey:test", Provider: "claude", Label: "claude-apikey"}
	if _, err := authManager.Register(context.Background(), authEntry); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	releaseUpstream, upstreamExceeded := manager.AcquireUpstream(authEntry.ID, 1)
	if upstreamExceeded != nil {
		t.Fatalf("AcquireUpstream() error = %v", upstreamExceeded)
	}
	defer releaseUpstream()

	h := NewHandlerWithoutConfigFilePath(cfg, authManager)
	h.SetConcurrencyManager(manager)
	req := httptest.NewRequest(http.MethodGet, "/v0/management/concurrency-status", nil)
	rr := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rr)
	c.Request = req
	h.GetConcurrencyStatus(c)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	var payload struct {
		Enabled bool `json:"enabled"`
		Current struct {
			GlobalInFlight int `json:"global-inflight"`
		} `json:"current"`
		Endpoints []struct {
			Path     string `json:"path"`
			Limit    int    `json:"limit"`
			InFlight int    `json:"inflight"`
		} `json:"endpoints"`
		ClientAPIKeys []struct {
			APIKey   string `json:"api-key"`
			Alias    string `json:"alias"`
			Limit    int    `json:"limit"`
			InFlight int    `json:"inflight"`
		} `json:"client-api-keys"`
		UpstreamAccounts []struct {
			AuthID   string `json:"auth-id"`
			Provider string `json:"provider"`
			Limit    int    `json:"limit"`
			InFlight int    `json:"inflight"`
		} `json:"upstream-accounts"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v body=%s", err, rr.Body.String())
	}
	if !payload.Enabled || payload.Current.GlobalInFlight != 1 {
		t.Fatalf("unexpected payload current state: %#v", payload)
	}
	if len(payload.Endpoints) != 1 || payload.Endpoints[0].InFlight != 1 {
		t.Fatalf("unexpected endpoints payload: %#v", payload.Endpoints)
	}
	if len(payload.ClientAPIKeys) != 1 || payload.ClientAPIKeys[0].Alias != "Notebook" || payload.ClientAPIKeys[0].InFlight != 1 {
		t.Fatalf("unexpected client api keys payload: %#v", payload.ClientAPIKeys)
	}
	if len(payload.UpstreamAccounts) != 1 || payload.UpstreamAccounts[0].AuthID != authEntry.ID || payload.UpstreamAccounts[0].InFlight != 1 {
		t.Fatalf("unexpected upstream accounts payload: %#v", payload.UpstreamAccounts)
	}
}
