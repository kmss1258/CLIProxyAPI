package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gin "github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/concurrency"
	proxyconfig "github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	internallogging "github.com/router-for-me/CLIProxyAPI/v6/internal/logging"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/quota"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/registry"
	sdkaccess "github.com/router-for-me/CLIProxyAPI/v6/sdk/access"
	"github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/executor"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v6/sdk/config"
	"golang.org/x/crypto/bcrypt"
)

type claudeRouteTestExecutor struct{}

func (e *claudeRouteTestExecutor) Identifier() string { return "claude" }

func (e *claudeRouteTestExecutor) Execute(context.Context, *auth.Auth, cliproxyexecutor.Request, cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	return cliproxyexecutor.Response{Payload: []byte(`{"id":"msg_test","type":"message","role":"assistant","content":[{"type":"text","text":"ok"}]}`)}, nil
}

func (e *claudeRouteTestExecutor) ExecuteStream(context.Context, *auth.Auth, cliproxyexecutor.Request, cliproxyexecutor.Options) (*cliproxyexecutor.StreamResult, error) {
	chunks := make(chan cliproxyexecutor.StreamChunk, 2)
	chunks <- cliproxyexecutor.StreamChunk{Payload: []byte("event: message_start\ndata: {\"type\":\"message_start\"}\n\n")}
	chunks <- cliproxyexecutor.StreamChunk{Payload: []byte("event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\",\"stop_sequence\":null},\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}\n\n")}
	close(chunks)
	return &cliproxyexecutor.StreamResult{Chunks: chunks}, nil
}

func (e *claudeRouteTestExecutor) CountTokens(context.Context, *auth.Auth, cliproxyexecutor.Request, cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	return cliproxyexecutor.Response{Payload: []byte(`{"input_tokens":3}`)}, nil
}

func (e *claudeRouteTestExecutor) Refresh(context.Context, *auth.Auth) (*auth.Auth, error) {
	return nil, nil
}

func (e *claudeRouteTestExecutor) HttpRequest(context.Context, *auth.Auth, *http.Request) (*http.Response, error) {
	return nil, nil
}

func newTestServer(t *testing.T) *Server {
	t.Helper()

	gin.SetMode(gin.TestMode)

	tmpDir := t.TempDir()
	authDir := filepath.Join(tmpDir, "auth")
	if err := os.MkdirAll(authDir, 0o700); err != nil {
		t.Fatalf("failed to create auth dir: %v", err)
	}

	cfg := &proxyconfig.Config{
		SDKConfig: sdkconfig.SDKConfig{
			APIKeys: []string{"test-key"},
		},
		Port:                   0,
		AuthDir:                authDir,
		Debug:                  true,
		LoggingToFile:          false,
		UsageStatisticsEnabled: false,
	}

	authManager := auth.NewManager(nil, nil, nil)
	accessManager := sdkaccess.NewManager()

	configPath := filepath.Join(tmpDir, "config.yaml")
	return NewServer(cfg, authManager, accessManager, configPath)
}

func newClaudeRouteTestServer(t *testing.T, modelID string) *Server {
	t.Helper()
	server := newTestServer(t)
	server.handlers.AuthManager.RegisterExecutor(&claudeRouteTestExecutor{})
	claudeAuth := &auth.Auth{ID: "claude-auth-1", Provider: "claude", Status: auth.StatusActive}
	if _, err := server.handlers.AuthManager.Register(context.Background(), claudeAuth); err != nil {
		t.Fatalf("failed to register claude auth: %v", err)
	}
	registry.GetGlobalRegistry().RegisterClient(claudeAuth.ID, claudeAuth.Provider, []*registry.ModelInfo{{ID: modelID}})
	t.Cleanup(func() {
		registry.GetGlobalRegistry().UnregisterClient(claudeAuth.ID)
	})
	return server
}

