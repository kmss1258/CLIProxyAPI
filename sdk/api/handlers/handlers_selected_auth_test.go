package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	internalconfig "github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/registry"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	coreexecutor "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/executor"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v6/sdk/config"
)

type selectedAuthAwareExecutor struct {
	executeAuthID string
	countAuthID   string
	streamAuthID  string
}

func (e *selectedAuthAwareExecutor) Identifier() string { return "codex" }

func (e *selectedAuthAwareExecutor) Execute(context.Context, *coreauth.Auth, coreexecutor.Request, coreexecutor.Options) (coreexecutor.Response, error) {
	return coreexecutor.Response{}, &coreauth.Error{Code: "not_implemented", Message: "unexpected Execute call"}
}

func (e *selectedAuthAwareExecutor) ExecuteStream(context.Context, *coreauth.Auth, coreexecutor.Request, coreexecutor.Options) (*coreexecutor.StreamResult, error) {
	return nil, &coreauth.Error{Code: "not_implemented", Message: "unexpected ExecuteStream call"}
}

func (e *selectedAuthAwareExecutor) Refresh(context.Context, *coreauth.Auth) (*coreauth.Auth, error) {
	return nil, &coreauth.Error{Code: "not_implemented", Message: "Refresh not implemented"}
}

func (e *selectedAuthAwareExecutor) CountTokens(context.Context, *coreauth.Auth, coreexecutor.Request, coreexecutor.Options) (coreexecutor.Response, error) {
	return coreexecutor.Response{}, &coreauth.Error{Code: "not_implemented", Message: "unexpected CountTokens call"}
}

func (e *selectedAuthAwareExecutor) HttpRequest(context.Context, *coreauth.Auth, *http.Request) (*http.Response, error) {
	return nil, &coreauth.Error{Code: "not_implemented", Message: "HttpRequest not implemented", HTTPStatus: http.StatusNotImplemented}
}

type selectedAuthExecuteExecutor struct{ selectedAuthAwareExecutor }

func (e *selectedAuthExecuteExecutor) Execute(_ context.Context, auth *coreauth.Auth, _ coreexecutor.Request, _ coreexecutor.Options) (coreexecutor.Response, error) {
	if auth != nil {
		e.executeAuthID = auth.ID
	}
	return coreexecutor.Response{Payload: []byte(`{"ok":true}`)}, nil
}

type selectedAuthCountExecutor struct{ selectedAuthAwareExecutor }

func (e *selectedAuthCountExecutor) CountTokens(_ context.Context, auth *coreauth.Auth, _ coreexecutor.Request, _ coreexecutor.Options) (coreexecutor.Response, error) {
	if auth != nil {
		e.countAuthID = auth.ID
	}
	return coreexecutor.Response{Payload: []byte(`{"total_tokens":1}`)}, nil
}

type selectedAuthStreamExecutor struct{ selectedAuthAwareExecutor }

func (e *selectedAuthStreamExecutor) ExecuteStream(_ context.Context, auth *coreauth.Auth, _ coreexecutor.Request, _ coreexecutor.Options) (*coreexecutor.StreamResult, error) {
	if auth != nil {
		e.streamAuthID = auth.ID
	}
	ch := make(chan coreexecutor.StreamChunk, 1)
	ch <- coreexecutor.StreamChunk{Payload: []byte("ok")}
	close(ch)
	return &coreexecutor.StreamResult{Chunks: ch}, nil
}

func selectedAuthTestContext(apiKey string) context.Context {
	gin.SetMode(gin.TestMode)
	rr := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rr)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	c.Set("apiKey", apiKey)
	return context.WithValue(context.Background(), "gin", c)
}

func selectedAuthTestHandler(t *testing.T, executor coreauth.ProviderExecutor, policies []internalconfig.ClientAPIKeyPolicy, auths ...*coreauth.Auth) *BaseAPIHandler {
	t.Helper()
	manager := coreauth.NewManager(nil, nil, nil)
	manager.RegisterExecutor(executor)
	for _, auth := range auths {
		if _, err := manager.Register(context.Background(), auth); err != nil {
			t.Fatalf("manager.Register(%s): %v", auth.ID, err)
		}
	}
	handler := NewBaseAPIHandlers(&sdkconfig.SDKConfig{ClientAPIKeyPolicies: policies}, manager)
	return handler
}

