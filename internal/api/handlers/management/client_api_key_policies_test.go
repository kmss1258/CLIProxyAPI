package management

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/quota"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
)

func TestPutClientAPIKeyPoliciesRejectsUnknownKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewHandlerWithoutConfigFilePath(&config.Config{SDKConfig: config.SDKConfig{APIKeys: []string{"known-key"}}}, nil)
	req := httptest.NewRequest(http.MethodPut, "/v0/management/client-api-key-policies", bytes.NewBufferString(`[{"api-key":"unknown-key","output-token-quota":100}]`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rr)
	c.Request = req
	h.PutClientAPIKeyPolicies(c)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestPutClientAPIKeyPoliciesRejectsInvalidResetMode(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewHandlerWithoutConfigFilePath(&config.Config{SDKConfig: config.SDKConfig{APIKeys: []string{"known-key"}}}, nil)
	req := httptest.NewRequest(http.MethodPut, "/v0/management/client-api-key-policies", bytes.NewBufferString(`[{"api-key":"known-key","output-token-quota":100,"output-token-quota-reset":"monthly"}]`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rr)
	c.Request = req
	h.PutClientAPIKeyPolicies(c)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rr.Code, rr.Body.String())
	}
	if len(h.cfg.ClientAPIKeyPolicies) != 0 {
		data, _ := json.Marshal(h.cfg.ClientAPIKeyPolicies)
		t.Fatalf("expected handler state unchanged on validation failure, got %s", string(data))
	}
}

func TestPutClientAPIKeyPoliciesRejectsResetHoursWithoutQuota(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewHandlerWithoutConfigFilePath(&config.Config{SDKConfig: config.SDKConfig{APIKeys: []string{"known-key"}}}, nil)
	req := httptest.NewRequest(http.MethodPut, "/v0/management/client-api-key-policies", bytes.NewBufferString(`[{"api-key":"known-key","output-token-quota":0,"output-token-quota-reset-hours":24}]`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rr)
	c.Request = req
	h.PutClientAPIKeyPolicies(c)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rr.Code, rr.Body.String())
	}
	if len(h.cfg.ClientAPIKeyPolicies) != 0 {
		data, _ := json.Marshal(h.cfg.ClientAPIKeyPolicies)
		t.Fatalf("expected handler state unchanged on validation failure, got %s", string(data))
	}
}