func TestHealthz(t *testing.T) {
	server := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rr := httptest.NewRecorder()
	server.engine.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected status code: got %d want %d; body=%s", rr.Code, http.StatusOK, rr.Body.String())
	}

	var resp struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response JSON: %v; body=%s", err, rr.Body.String())
	}
	if resp.Status != "ok" {
		t.Fatalf("unexpected response status: got %q want %q", resp.Status, "ok")
	}
}

func TestQuotaHTML(t *testing.T) {
	server := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/quota.html", nil)
	rr := httptest.NewRecorder()
	server.engine.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected status code: got %d want %d; body=%s", rr.Code, http.StatusOK, rr.Body.String())
	}
	if body := rr.Body.String(); !strings.Contains(body, "쿼터 운영 콘솔") || !strings.Contains(body, "/v0/quota-status") {
		t.Fatalf("quota page body missing expected content: %s", body)
	}
	if body := rr.Body.String(); !strings.Contains(body, "API 키 quota 현황") || !strings.Contains(body, "API 키 사용량 상세") {
		t.Fatalf("quota page body missing workflow headings: %s", body)
	}
	if body := rr.Body.String(); !strings.Contains(body, "API 키 별칭") || !strings.Contains(body, "saveAPIKeyAlias") {
		t.Fatalf("quota page body missing alias editor content: %s", body)
	}
	if body := rr.Body.String(); !strings.Contains(body, "새 API 키 발급") || !strings.Contains(body, "API 키 발급") || !strings.Contains(body, `value="100000"`) || !strings.Contains(body, `value="24"`) {
		t.Fatalf("quota page body missing api key creation panel: %s", body)
	}
	if body := rr.Body.String(); !strings.Contains(body, "API 키 관리 고급 설정") || !strings.Contains(body, "/v0/management/concurrency-status") {
		t.Fatalf("quota page body missing concurrency management panel: %s", body)
	}
	if body := rr.Body.String(); !strings.Contains(body, "동시 요청 override") || !strings.Contains(body, "기본값으로 되돌리기") {
		t.Fatalf("quota page body missing inline concurrency controls: %s", body)
	}
	if body := rr.Body.String(); !strings.Contains(body, `<link rel="icon" href="/favicon.ico" type="image/x-icon">`) {
		t.Fatalf("quota page body missing favicon link: %s", body)
	}
}

func TestQuotaFavicon(t *testing.T) {
	server := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/favicon.ico", nil)
	rr := httptest.NewRecorder()
	server.engine.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected status code: got %d want %d; body=%s", rr.Code, http.StatusOK, rr.Body.String())
	}
	if got := rr.Header().Get("Content-Type"); got != "image/x-icon" {
		t.Fatalf("unexpected content type: got %q want %q", got, "image/x-icon")
	}
	if rr.Body.Len() == 0 {
		t.Fatalf("expected non-empty favicon body")
	}
}

func TestClaudeEventLoggingBatchEndpoint(t *testing.T) {
	server := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/event_logging/batch", bytes.NewBufferString(`{"events":[]}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	server.engine.ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("unexpected status code: got %d want %d; body=%s", rr.Code, http.StatusNoContent, rr.Body.String())
	}
	if rr.Body.Len() != 0 {
		t.Fatalf("expected empty body, got %q", rr.Body.String())
	}
}

func TestClaudeMessagesRoute_NonStreaming(t *testing.T) {
	server := newClaudeRouteTestServer(t, "claude-sonnet-latest")
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewBufferString(`{"model":"claude-sonnet-latest","messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]}`))
	req.Header.Set("Authorization", "Bearer test-key")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	server.engine.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected status code: got %d want %d; body=%s", rr.Code, http.StatusOK, rr.Body.String())
	}
	if got := rr.Header().Get("Content-Type"); !strings.Contains(got, "application/json") {
		t.Fatalf("unexpected content type: %q", got)
	}
	if body := rr.Body.String(); !strings.Contains(body, `"type":"message"`) || !strings.Contains(body, `"text":"ok"`) {
		t.Fatalf("unexpected response body: %s", body)
	}
}