func registerSelectedAuthTestModel(t *testing.T, auth *coreauth.Auth, model string) {
	t.Helper()
	registry.GetGlobalRegistry().RegisterClient(auth.ID, auth.Provider, []*registry.ModelInfo{{ID: model}})
	t.Cleanup(func() {
		registry.GetGlobalRegistry().UnregisterClient(auth.ID)
	})
}

func TestApplyClientAPIKeySelectedAuthPinsMatchingAuth(t *testing.T) {
	auth := &coreauth.Auth{ID: "auth-1", Provider: "codex", Status: coreauth.StatusActive}
	registerSelectedAuthTestModel(t, auth, "test-model")
	handler := selectedAuthTestHandler(t, &selectedAuthExecuteExecutor{}, []internalconfig.ClientAPIKeyPolicy{{APIKey: "client-key", SelectedAuthID: auth.ID, SelectedAuthIndex: auth.EnsureIndex()}}, auth)
	ctx, err := handler.applyClientAPIKeySelectedAuth(selectedAuthTestContext("client-key"), []string{"codex"}, "test-model")
	if err != nil {
		t.Fatalf("applyClientAPIKeySelectedAuth() error = %v", err)
	}
	if got := pinnedAuthIDFromContext(ctx); got != auth.ID {
		t.Fatalf("pinned auth id = %q, want %q", got, auth.ID)
	}
}

func TestApplyClientAPIKeySelectedAuthPinsMatchingAuthByIDOnly(t *testing.T) {
	auth := &coreauth.Auth{ID: "auth-1", Provider: "codex", Status: coreauth.StatusActive}
	registerSelectedAuthTestModel(t, auth, "test-model")
	handler := selectedAuthTestHandler(t, &selectedAuthExecuteExecutor{}, []internalconfig.ClientAPIKeyPolicy{{APIKey: "client-key", SelectedAuthID: auth.ID}}, auth)
	ctx, err := handler.applyClientAPIKeySelectedAuth(selectedAuthTestContext("client-key"), []string{"codex"}, "test-model")
	if err != nil {
		t.Fatalf("applyClientAPIKeySelectedAuth() error = %v", err)
	}
	if got := pinnedAuthIDFromContext(ctx); got != auth.ID {
		t.Fatalf("pinned auth id = %q, want %q", got, auth.ID)
	}
}

func TestApplyClientAPIKeySelectedAuthRejectsDisabledAuth(t *testing.T) {
	auth := &coreauth.Auth{ID: "auth-1", Provider: "codex", Status: coreauth.StatusDisabled, Disabled: true}
	registerSelectedAuthTestModel(t, auth, "test-model")
	handler := selectedAuthTestHandler(t, &selectedAuthExecuteExecutor{}, []internalconfig.ClientAPIKeyPolicy{{APIKey: "client-key", SelectedAuthIndex: auth.EnsureIndex()}}, auth)
	_, err := handler.applyClientAPIKeySelectedAuth(selectedAuthTestContext("client-key"), []string{"codex"}, "test-model")
	if err == nil {
		t.Fatalf("expected disabled selected auth to fail")
	}
	if err.Error() == "" {
		t.Fatalf("expected descriptive error, got empty string")
	}
}

func TestApplyClientAPIKeySelectedAuthRejectsMissingAuth(t *testing.T) {
	handler := selectedAuthTestHandler(t, &selectedAuthExecuteExecutor{}, []internalconfig.ClientAPIKeyPolicy{{APIKey: "client-key", SelectedAuthIndex: "missing-auth"}})
	_, err := handler.applyClientAPIKeySelectedAuth(selectedAuthTestContext("client-key"), []string{"codex"}, "test-model")
	if err == nil {
		t.Fatalf("expected missing selected auth to fail")
	}
}