func TestPutClientAPIKeyPoliciesMapsDeprecatedWeeklyResetToHours(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(configPath, []byte("api-keys:\n  - known-key\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	h := NewHandler(&config.Config{SDKConfig: config.SDKConfig{APIKeys: []string{"known-key"}}}, configPath, nil)
	req := httptest.NewRequest(http.MethodPut, "/v0/management/client-api-key-policies", bytes.NewBufferString(`[{"api-key":"known-key","output-token-quota":100,"output-token-quota-reset":"weekly-from-first-use"}]`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rr)
	c.Request = req
	h.PutClientAPIKeyPolicies(c)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	if len(h.cfg.ClientAPIKeyPolicies) != 1 {
		t.Fatalf("expected one policy, got %d", len(h.cfg.ClientAPIKeyPolicies))
	}
	if got := h.cfg.ClientAPIKeyPolicies[0].OutputTokenQuotaResetHours; got != config.ClientAPIKeyPolicyQuotaResetWeeklyHours {
		t.Fatalf("expected deprecated weekly reset to map to %d hours, got %d", config.ClientAPIKeyPolicyQuotaResetWeeklyHours, got)
	}
	if got := h.cfg.ClientAPIKeyPolicies[0].LegacyOutputTokenQuotaReset; got != "" {
		t.Fatalf("expected deprecated reset field to be cleared, got %q", got)
	}
}

func TestPatchClientAPIKeyPolicyUpdatesResetHours(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(configPath, []byte("api-keys:\n  - known-key\nclient-api-key-policies:\n  - api-key: known-key\n    output-token-quota: 100\n    output-token-quota-reset-hours: 168\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	h := NewHandler(&config.Config{SDKConfig: config.SDKConfig{
		APIKeys: []string{"known-key"},
		ClientAPIKeyPolicies: []config.ClientAPIKeyPolicy{{
			APIKey:                     "known-key",
			OutputTokenQuota:           100,
			OutputTokenQuotaResetHours: 168,
		}},
	}}, configPath, nil)
	req := httptest.NewRequest(http.MethodPatch, "/v0/management/client-api-key-policies", bytes.NewBufferString(`{"match":"known-key","value":{"output-token-quota-reset-hours":24}}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rr)
	c.Request = req
	h.PatchClientAPIKeyPolicy(c)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	if got := h.cfg.ClientAPIKeyPolicies[0].OutputTokenQuotaResetHours; got != 24 {
		t.Fatalf("expected reset hours to update to 24, got %d", got)
	}
}

func TestPatchClientAPIKeyPolicyRejectsDeprecatedWeeklyResetWithoutQuota(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(configPath, []byte("api-keys:\n  - known-key\nclient-api-key-policies:\n  - api-key: known-key\n    output-token-quota: 0\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	h := NewHandler(&config.Config{SDKConfig: config.SDKConfig{
		APIKeys: []string{"known-key"},
		ClientAPIKeyPolicies: []config.ClientAPIKeyPolicy{{
			APIKey:           "known-key",
			OutputTokenQuota: 0,
		}},
	}}, configPath, nil)
	req := httptest.NewRequest(http.MethodPatch, "/v0/management/client-api-key-policies", bytes.NewBufferString(`{"match":"known-key","value":{"output-token-quota-reset":"weekly-from-first-use"}}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rr)
	c.Request = req
	h.PatchClientAPIKeyPolicy(c)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestPatchClientAPIKeyPolicyMapsDeprecatedWeeklyResetOverExistingHours(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(configPath, []byte("api-keys:\n  - known-key\nclient-api-key-policies:\n  - api-key: known-key\n    output-token-quota: 100\n    output-token-quota-reset-hours: 24\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	h := NewHandler(&config.Config{SDKConfig: config.SDKConfig{
		APIKeys: []string{"known-key"},
		ClientAPIKeyPolicies: []config.ClientAPIKeyPolicy{{
			APIKey:                     "known-key",
			OutputTokenQuota:           100,
			OutputTokenQuotaResetHours: 24,
		}},
	}}, configPath, nil)
	req := httptest.NewRequest(http.MethodPatch, "/v0/management/client-api-key-policies", bytes.NewBufferString(`{"match":"known-key","value":{"output-token-quota-reset":"weekly-from-first-use"}}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rr)
	c.Request = req
	h.PatchClientAPIKeyPolicy(c)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	if got := h.cfg.ClientAPIKeyPolicies[0].OutputTokenQuotaResetHours; got != config.ClientAPIKeyPolicyQuotaResetWeeklyHours {
		t.Fatalf("expected deprecated weekly reset to override with %d hours, got %d", config.ClientAPIKeyPolicyQuotaResetWeeklyHours, got)
	}
	if got := h.cfg.ClientAPIKeyPolicies[0].LegacyOutputTokenQuotaReset; got != "" {
		t.Fatalf("expected deprecated reset field to be cleared, got %q", got)
	}
}

func TestPutClientAPIKeyPoliciesPersistsAlias(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(configPath, []byte("api-keys:\n  - known-key\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	h := NewHandler(&config.Config{SDKConfig: config.SDKConfig{APIKeys: []string{"known-key"}}}, configPath, nil)
	req := httptest.NewRequest(http.MethodPut, "/v0/management/client-api-key-policies", bytes.NewBufferString(`[{"api-key":"known-key","alias":"Workstation","output-token-quota":100}]`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rr)
	c.Request = req
	h.PutClientAPIKeyPolicies(c)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	if got := h.cfg.ClientAPIKeyPolicies[0].Alias; got != "Workstation" {
		t.Fatalf("expected alias to persist, got %q", got)
	}
}

func TestPatchClientAPIKeyPolicyCreatesAliasOnlyPolicy(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(configPath, []byte("api-keys:\n  - known-key\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	h := NewHandler(&config.Config{SDKConfig: config.SDKConfig{APIKeys: []string{"known-key"}}}, configPath, nil)
	req := httptest.NewRequest(http.MethodPatch, "/v0/management/client-api-key-policies", bytes.NewBufferString(`{"match":"known-key","value":{"api-key":"known-key","alias":"Laptop"}}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rr)
	c.Request = req
	h.PatchClientAPIKeyPolicy(c)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	if len(h.cfg.ClientAPIKeyPolicies) != 1 {
		t.Fatalf("expected one policy, got %d", len(h.cfg.ClientAPIKeyPolicies))
	}
	if got := h.cfg.ClientAPIKeyPolicies[0].Alias; got != "Laptop" {
		t.Fatalf("expected alias-only policy to persist alias, got %q", got)
	}
	if got := h.cfg.ClientAPIKeyPolicies[0].OutputTokenQuota; got != 0 {
		t.Fatalf("expected alias-only policy to keep quota 0, got %d", got)
	}
}

func TestPatchClientAPIKeyPolicyRejectsEmptyAliasOnlyCreate(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(configPath, []byte("api-keys:\n  - known-key\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	h := NewHandler(&config.Config{SDKConfig: config.SDKConfig{APIKeys: []string{"known-key"}}}, configPath, nil)
	req := httptest.NewRequest(http.MethodPatch, "/v0/management/client-api-key-policies", bytes.NewBufferString(`{"match":"known-key","value":{"api-key":"known-key","alias":"  "}}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rr)
	c.Request = req
	h.PatchClientAPIKeyPolicy(c)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 no-op, got %d body=%s", rr.Code, rr.Body.String())
	}
	if len(h.cfg.ClientAPIKeyPolicies) != 0 {
		t.Fatalf("expected no policies to be created, got %#v", h.cfg.ClientAPIKeyPolicies)
	}
}

func TestPatchClientAPIKeyPolicyTreatsBlankSelectedAuthCreateAsNoOp(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(configPath, []byte("api-keys:\n  - known-key\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	h := NewHandler(&config.Config{SDKConfig: config.SDKConfig{APIKeys: []string{"known-key"}}}, configPath, nil)
	req := httptest.NewRequest(http.MethodPatch, "/v0/management/client-api-key-policies", bytes.NewBufferString(`{"match":"known-key","value":{"api-key":"known-key","selected-auth-index":"  "}}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rr)
	c.Request = req
	h.PatchClientAPIKeyPolicy(c)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	if len(h.cfg.ClientAPIKeyPolicies) != 0 {
		t.Fatalf("expected no policies to be created, got %#v", h.cfg.ClientAPIKeyPolicies)
	}
}

func TestPatchClientAPIKeyPolicyCreatesSelectedAuthOnlyPolicy(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(configPath, []byte("api-keys:\n  - known-key\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	h := NewHandler(&config.Config{SDKConfig: config.SDKConfig{APIKeys: []string{"known-key"}}}, configPath, nil)
	req := httptest.NewRequest(http.MethodPatch, "/v0/management/client-api-key-policies", bytes.NewBufferString(`{"match":"known-key","value":{"api-key":"known-key","selected-auth-index":"auth-1"}}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rr)
	c.Request = req
	h.PatchClientAPIKeyPolicy(c)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	if len(h.cfg.ClientAPIKeyPolicies) != 1 {
		t.Fatalf("expected one policy, got %d", len(h.cfg.ClientAPIKeyPolicies))
	}
	if got := h.cfg.ClientAPIKeyPolicies[0].SelectedAuthIndex; got != "auth-1" {
		t.Fatalf("expected selected auth index to persist, got %q", got)
	}
}

func TestPatchClientAPIKeyPolicyBackfillsSelectedAuthIDFromIndex(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(configPath, []byte("api-keys:\n  - known-key\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	manager := coreauth.NewManager(nil, nil, nil)
	auth := &coreauth.Auth{ID: "auth-1", Provider: "codex", Status: coreauth.StatusActive}
	if _, err := manager.Register(context.Background(), auth); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	h := NewHandler(&config.Config{SDKConfig: config.SDKConfig{APIKeys: []string{"known-key"}}}, configPath, manager)
	req := httptest.NewRequest(http.MethodPatch, "/v0/management/client-api-key-policies", bytes.NewBufferString(`{"match":"known-key","value":{"api-key":"known-key","selected-auth-index":"`+auth.EnsureIndex()+`"}}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rr)
	c.Request = req
	h.PatchClientAPIKeyPolicy(c)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	if len(h.cfg.ClientAPIKeyPolicies) != 1 {
		t.Fatalf("expected one policy, got %d", len(h.cfg.ClientAPIKeyPolicies))
	}
	if got := h.cfg.ClientAPIKeyPolicies[0].SelectedAuthID; got != auth.ID {
		t.Fatalf("expected selected auth id %q, got %q", auth.ID, got)
	}
	if got := h.cfg.ClientAPIKeyPolicies[0].SelectedAuthIndex; got != auth.EnsureIndex() {
		t.Fatalf("expected selected auth index %q, got %q", auth.EnsureIndex(), got)
	}
}

func TestPatchClientAPIKeyPolicyRewritesStaleIndexFromSelectedAuthID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(configPath, []byte("api-keys:\n  - known-key\nclient-api-key-policies:\n  - api-key: known-key\n    selected-auth-id: auth-1\n    selected-auth-index: stale-index\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	manager := coreauth.NewManager(nil, nil, nil)
	auth := &coreauth.Auth{ID: "auth-1", Provider: "codex", Status: coreauth.StatusActive}
	if _, err := manager.Register(context.Background(), auth); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	h := NewHandler(&config.Config{SDKConfig: config.SDKConfig{APIKeys: []string{"known-key"}, ClientAPIKeyPolicies: []config.ClientAPIKeyPolicy{{APIKey: "known-key", SelectedAuthID: auth.ID, SelectedAuthIndex: "stale-index"}}}}, configPath, manager)
	req := httptest.NewRequest(http.MethodPatch, "/v0/management/client-api-key-policies", bytes.NewBufferString(`{"match":"known-key","value":{"selected-auth-id":"auth-1"}}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rr)
	c.Request = req
	h.PatchClientAPIKeyPolicy(c)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	if got := h.cfg.ClientAPIKeyPolicies[0].SelectedAuthIndex; got != auth.EnsureIndex() {
		t.Fatalf("expected selected auth index %q, got %q", auth.EnsureIndex(), got)
	}
}

func TestPatchClientAPIKeyPolicyUpdatesSelectedAuthIndex(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(configPath, []byte("api-keys:\n  - known-key\nclient-api-key-policies:\n  - api-key: known-key\n    alias: Laptop\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	h := NewHandler(&config.Config{SDKConfig: config.SDKConfig{
		APIKeys:              []string{"known-key"},
		ClientAPIKeyPolicies: []config.ClientAPIKeyPolicy{{APIKey: "known-key", Alias: "Laptop"}},
	}}, configPath, nil)
	req := httptest.NewRequest(http.MethodPatch, "/v0/management/client-api-key-policies", bytes.NewBufferString(`{"match":"known-key","value":{"selected-auth-index":"auth-2"}}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rr)
	c.Request = req
	h.PatchClientAPIKeyPolicy(c)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	if got := h.cfg.ClientAPIKeyPolicies[0].SelectedAuthIndex; got != "auth-2" {
		t.Fatalf("expected selected auth index to update, got %q", got)
	}
}

func TestPatchClientAPIKeyPolicyClearsSelectedAuthIndex(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(configPath, []byte("api-keys:\n  - known-key\nclient-api-key-policies:\n  - api-key: known-key\n    alias: Laptop\n    selected-auth-index: auth-1\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	h := NewHandler(&config.Config{SDKConfig: config.SDKConfig{
		APIKeys:              []string{"known-key"},
		ClientAPIKeyPolicies: []config.ClientAPIKeyPolicy{{APIKey: "known-key", Alias: "Laptop", SelectedAuthID: "auth-id-1", SelectedAuthIndex: "auth-1"}},
	}}, configPath, nil)
	req := httptest.NewRequest(http.MethodPatch, "/v0/management/client-api-key-policies", bytes.NewBufferString(`{"match":"known-key","value":{"selected-auth-index":""}}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rr)
	c.Request = req
	h.PatchClientAPIKeyPolicy(c)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	if got := h.cfg.ClientAPIKeyPolicies[0].SelectedAuthIndex; got != "" {
		t.Fatalf("expected selected auth index to clear, got %q", got)
	}
	if got := h.cfg.ClientAPIKeyPolicies[0].SelectedAuthID; got != "" {
		t.Fatalf("expected selected auth id to clear, got %q", got)
	}
}

func TestPatchClientAPIKeyPolicyRemovesSelectedAuthOnlyPolicyWhenCleared(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(configPath, []byte("api-keys:\n  - known-key\nclient-api-key-policies:\n  - api-key: known-key\n    selected-auth-index: auth-1\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	h := NewHandler(&config.Config{SDKConfig: config.SDKConfig{
		APIKeys:              []string{"known-key"},
		ClientAPIKeyPolicies: []config.ClientAPIKeyPolicy{{APIKey: "known-key", SelectedAuthIndex: "auth-1"}},
	}}, configPath, nil)
	req := httptest.NewRequest(http.MethodPatch, "/v0/management/client-api-key-policies", bytes.NewBufferString(`{"match":"known-key","value":{"selected-auth-index":""}}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rr)
	c.Request = req
	h.PatchClientAPIKeyPolicy(c)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	if len(h.cfg.ClientAPIKeyPolicies) != 0 {
		t.Fatalf("expected selected-auth-only policy to be removed, got %#v", h.cfg.ClientAPIKeyPolicies)
	}
}

func TestPatchClientAPIKeyPolicyRemovesEmptyAliasOnlyPolicy(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(configPath, []byte("api-keys:\n  - known-key\nclient-api-key-policies:\n  - api-key: known-key\n    alias: Laptop\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	h := NewHandler(&config.Config{SDKConfig: config.SDKConfig{
		APIKeys:              []string{"known-key"},
		ClientAPIKeyPolicies: []config.ClientAPIKeyPolicy{{APIKey: "known-key", Alias: "Laptop"}},
	}}, configPath, nil)
	req := httptest.NewRequest(http.MethodPatch, "/v0/management/client-api-key-policies", bytes.NewBufferString(`{"match":"known-key","value":{"alias":""}}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rr)
	c.Request = req
	h.PatchClientAPIKeyPolicy(c)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	if len(h.cfg.ClientAPIKeyPolicies) != 0 {
		t.Fatalf("expected empty alias-only policy to be removed, got %#v", h.cfg.ClientAPIKeyPolicies)
	}
}

func TestPutClientAPIKeyPoliciesDoesNotMutateOnPersistFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tmpDir := t.TempDir()
	h := NewHandler(&config.Config{SDKConfig: config.SDKConfig{APIKeys: []string{"known-key"}}}, tmpDir, nil)
	req := httptest.NewRequest(http.MethodPut, "/v0/management/client-api-key-policies", bytes.NewBufferString(`[{"api-key":"known-key","output-token-quota":100}]`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rr)
	c.Request = req
	h.PutClientAPIKeyPolicies(c)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d body=%s", rr.Code, rr.Body.String())
	}
	if len(h.cfg.ClientAPIKeyPolicies) != 0 {
		data, _ := json.Marshal(h.cfg.ClientAPIKeyPolicies)
		t.Fatalf("expected handler state unchanged on persist failure, got %s", string(data))
	}
}

func TestPutClientAPIKeyPoliciesDoesNotMutateOnQuotaManagerFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(configPath, []byte("api-keys:\n  - known-key\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	blocker := filepath.Join(tmpDir, "blocked")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile() blocker error = %v", err)
	}
	cfg := &config.Config{SDKConfig: config.SDKConfig{APIKeys: []string{"known-key"}}}
	cfg.SQLitePromptLog.Path = filepath.Join(blocker, "quota.sqlite")
	h := NewHandler(cfg, configPath, nil)
	h.SetQuotaManager(quota.NewManager())
	req := httptest.NewRequest(http.MethodPut, "/v0/management/client-api-key-policies", bytes.NewBufferString(`[{"api-key":"known-key","output-token-quota":100}]`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rr)
	c.Request = req
	h.PutClientAPIKeyPolicies(c)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d body=%s", rr.Code, rr.Body.String())
	}
	if len(h.cfg.ClientAPIKeyPolicies) != 0 {
		data, _ := json.Marshal(h.cfg.ClientAPIKeyPolicies)
		t.Fatalf("expected handler state unchanged on quota manager failure, got %s", string(data))
	}
}