func TestClaudeMessagesRoute_Streaming(t *testing.T) {
	server := newClaudeRouteTestServer(t, "claude-sonnet-latest")
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewBufferString(`{"model":"claude-sonnet-latest","stream":true,"messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]}`))
	req.Header.Set("Authorization", "Bearer test-key")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	server.engine.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected status code: got %d want %d; body=%s", rr.Code, http.StatusOK, rr.Body.String())
	}
	if got := rr.Header().Get("Content-Type"); !strings.Contains(got, "text/event-stream") {
		t.Fatalf("unexpected content type: %q", got)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "event: message_start") || !strings.Contains(body, "event: message_delta") {
		t.Fatalf("unexpected stream body: %s", body)
	}
	if !strings.Contains(body, `"stop_reason":"end_turn"`) {
		t.Fatalf("stream body missing stop_reason: %s", body)
	}
}

func TestClaudeCountTokensRoute(t *testing.T) {
	server := newClaudeRouteTestServer(t, "claude-sonnet-latest")
	req := httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", bytes.NewBufferString(`{"model":"claude-sonnet-latest","messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]}`))
	req.Header.Set("Authorization", "Bearer test-key")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	server.engine.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected status code: got %d want %d; body=%s", rr.Code, http.StatusOK, rr.Body.String())
	}
	if body := rr.Body.String(); body != `{"input_tokens":3}` {
		t.Fatalf("unexpected count_tokens body: %s", body)
	}
}

func TestV1ModelsIncludesClaudeModel(t *testing.T) {
	server := newClaudeRouteTestServer(t, "claude-sonnet-latest")
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer test-key")
	rr := httptest.NewRecorder()
	server.engine.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected status code: got %d want %d; body=%s", rr.Code, http.StatusOK, rr.Body.String())
	}
	if body := rr.Body.String(); !strings.Contains(body, "claude-sonnet-latest") {
		t.Fatalf("expected claude model in models response, got %s", body)
	}
}

func TestManagementQuotaStatusRouteRequiresKey(t *testing.T) {
	server := newTestServer(t)
	hash, err := bcrypt.GenerateFromPassword([]byte("secret-key"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("GenerateFromPassword() error = %v", err)
	}
	server.cfg.RemoteManagement.SecretKey = string(hash)
	server.cfg.RemoteManagement.AllowRemote = true
	server.registerManagementRoutes()
	server.managementRoutesEnabled.Store(true)
	req := httptest.NewRequest(http.MethodGet, "/v0/management/quota-status", nil)
	rr := httptest.NewRecorder()
	server.engine.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without key, got %d body=%s", rr.Code, rr.Body.String())
	}
	req = httptest.NewRequest(http.MethodGet, "/v0/management/quota-status", nil)
	req.Header.Set("Authorization", "Bearer secret-key")
	rr = httptest.NewRecorder()
	server.engine.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 with key, got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestManagementConcurrencyRoutesRequireKeyAndReturnPayload(t *testing.T) {
	server := newTestServer(t)
	hash, err := bcrypt.GenerateFromPassword([]byte("secret-key"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("GenerateFromPassword() error = %v", err)
	}
	server.cfg.RemoteManagement.SecretKey = string(hash)
	server.cfg.RemoteManagement.AllowRemote = true
	server.cfg.Concurrency = proxyconfig.ConcurrencyConfig{
		Enabled: true,
		Defaults: proxyconfig.ConcurrencyDefaults{
			GlobalInFlight:  10,
			ClientAPIKey:    2,
			UpstreamAccount: 1,
		},
		Endpoints: []proxyconfig.EndpointConcurrencyLimit{{Path: "/v1/chat/completions", InFlight: 4}},
	}
	concurrency.DefaultManager().UpdateConfig(server.cfg)
	server.registerManagementRoutes()
	server.managementRoutesEnabled.Store(true)

	req := httptest.NewRequest(http.MethodGet, "/v0/management/concurrency-config", nil)
	resp := httptest.NewRecorder()
	server.engine.ServeHTTP(resp, req)
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without key, got %d body=%s", resp.Code, resp.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/v0/management/concurrency-config", nil)
	req.Header.Set("Authorization", "Bearer secret-key")
	resp = httptest.NewRecorder()
	server.engine.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200 for concurrency config, got %d body=%s", resp.Code, resp.Body.String())
	}
	if body := resp.Body.String(); !strings.Contains(body, "global-inflight") || !strings.Contains(body, "/v1/chat/completions") {
		t.Fatalf("unexpected concurrency config body: %s", body)
	}

	req = httptest.NewRequest(http.MethodGet, "/v0/management/concurrency-status", nil)
	req.Header.Set("Authorization", "Bearer secret-key")
	resp = httptest.NewRecorder()
	server.engine.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200 for concurrency status, got %d body=%s", resp.Code, resp.Body.String())
	}
	if body := resp.Body.String(); !strings.Contains(body, "client-api-keys") || !strings.Contains(body, "upstream-accounts") {
		t.Fatalf("unexpected concurrency status body: %s", body)
	}
}

func TestV1RoutesApplyConcurrencyMiddleware(t *testing.T) {
	server := newTestServer(t)
	server.cfg.ClientAPIKeyPolicies = []proxyconfig.ClientAPIKeyPolicy{{APIKey: "test-key", Concurrency: 1}}
	server.cfg.Concurrency = proxyconfig.ConcurrencyConfig{Enabled: true}
	concurrency.DefaultManager().UpdateConfig(server.cfg)
	release, exceeded := concurrency.DefaultManager().AcquireHTTP("test-key", "/v1/chat/completions")
	if exceeded != nil {
		t.Fatalf("expected pre-acquire to succeed, got %v", exceeded)
	}
	defer release()

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4.1","messages":[]}`))
	req.Header.Set("Authorization", "Bearer test-key")
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	server.engine.ServeHTTP(resp, req)
	if resp.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d body=%s", resp.Code, resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), "client_concurrency_limit_exceeded") {
		t.Fatalf("expected concurrency error body, got %s", resp.Body.String())
	}
}