func TestApplyClientAPIKeySelectedAuthRejectsMissingSelectedAuthIDEvenWithStaleIndex(t *testing.T) {
	auth := &coreauth.Auth{ID: "auth-1", Provider: "codex", Status: coreauth.StatusActive}
	registerSelectedAuthTestModel(t, auth, "test-model")
	handler := selectedAuthTestHandler(t, &selectedAuthExecuteExecutor{}, []internalconfig.ClientAPIKeyPolicy{{APIKey: "client-key", SelectedAuthID: "missing-id", SelectedAuthIndex: auth.EnsureIndex()}}, auth)
	_, err := handler.applyClientAPIKeySelectedAuth(selectedAuthTestContext("client-key"), []string{"codex"}, "test-model")
	if err == nil {
		t.Fatalf("expected missing selected auth id to fail")
	}
	if !strings.Contains(err.Error(), "selected upstream account ID was not found") {
		t.Fatalf("expected missing-id reason, got %v", err)
	}
	if !strings.Contains(err.Error(), "missing-id") {
		t.Fatalf("expected error to mention selected auth id, got %v", err)
	}
}

func TestApplyClientAPIKeySelectedAuthUsesIDWhenIndexIsStale(t *testing.T) {
	auth := &coreauth.Auth{ID: "auth-1", Provider: "codex", Status: coreauth.StatusActive}
	registerSelectedAuthTestModel(t, auth, "test-model")
	handler := selectedAuthTestHandler(t, &selectedAuthExecuteExecutor{}, []internalconfig.ClientAPIKeyPolicy{{APIKey: "client-key", SelectedAuthID: auth.ID, SelectedAuthIndex: "stale-index"}}, auth)
	ctx, err := handler.applyClientAPIKeySelectedAuth(selectedAuthTestContext("client-key"), []string{"codex"}, "test-model")
	if err != nil {
		t.Fatalf("applyClientAPIKeySelectedAuth() error = %v", err)
	}
	if got := pinnedAuthIDFromContext(ctx); got != auth.ID {
		t.Fatalf("pinned auth id = %q, want %q", got, auth.ID)
	}
}

func TestApplyClientAPIKeySelectedAuthRejectsUnsupportedModel(t *testing.T) {
	auth := &coreauth.Auth{ID: "auth-1", Provider: "codex", Status: coreauth.StatusActive}
	registerSelectedAuthTestModel(t, auth, "other-model")
	handler := selectedAuthTestHandler(t, &selectedAuthExecuteExecutor{}, []internalconfig.ClientAPIKeyPolicy{{APIKey: "client-key", SelectedAuthIndex: auth.EnsureIndex()}}, auth)
	_, err := handler.applyClientAPIKeySelectedAuth(selectedAuthTestContext("client-key"), []string{"codex"}, "test-model")
	if err == nil {
		t.Fatalf("expected unsupported selected auth to fail")
	}
}

func TestApplyClientAPIKeySelectedAuthAllowsProviderPrefixedModelMatch(t *testing.T) {
	auth := &coreauth.Auth{ID: "auth-1", Provider: "codex", Status: coreauth.StatusActive}
	registerSelectedAuthTestModel(t, auth, "openai/gpt-5.4")
	handler := selectedAuthTestHandler(t, &selectedAuthExecuteExecutor{}, []internalconfig.ClientAPIKeyPolicy{{APIKey: "client-key", SelectedAuthIndex: auth.EnsureIndex()}}, auth)
	ctx, err := handler.applyClientAPIKeySelectedAuth(selectedAuthTestContext("client-key"), []string{"openai"}, "gpt-5.4")
	if err != nil {
		t.Fatalf("applyClientAPIKeySelectedAuth() error = %v", err)
	}
	if got := pinnedAuthIDFromContext(ctx); got != auth.ID {
		t.Fatalf("pinned auth id = %q, want %q", got, auth.ID)
	}

	ctx, err = handler.applyClientAPIKeySelectedAuth(selectedAuthTestContext("client-key"), []string{"openai"}, "openai/gpt-5.4")
	if err != nil {
		t.Fatalf("applyClientAPIKeySelectedAuth() error = %v", err)
	}
	if got := pinnedAuthIDFromContext(ctx); got != auth.ID {
		t.Fatalf("pinned auth id = %q, want %q", got, auth.ID)
	}
}

func TestApplyClientAPIKeySelectedAuthAllowsModelPrefixMatch(t *testing.T) {
	auth := &coreauth.Auth{ID: "auth-1", Provider: "gemini", Status: coreauth.StatusActive}
	registerSelectedAuthTestModel(t, auth, "models/gemini-3-flash-preview")
	handler := selectedAuthTestHandler(t, &selectedAuthExecuteExecutor{}, []internalconfig.ClientAPIKeyPolicy{{APIKey: "client-key", SelectedAuthIndex: auth.EnsureIndex()}}, auth)
	ctx, err := handler.applyClientAPIKeySelectedAuth(selectedAuthTestContext("client-key"), []string{"gemini"}, "gemini-3-flash-preview")
	if err != nil {
		t.Fatalf("applyClientAPIKeySelectedAuth() error = %v", err)
	}
	if got := pinnedAuthIDFromContext(ctx); got != auth.ID {
		t.Fatalf("pinned auth id = %q, want %q", got, auth.ID)
	}
}

func TestApplyClientAPIKeySelectedAuthIgnoresExpiredUnavailableModelState(t *testing.T) {
	auth := &coreauth.Auth{
		ID:       "auth-1",
		Provider: "codex",
		Status:   coreauth.StatusActive,
		ModelStates: map[string]*coreauth.ModelState{
			"gpt-5.4-mini": {
				Unavailable:    true,
				NextRetryAfter: time.Now().Add(-1 * time.Minute),
			},
		},
	}
	registerSelectedAuthTestModel(t, auth, "gpt-5.4-mini")
	handler := selectedAuthTestHandler(t, &selectedAuthExecuteExecutor{}, []internalconfig.ClientAPIKeyPolicy{{APIKey: "client-key", SelectedAuthIndex: auth.EnsureIndex()}}, auth)
	ctx, err := handler.applyClientAPIKeySelectedAuth(selectedAuthTestContext("client-key"), []string{"codex"}, "gpt-5.4-mini")
	if err != nil {
		t.Fatalf("applyClientAPIKeySelectedAuth() error = %v", err)
	}
	if got := pinnedAuthIDFromContext(ctx); got != auth.ID {
		t.Fatalf("pinned auth id = %q, want %q", got, auth.ID)
	}
}

func TestApplyClientAPIKeySelectedAuthIgnoresActiveUnavailableModelState(t *testing.T) {
	auth := &coreauth.Auth{
		ID:       "auth-1",
		Provider: "codex",
		Status:   coreauth.StatusActive,
		ModelStates: map[string]*coreauth.ModelState{
			"gpt-5.4-mini": {
				Unavailable:    true,
				NextRetryAfter: time.Now().Add(1 * time.Minute),
			},
		},
	}
	registerSelectedAuthTestModel(t, auth, "gpt-5.4-mini")
	handler := selectedAuthTestHandler(t, &selectedAuthExecuteExecutor{}, []internalconfig.ClientAPIKeyPolicy{{APIKey: "client-key", SelectedAuthIndex: auth.EnsureIndex()}}, auth)
	ctx, err := handler.applyClientAPIKeySelectedAuth(selectedAuthTestContext("client-key"), []string{"codex"}, "gpt-5.4-mini")
	if err != nil {
		t.Fatalf("applyClientAPIKeySelectedAuth() error = %v", err)
	}
	if got := pinnedAuthIDFromContext(ctx); got != auth.ID {
		t.Fatalf("pinned auth id = %q, want %q", got, auth.ID)
	}
}

func TestExecuteWithAuthManagerUsesSelectedAuth(t *testing.T) {
	executor := &selectedAuthExecuteExecutor{}
	auth := &coreauth.Auth{ID: "auth-1", Provider: "codex", Status: coreauth.StatusActive}
	registerSelectedAuthTestModel(t, auth, "test-model")
	handler := selectedAuthTestHandler(t, executor, []internalconfig.ClientAPIKeyPolicy{{APIKey: "client-key", SelectedAuthID: auth.ID, SelectedAuthIndex: auth.EnsureIndex()}}, auth)
	payload, _, errMsg := handler.ExecuteWithAuthManager(selectedAuthTestContext("client-key"), "openai", "test-model", []byte(`{"model":"test-model"}`), "")
	if errMsg != nil {
		t.Fatalf("unexpected error: %+v", errMsg)
	}
	if string(payload) != `{"ok":true}` {
		t.Fatalf("payload = %s", string(payload))
	}
	if executor.executeAuthID != auth.ID {
		t.Fatalf("execute auth id = %q, want %q", executor.executeAuthID, auth.ID)
	}
}