func TestManagementCanPatchConcurrencyConfigAndClientPolicy(t *testing.T) {
	server := newTestServer(t)
	hash, err := bcrypt.GenerateFromPassword([]byte("secret-key"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("GenerateFromPassword() error = %v", err)
	}
	server.cfg.RemoteManagement.SecretKey = string(hash)
	server.cfg.RemoteManagement.AllowRemote = true
	if data, errMarshal := json.Marshal(server.cfg); errMarshal != nil {
		t.Fatalf("Marshal() error = %v", errMarshal)
	} else if errWrite := os.WriteFile(server.configFilePath, data, 0o644); errWrite != nil {
		t.Fatalf("WriteFile() error = %v", errWrite)
	}
	server.registerManagementRoutes()
	server.managementRoutesEnabled.Store(true)

	patchConfigBody := []byte(`{"enabled":true,"defaults":{"client-api-key":4,"upstream-account":2}}`)
	req := httptest.NewRequest(http.MethodPatch, "/v0/management/concurrency-config", bytes.NewReader(patchConfigBody))
	req.Header.Set("Authorization", "Bearer secret-key")
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	server.engine.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200 from concurrency config patch, got %d body=%s", resp.Code, resp.Body.String())
	}
	if server.cfg.Concurrency.Defaults.ClientAPIKey != 4 || server.cfg.Concurrency.Defaults.UpstreamAccount != 2 || !server.cfg.Concurrency.Enabled {
		t.Fatalf("unexpected concurrency config after patch: %+v", server.cfg.Concurrency)
	}

	patchPolicyBody := []byte(`{"value":{"api-key":"test-key","concurrency":3}}`)
	req = httptest.NewRequest(http.MethodPatch, "/v0/management/client-api-key-concurrency-policies", bytes.NewReader(patchPolicyBody))
	req.Header.Set("Authorization", "Bearer secret-key")
	req.Header.Set("Content-Type", "application/json")
	resp = httptest.NewRecorder()
	server.engine.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200 from client concurrency patch, got %d body=%s", resp.Code, resp.Body.String())
	}
	if len(server.cfg.ClientAPIKeyPolicies) != 1 || server.cfg.ClientAPIKeyPolicies[0].Concurrency != 3 {
		t.Fatalf("unexpected client api key policies after patch: %+v", server.cfg.ClientAPIKeyPolicies)
	}

	clearConfigBody := []byte(`{"concurrency":{"enabled":false,"defaults":{"global-inflight":0,"client-api-key":0,"upstream-account":0},"endpoints":[]}}`)
	req = httptest.NewRequest(http.MethodPut, "/v0/management/concurrency-config", bytes.NewReader(clearConfigBody))
	req.Header.Set("Authorization", "Bearer secret-key")
	req.Header.Set("Content-Type", "application/json")
	resp = httptest.NewRecorder()
	server.engine.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200 from concurrency config put clear, got %d body=%s", resp.Code, resp.Body.String())
	}
	if server.cfg.Concurrency.Enabled || server.cfg.Concurrency.Defaults.GlobalInFlight != 0 || server.cfg.Concurrency.Defaults.ClientAPIKey != 0 || server.cfg.Concurrency.Defaults.UpstreamAccount != 0 || len(server.cfg.Concurrency.Endpoints) != 0 {
		t.Fatalf("expected concurrency config to clear to zeros, got %+v", server.cfg.Concurrency)
	}
}

func TestAmpProviderModelRoutes(t *testing.T) {
	testCases := []struct {
		name         string
		path         string
		wantStatus   int
		wantContains string
	}{
		{
			name:         "openai root models",
			path:         "/api/provider/openai/models",
			wantStatus:   http.StatusOK,
			wantContains: `"object":"list"`,
		},
		{
			name:         "groq root models",
			path:         "/api/provider/groq/models",
			wantStatus:   http.StatusOK,
			wantContains: `"object":"list"`,
		},
		{
			name:         "openai models",
			path:         "/api/provider/openai/v1/models",
			wantStatus:   http.StatusOK,
			wantContains: `"object":"list"`,
		},
		{
			name:         "anthropic models",
			path:         "/api/provider/anthropic/v1/models",
			wantStatus:   http.StatusOK,
			wantContains: `"data"`,
		},
		{
			name:         "google models v1",
			path:         "/api/provider/google/v1/models",
			wantStatus:   http.StatusOK,
			wantContains: `"models"`,
		},
		{
			name:         "google models v1beta",
			path:         "/api/provider/google/v1beta/models",
			wantStatus:   http.StatusOK,
			wantContains: `"models"`,
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			server := newTestServer(t)

			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			req.Header.Set("Authorization", "Bearer test-key")

			rr := httptest.NewRecorder()
			server.engine.ServeHTTP(rr, req)

			if rr.Code != tc.wantStatus {
				t.Fatalf("unexpected status code for %s: got %d want %d; body=%s", tc.path, rr.Code, tc.wantStatus, rr.Body.String())
			}
			if body := rr.Body.String(); !strings.Contains(body, tc.wantContains) {
				t.Fatalf("response body for %s missing %q: %s", tc.path, tc.wantContains, body)
			}
		})
	}
}