func TestExecuteCountWithAuthManagerUsesSelectedAuth(t *testing.T) {
	executor := &selectedAuthCountExecutor{}
	auth := &coreauth.Auth{ID: "auth-1", Provider: "codex", Status: coreauth.StatusActive}
	registerSelectedAuthTestModel(t, auth, "test-model")
	handler := selectedAuthTestHandler(t, executor, []internalconfig.ClientAPIKeyPolicy{{APIKey: "client-key", SelectedAuthID: auth.ID, SelectedAuthIndex: auth.EnsureIndex()}}, auth)
	payload, _, errMsg := handler.ExecuteCountWithAuthManager(selectedAuthTestContext("client-key"), "openai", "test-model", []byte(`{"model":"test-model"}`), "")
	if errMsg != nil {
		t.Fatalf("unexpected error: %+v", errMsg)
	}
	if string(payload) != `{"total_tokens":1}` {
		t.Fatalf("payload = %s", string(payload))
	}
	if executor.countAuthID != auth.ID {
		t.Fatalf("count auth id = %q, want %q", executor.countAuthID, auth.ID)
	}
}

func TestExecuteStreamWithAuthManagerUsesSelectedAuth(t *testing.T) {
	executor := &selectedAuthStreamExecutor{}
	auth := &coreauth.Auth{ID: "auth-1", Provider: "codex", Status: coreauth.StatusActive}
	registerSelectedAuthTestModel(t, auth, "test-model")
	handler := selectedAuthTestHandler(t, executor, []internalconfig.ClientAPIKeyPolicy{{APIKey: "client-key", SelectedAuthID: auth.ID, SelectedAuthIndex: auth.EnsureIndex()}}, auth)
	dataChan, _, errChan := handler.ExecuteStreamWithAuthManager(selectedAuthTestContext("client-key"), "openai", "test-model", []byte(`{"model":"test-model"}`), "")
	if dataChan == nil || errChan == nil {
		t.Fatalf("expected non-nil channels")
	}
	var got []byte
	for chunk := range dataChan {
		got = append(got, chunk...)
	}
	for msg := range errChan {
		if msg != nil {
			t.Fatalf("unexpected stream error: %+v", msg)
		}
	}
	if string(got) != "ok" {
		t.Fatalf("payload = %q", string(got))
	}
	if executor.streamAuthID != auth.ID {
		t.Fatalf("stream auth id = %q, want %q", executor.streamAuthID, auth.ID)
	}
}

func TestExecuteWithAuthManagerFailsWhenSelectedAuthMissing(t *testing.T) {
	executor := &selectedAuthExecuteExecutor{}
	auth := &coreauth.Auth{ID: "auth-1", Provider: "codex", Status: coreauth.StatusActive}
	registerSelectedAuthTestModel(t, auth, "test-model")
	handler := selectedAuthTestHandler(t, executor, []internalconfig.ClientAPIKeyPolicy{{APIKey: "client-key", SelectedAuthIndex: "missing-auth"}}, auth)
	_, _, errMsg := handler.ExecuteWithAuthManager(selectedAuthTestContext("client-key"), "openai", "test-model", []byte(`{"model":"test-model"}`), "")
	if errMsg == nil {
		t.Fatalf("expected restriction error")
	}
	if errMsg.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", errMsg.StatusCode, http.StatusServiceUnavailable)
	}
	var authErr *coreauth.Error
	if !errorsAs(errMsg.Error, &authErr) || authErr == nil {
		t.Fatalf("expected coreauth.Error, got %T", errMsg.Error)
	}
	if authErr.Code != "selected_auth_unavailable" {
		t.Fatalf("code = %q, want %q", authErr.Code, "selected_auth_unavailable")
	}
	if executor.executeAuthID != "" {
		t.Fatalf("expected no upstream execution, got auth id %q", executor.executeAuthID)
	}
}

func errorsAs(err error, target **coreauth.Error) bool {
	if err == nil {
		return false
	}
	typed, ok := err.(*coreauth.Error)
	if !ok {
		return false
	}
	*target = typed
	return true
}