func TestAmpProviderRoutesApplyQuotaMiddleware(t *testing.T) {
	server := newTestServer(t)
	tmpDir := t.TempDir()
	server.cfg.SQLitePromptLog.Path = filepath.Join(tmpDir, "quota.sqlite")
	server.cfg.ClientAPIKeyPolicies = []proxyconfig.ClientAPIKeyPolicy{{APIKey: "test-key", OutputTokenQuota: 1}}
	if err := quota.DefaultManager().UpdateConfig(server.cfg, filepath.Join(tmpDir, "config.yaml")); err != nil {
		t.Fatalf("UpdateConfig() error = %v", err)
	}
	if err := quota.DefaultManager().ApplyUsage("test-key", 1); err != nil {
		t.Fatalf("ApplyUsage() error = %v", err)
	}
	defer func() { _ = quota.DefaultManager().ResetUsage("test-key") }()
	req := httptest.NewRequest(http.MethodGet, "/api/provider/openai/models", nil)
	req.Header.Set("Authorization", "Bearer test-key")
	rr := httptest.NewRecorder()
	server.engine.ServeHTTP(rr, req)
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestAmpGoogleV1Beta1BridgeAppliesQuotaMiddleware(t *testing.T) {
	server := newTestServer(t)
	tmpDir := t.TempDir()
	server.cfg.SQLitePromptLog.Path = filepath.Join(tmpDir, "quota.sqlite")
	server.cfg.ClientAPIKeyPolicies = []proxyconfig.ClientAPIKeyPolicy{{APIKey: "test-key", OutputTokenQuota: 1}}
	if err := quota.DefaultManager().UpdateConfig(server.cfg, filepath.Join(tmpDir, "config.yaml")); err != nil {
		t.Fatalf("UpdateConfig() error = %v", err)
	}
	if err := quota.DefaultManager().ApplyUsage("test-key", 1); err != nil {
		t.Fatalf("ApplyUsage() error = %v", err)
	}
	defer func() { _ = quota.DefaultManager().ResetUsage("test-key") }()
	req := httptest.NewRequest(http.MethodPost, "/api/provider/google/v1beta1/models/generateContent", strings.NewReader(`{"contents":[]}`))
	req.Header.Set("Authorization", "Bearer test-key")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	server.engine.ServeHTTP(rr, req)
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestAmpGoogleV1Beta1BridgeAppliesConcurrencyMiddleware(t *testing.T) {
	server := newTestServer(t)
	server.cfg.ClientAPIKeyPolicies = []proxyconfig.ClientAPIKeyPolicy{{APIKey: "test-key", Concurrency: 1}}
	server.cfg.Concurrency = proxyconfig.ConcurrencyConfig{Enabled: true}
	concurrency.DefaultManager().UpdateConfig(server.cfg)
	release, exceeded := concurrency.DefaultManager().AcquireHTTP("test-key", "/api/provider/google/v1beta1/*path")
	if exceeded != nil {
		t.Fatalf("expected pre-acquire to succeed, got %v", exceeded)
	}
	defer release()
	req := httptest.NewRequest(http.MethodPost, "/api/provider/google/v1beta1/models/generateContent", strings.NewReader(`{"contents":[]}`))
	req.Header.Set("Authorization", "Bearer test-key")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	server.engine.ServeHTTP(rr, req)
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "client_concurrency_limit_exceeded") {
		t.Fatalf("expected concurrency error body, got %s", rr.Body.String())
	}
}

func TestDefaultRequestLoggerFactory_UsesResolvedLogDirectory(t *testing.T) {
	t.Setenv("WRITABLE_PATH", "")
	t.Setenv("writable_path", "")

	originalWD, errGetwd := os.Getwd()
	if errGetwd != nil {
		t.Fatalf("failed to get current working directory: %v", errGetwd)
	}

	tmpDir := t.TempDir()
	if errChdir := os.Chdir(tmpDir); errChdir != nil {
		t.Fatalf("failed to switch working directory: %v", errChdir)
	}
	defer func() {
		if errChdirBack := os.Chdir(originalWD); errChdirBack != nil {
			t.Fatalf("failed to restore working directory: %v", errChdirBack)
		}
	}()

	// Force ResolveLogDirectory to fallback to auth-dir/logs by making ./logs not a writable directory.
	if errWriteFile := os.WriteFile(filepath.Join(tmpDir, "logs"), []byte("not-a-directory"), 0o644); errWriteFile != nil {
		t.Fatalf("failed to create blocking logs file: %v", errWriteFile)
	}

	configDir := filepath.Join(tmpDir, "config")
	if errMkdirConfig := os.MkdirAll(configDir, 0o755); errMkdirConfig != nil {
		t.Fatalf("failed to create config dir: %v", errMkdirConfig)
	}
	configPath := filepath.Join(configDir, "config.yaml")

	authDir := filepath.Join(tmpDir, "auth")
	if errMkdirAuth := os.MkdirAll(authDir, 0o700); errMkdirAuth != nil {
		t.Fatalf("failed to create auth dir: %v", errMkdirAuth)
	}

	cfg := &proxyconfig.Config{
		SDKConfig: proxyconfig.SDKConfig{
			RequestLog: false,
		},
		AuthDir:           authDir,
		ErrorLogsMaxFiles: 10,
	}

	logger := defaultRequestLoggerFactory(cfg, configPath)
	fileLogger, ok := logger.(*internallogging.FileRequestLogger)
	if !ok {
		t.Fatalf("expected *FileRequestLogger, got %T", logger)
	}

	errLog := fileLogger.LogRequestWithOptions(
		"/v1/chat/completions",
		http.MethodPost,
		map[string][]string{"Content-Type": []string{"application/json"}},
		[]byte(`{"input":"hello"}`),
		http.StatusBadGateway,
		map[string][]string{"Content-Type": []string{"application/json"}},
		[]byte(`{"error":"upstream failure"}`),
		nil,
		nil,
		nil,
		nil,
		nil,
		true,
		"issue-1711",
		time.Now(),
		time.Now(),
	)
	if errLog != nil {
		t.Fatalf("failed to write forced error request log: %v", errLog)
	}

	authLogsDir := filepath.Join(authDir, "logs")
	authEntries, errReadAuthDir := os.ReadDir(authLogsDir)
	if errReadAuthDir != nil {
		t.Fatalf("failed to read auth logs dir %s: %v", authLogsDir, errReadAuthDir)
	}
	foundErrorLogInAuthDir := false
	for _, entry := range authEntries {
		if strings.HasPrefix(entry.Name(), "error-") && strings.HasSuffix(entry.Name(), ".log") {
			foundErrorLogInAuthDir = true
			break
		}
	}
	if !foundErrorLogInAuthDir {
		t.Fatalf("expected forced error log in auth fallback dir %s, got entries: %+v", authLogsDir, authEntries)
	}

	configLogsDir := filepath.Join(configDir, "logs")
	configEntries, errReadConfigDir := os.ReadDir(configLogsDir)
	if errReadConfigDir != nil && !os.IsNotExist(errReadConfigDir) {
		t.Fatalf("failed to inspect config logs dir %s: %v", configLogsDir, errReadConfigDir)
	}
	for _, entry := range configEntries {
		if strings.HasPrefix(entry.Name(), "error-") && strings.HasSuffix(entry.Name(), ".log") {
			t.Fatalf("unexpected forced error log in config dir %s", configLogsDir)
		}
	}
}

func TestRequestLoggerEnabledPrefersSQLitePromptLog(t *testing.T) {
	cfg := &proxyconfig.Config{SDKConfig: proxyconfig.SDKConfig{RequestLog: false, SQLitePromptLog: proxyconfig.SQLitePromptLogConfig{Enabled: true}}}
	if !requestLoggerEnabled(cfg) {
		t.Fatalf("expected sqlite prompt log to force request logger enabled")
	}
	if requestLoggerEnabled(&proxyconfig.Config{}) {
		t.Fatalf("expected empty config to keep request logger disabled")
	}
}
