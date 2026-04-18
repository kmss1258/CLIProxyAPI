package management

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	gin "github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/auth/codex"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/quota"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/usage"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	coreusage "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/usage"
)

func TestGetQuotaStatusReturnsQuotaUsageAndAuthFiles(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tmpDir := t.TempDir()
	authDir := filepath.Join(tmpDir, "auths")
	if err := os.MkdirAll(authDir, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	authPath := filepath.Join(authDir, "codex-test@example.com-plus.json")
	if err := os.WriteFile(authPath, []byte(`{"type":"codex","email":"test@example.com","disabled":false}`), 0o600); err != nil {
		t.Fatalf("WriteFile() auth error = %v", err)
	}
	cfg := &config.Config{SDKConfig: config.SDKConfig{
		APIKeys:              []string{"k1"},
		ClientAPIKeyPolicies: []config.ClientAPIKeyPolicy{{APIKey: "k1", Alias: "Notebook", OutputTokenQuota: 100, OutputTokenQuotaResetHours: 24}},
	}, AuthDir: authDir}
	stats := usage.NewRequestStatistics()
	recentUsageTime := time.Now().Add(-1 * time.Hour).UTC()
	stats.Record(nil, coreusage.Record{APIKey: "k1", Model: "gpt-5.4-mini", RequestedAt: recentUsageTime, AuthIndex: "auth-1", Source: "test@example.com", Detail: coreusage.Detail{TotalTokens: 12}})
	manager := quota.NewManager()
	managerTime := time.Date(2026, time.April, 17, 12, 0, 0, 0, time.UTC)
	manager.SetNowForTest(managerTime)
	cfg.SQLitePromptLog.Path = filepath.Join(tmpDir, "prompt_logs.sqlite")
	if err := manager.UpdateConfig(cfg, filepath.Join(tmpDir, "config.yaml")); err != nil {
		t.Fatalf("UpdateConfig() error = %v", err)
	}
	if err := manager.ApplyUsage("k1", 25); err != nil {
		t.Fatalf("ApplyUsage() error = %v", err)
	}
	h := NewHandlerWithoutConfigFilePath(cfg, nil)
	h.SetUsageStatistics(stats)
	h.SetQuotaManager(manager)
	req := httptest.NewRequest(http.MethodGet, "/v0/management/quota-status", nil)
	rr := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rr)
	c.Request = req
	h.GetQuotaStatus(c)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	var payload struct {
		Usage struct {
			APIs map[string]any `json:"apis"`
		} `json:"usage"`
		ClientAPIKeyQuotas []quota.Status    `json:"client_api_key_quotas"`
		APIKeyAliases      map[string]string `json:"api_key_aliases"`
		AuthFiles          []map[string]any  `json:"auth_files"`
		AuthUsage          []map[string]any  `json:"auth_usage"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v body=%s", err, rr.Body.String())
	}
	if len(payload.ClientAPIKeyQuotas) != 1 {
		t.Fatalf("expected one quota status, got %#v", payload.ClientAPIKeyQuotas)
	}
	if payload.ClientAPIKeyQuotas[0].WindowStartedAt == nil || payload.ClientAPIKeyQuotas[0].WindowEndsAt == nil {
		t.Fatalf("expected quota window timestamps, got %#v", payload.ClientAPIKeyQuotas[0])
	}
	if got := payload.ClientAPIKeyQuotas[0].Alias; got != "Notebook" {
		t.Fatalf("expected quota alias, got %#v", payload.ClientAPIKeyQuotas[0])
	}
	if got := payload.APIKeyAliases["k1"]; got != "Notebook" {
		t.Fatalf("expected api_key_aliases entry, got %#v", payload.APIKeyAliases)
	}
	if payload.ClientAPIKeyQuotas[0].WindowRemainingSeconds != 24*60*60 {
		t.Fatalf("expected one day of remaining window, got %#v", payload.ClientAPIKeyQuotas[0])
	}
	if len(payload.AuthFiles) != 1 {
		t.Fatalf("expected one auth file, got %#v", payload.AuthFiles)
	}
	if payload.AuthFiles[0]["email"] != "test@example.com" {
		t.Fatalf("expected auth email, got %#v", payload.AuthFiles[0])
	}
	if len(payload.AuthUsage) != 1 {
		t.Fatalf("expected one auth usage row, got %#v", payload.AuthUsage)
	}
	if got := payload.AuthUsage[0]["auth_index"]; got != "auth-1" {
		t.Fatalf("expected auth usage auth_index, got %#v", payload.AuthUsage[0])
	}
	if got := payload.AuthUsage[0]["requests_24h"]; got != float64(1) {
		t.Fatalf("expected requests_24h=1, got %#v", payload.AuthUsage[0])
	}
	if len(payload.Usage.APIs) != 1 {
		t.Fatalf("expected one usage api entry, got %#v", payload.Usage.APIs)
	}
}

func TestGetQuotaStatusEnrichesAuthFilesWithOpenAIQuota(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tmpDir := t.TempDir()
	authPath := filepath.Join(tmpDir, "codex-test@example.com-pro.json")
	if err := os.WriteFile(authPath, []byte(`{"type":"codex"}`), 0o600); err != nil {
		t.Fatalf("WriteFile() auth error = %v", err)
	}
	manager := coreauth.NewManager(nil, nil, nil)
	auth := &coreauth.Auth{
		ID:       "auth-1",
		Provider: "codex",
		FileName: "codex-test@example.com-pro.json",
		Status:   coreauth.StatusActive,
		Attributes: map[string]string{
			"path": authPath,
		},
		Metadata: map[string]any{
			"email":        "test@example.com",
			"access_token": codexTestJWTToken("acc-123"),
			"account_id":   "acc-123",
			"expired":      time.Now().Add(time.Hour).Format(time.RFC3339),
		},
	}
	if _, err := manager.Register(context.Background(), auth); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	originalFactory := newCodexAuth
	defer func() { newCodexAuth = originalFactory }()
	newCodexAuth = func(cfg *config.Config) *codex.CodexAuth {
		return codex.NewCodexAuthWithHTTPClient(&http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"rate_limit":{"primary_window":{"used_percent":12,"reset_at":1713312000000},"secondary_window":{"used_percent":80,"reset_after_seconds":3600}}}`)),
				Header:     make(http.Header),
				Request:    req,
			}, nil
		})})
	}

	h := NewHandlerWithoutConfigFilePath(&config.Config{}, manager)
	req := httptest.NewRequest(http.MethodGet, "/v0/management/quota-status", nil)
	rr := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rr)
	c.Request = req
	h.GetQuotaStatus(c)
	if !waitForCondition(500*time.Millisecond, func() bool {
		h.openAIQuotaMu.Lock()
		defer h.openAIQuotaMu.Unlock()
		for _, entry := range h.openAIQuotaCache {
			if entry != nil && entry.snapshot != nil {
				return true
			}
		}
		return false
	}) {
		t.Fatalf("expected background quota cache to populate")
	}
	rr = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(rr)
	c.Request = httptest.NewRequest(http.MethodGet, "/v0/management/quota-status", nil)
	h.GetQuotaStatus(c)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}

	var payload struct {
		AuthFiles []struct {
			Email       string                    `json:"email"`
			OpenAIQuota *codex.UsageQuotaSnapshot `json:"openai_quota"`
		} `json:"auth_files"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v body=%s", err, rr.Body.String())
	}
	if len(payload.AuthFiles) != 1 {
		t.Fatalf("expected one auth file, got %#v", payload.AuthFiles)
	}
	if payload.AuthFiles[0].Email != "test@example.com" {
		t.Fatalf("expected auth email, got %#v", payload.AuthFiles[0])
	}
	quota := payload.AuthFiles[0].OpenAIQuota
	if quota == nil {
		t.Fatalf("expected openai_quota, got %#v", payload.AuthFiles[0])
	}
	if quota.Daily == nil || quota.Daily.PercentRemaining != 88 {
		t.Fatalf("expected daily percent remaining 88, got %#v", quota)
	}
	if quota.Weekly == nil || quota.Weekly.PercentRemaining != 20 {
		t.Fatalf("expected weekly percent remaining 20, got %#v", quota)
	}
	if quota.Daily.ResetTimeISO != "2024-04-17T00:00:00Z" {
		t.Fatalf("expected millisecond reset_at conversion, got %#v", quota.Daily)
	}
}

func TestGetQuotaStatusJoinsAuthFilesAndUsageByAuthIndex(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tmpDir := t.TempDir()
	authPathOne := filepath.Join(tmpDir, "codex-alpha@example.com-pro.json")
	authPathTwo := filepath.Join(tmpDir, "codex-beta@example.com-pro.json")
	if err := os.WriteFile(authPathOne, []byte(`{"type":"codex"}`), 0o600); err != nil {
		t.Fatalf("WriteFile() auth one error = %v", err)
	}
	if err := os.WriteFile(authPathTwo, []byte(`{"type":"codex"}`), 0o600); err != nil {
		t.Fatalf("WriteFile() auth two error = %v", err)
	}
	manager := coreauth.NewManager(nil, nil, nil)
	authOne := &coreauth.Auth{
		ID:       "id-auth-1",
		Index:    "auth-1",
		Provider: "codex",
		FileName: "codex-alpha@example.com-pro.json",
		Status:   coreauth.StatusActive,
		Attributes: map[string]string{
			"path": authPathOne,
		},
		Metadata: map[string]any{
			"email":        "alpha@example.com",
			"access_token": codexTestJWTToken("acc-1"),
			"account_id":   "acc-1",
			"expired":      time.Now().Add(time.Hour).Format(time.RFC3339),
		},
	}
	authTwo := &coreauth.Auth{
		ID:       "id-auth-2",
		Index:    "auth-2",
		Provider: "codex",
		FileName: "codex-beta@example.com-pro.json",
		Status:   coreauth.StatusActive,
		Attributes: map[string]string{
			"path": authPathTwo,
		},
		Metadata: map[string]any{
			"email":        "beta@example.com",
			"access_token": codexTestJWTToken("acc-2"),
			"account_id":   "acc-2",
			"expired":      time.Now().Add(time.Hour).Format(time.RFC3339),
		},
	}
	if _, err := manager.Register(context.Background(), authOne); err != nil {
		t.Fatalf("Register() auth one error = %v", err)
	}
	if _, err := manager.Register(context.Background(), authTwo); err != nil {
		t.Fatalf("Register() auth two error = %v", err)
	}
	authIndexes := map[string]string{}
	for _, item := range manager.List() {
		if item == nil {
			continue
		}
		authIndexes[item.ID] = item.EnsureIndex()
	}
	authOneIndex := authIndexes["id-auth-1"]
	authTwoIndex := authIndexes["id-auth-2"]
	if authOneIndex == "" || authTwoIndex == "" {
		t.Fatalf("expected manager-assigned auth indexes, got %#v", authIndexes)
	}

	stats := usage.NewRequestStatistics()
	stats.Record(nil, coreusage.Record{APIKey: "k1", Model: "gpt-5.4-mini", RequestedAt: time.Now().Add(-30 * time.Minute).UTC(), AuthIndex: authTwoIndex, Source: "beta@example.com", Detail: coreusage.Detail{TotalTokens: 17}})

	originalFactory := newCodexAuth
	defer func() { newCodexAuth = originalFactory }()
	newCodexAuth = func(cfg *config.Config) *codex.CodexAuth {
		return codex.NewCodexAuthWithHTTPClient(&http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			accountHeader := req.Header.Get("ChatGPT-Account-Id")
			percent := 10
			if accountHeader == "acc-2" {
				percent = 25
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"rate_limit":{"primary_window":{"used_percent":` + strconv.Itoa(percent) + `}}}`)),
				Header:     make(http.Header),
				Request:    req,
			}, nil
		})})
	}

	h := NewHandlerWithoutConfigFilePath(&config.Config{}, manager)
	h.SetUsageStatistics(stats)
	rr := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rr)
	c.Request = httptest.NewRequest(http.MethodGet, "/v0/management/quota-status", nil)
	h.GetQuotaStatus(c)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}

	var payload struct {
		AuthFiles []map[string]any `json:"auth_files"`
		AuthUsage []map[string]any `json:"auth_usage"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v body=%s", err, rr.Body.String())
	}
	if len(payload.AuthFiles) != 2 {
		t.Fatalf("expected two auth files, got %#v", payload.AuthFiles)
	}
	if len(payload.AuthUsage) != 1 {
		t.Fatalf("expected one auth usage row, got %#v", payload.AuthUsage)
	}

	byIndex := map[string]map[string]any{}
	for _, entry := range payload.AuthFiles {
		idx, _ := entry["auth_index"].(string)
		byIndex[idx] = entry
	}
	if _, ok := byIndex[authOneIndex]; !ok {
		t.Fatalf("expected auth-1 entry, got %#v", byIndex)
	}
	if _, ok := byIndex[authTwoIndex]; !ok {
		t.Fatalf("expected auth-2 entry, got %#v", byIndex)
	}
	if got := payload.AuthUsage[0]["auth_index"]; got != authTwoIndex {
		t.Fatalf("expected auth_usage row for auth-2, got %#v", payload.AuthUsage[0])
	}
	if got := byIndex[authTwoIndex]["requests_24h"]; got != float64(1) {
		t.Fatalf("expected auth-2 requests_24h=1, got %#v", byIndex[authTwoIndex])
	}
	if _, exists := byIndex[authOneIndex]["requests_24h"]; exists {
		t.Fatalf("expected auth-1 to remain free of auth-2 usage fields, got %#v", byIndex[authOneIndex])
	}
	authTwoQuota, ok := byIndex[authTwoIndex]["openai_quota"].(map[string]any)
	if !ok {
		t.Fatalf("expected auth-2 openai_quota payload, got %#v", byIndex[authTwoIndex])
	}
	daily, ok := authTwoQuota["daily"].(map[string]any)
	if !ok || daily["percent_remaining"] != float64(75) {
		t.Fatalf("expected auth-2 quota to reflect acc-2 response, got %#v", authTwoQuota)
	}
}

func TestGetQuotaStatusColdMissFetchesQuotaImmediately(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tmpDir := t.TempDir()
	authPath := filepath.Join(tmpDir, "codex-test@example.com-pro.json")
	if err := os.WriteFile(authPath, []byte(`{"type":"codex"}`), 0o600); err != nil {
		t.Fatalf("WriteFile() auth error = %v", err)
	}
	manager := coreauth.NewManager(nil, nil, nil)
	auth := &coreauth.Auth{
		ID:       "auth-1",
		Provider: "codex",
		FileName: "codex-test@example.com-pro.json",
		Status:   coreauth.StatusActive,
		Attributes: map[string]string{
			"path": authPath,
		},
		Metadata: map[string]any{
			"email":        "test@example.com",
			"access_token": codexTestJWTToken("acc-123"),
			"account_id":   "acc-123",
			"expired":      time.Now().Add(time.Hour).Format(time.RFC3339),
		},
	}
	if _, err := manager.Register(context.Background(), auth); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	var calls int32
	originalFactory := newCodexAuth
	defer func() { newCodexAuth = originalFactory }()
	newCodexAuth = func(cfg *config.Config) *codex.CodexAuth {
		return codex.NewCodexAuthWithHTTPClient(&http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			atomic.AddInt32(&calls, 1)
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"rate_limit":{"primary_window":{"used_percent":25},"secondary_window":{"used_percent":50}}}`)),
				Header:     make(http.Header),
				Request:    req,
			}, nil
		})})
	}

	h := NewHandlerWithoutConfigFilePath(&config.Config{}, manager)
	rr := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rr)
	c.Request = httptest.NewRequest(http.MethodGet, "/v0/management/quota-status", nil)
	h.GetQuotaStatus(c)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	var firstPayload struct {
		AuthFiles []struct {
			OpenAIQuota *codex.UsageQuotaSnapshot `json:"openai_quota"`
		} `json:"auth_files"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &firstPayload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v body=%s", err, rr.Body.String())
	}
	if len(firstPayload.AuthFiles) != 1 {
		t.Fatalf("expected one auth file, got %#v", firstPayload.AuthFiles)
	}
	if firstPayload.AuthFiles[0].OpenAIQuota == nil || firstPayload.AuthFiles[0].OpenAIQuota.Daily == nil {
		t.Fatalf("expected cold miss response with immediate quota snapshot, got %#v", firstPayload.AuthFiles)
	}
	if !waitForCondition(200*time.Millisecond, func() bool {
		h.openAIQuotaMu.Lock()
		defer h.openAIQuotaMu.Unlock()
		for _, entry := range h.openAIQuotaCache {
			if entry != nil && entry.snapshot != nil && entry.snapshot.Daily != nil {
				return true
			}
		}
		return false
	}) {
		t.Fatalf("expected cache population after immediate fetch")
	}
	rr = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(rr)
	c.Request = httptest.NewRequest(http.MethodGet, "/v0/management/quota-status", nil)
	h.GetQuotaStatus(c)
	var secondPayload struct {
		AuthFiles []struct {
			OpenAIQuota *codex.UsageQuotaSnapshot `json:"openai_quota"`
		} `json:"auth_files"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &secondPayload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v body=%s", err, rr.Body.String())
	}
	if secondPayload.AuthFiles[0].OpenAIQuota == nil || secondPayload.AuthFiles[0].OpenAIQuota.Daily == nil {
		t.Fatalf("expected cached quota snapshot, got %#v", secondPayload.AuthFiles)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("expected one synchronous cold-miss fetch, got %d", got)
	}
}

func TestGetQuotaStatusUsesFreshOpenAIQuotaCache(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tmpDir := t.TempDir()
	authPath := filepath.Join(tmpDir, "codex-test@example.com-pro.json")
	if err := os.WriteFile(authPath, []byte(`{"type":"codex"}`), 0o600); err != nil {
		t.Fatalf("WriteFile() auth error = %v", err)
	}
	manager := coreauth.NewManager(nil, nil, nil)
	auth := &coreauth.Auth{
		ID:       "auth-1",
		Provider: "codex",
		FileName: "codex-test@example.com-pro.json",
		Status:   coreauth.StatusActive,
		Attributes: map[string]string{
			"path": authPath,
		},
		Metadata: map[string]any{
			"email":        "test@example.com",
			"access_token": codexTestJWTToken("acc-123"),
			"account_id":   "acc-123",
			"expired":      time.Now().Add(time.Hour).Format(time.RFC3339),
		},
	}
	if _, err := manager.Register(context.Background(), auth); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	var calls int32
	originalFactory := newCodexAuth
	defer func() { newCodexAuth = originalFactory }()
	newCodexAuth = func(cfg *config.Config) *codex.CodexAuth {
		return codex.NewCodexAuthWithHTTPClient(&http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			atomic.AddInt32(&calls, 1)
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"rate_limit":{"primary_window":{"used_percent":10},"secondary_window":{"used_percent":20}}}`)),
				Header:     make(http.Header),
				Request:    req,
			}, nil
		})})
	}

	h := NewHandlerWithoutConfigFilePath(&config.Config{}, manager)
	if key := h.openAIQuotaCacheKey(auth); key == "" {
		t.Fatalf("expected cache key")
	} else {
		h.openAIQuotaMu.Lock()
		h.openAIQuotaCache[key] = &cachedOpenAIQuota{
			snapshot: &codex.UsageQuotaSnapshot{
				Provider:  "openai",
				AccountID: "acc-123",
				Daily:     &codex.UsageQuotaWindow{PercentRemaining: 90},
				Weekly:    &codex.UsageQuotaWindow{PercentRemaining: 80},
			},
			fetchedAt: time.Now(),
			expiresAt: time.Now().Add(time.Minute),
		}
		h.openAIQuotaMu.Unlock()
	}
	rr := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rr)
	c.Request = httptest.NewRequest(http.MethodGet, "/v0/management/quota-status", nil)
	h.GetQuotaStatus(c)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	if got := atomic.LoadInt32(&calls); got != 0 {
		t.Fatalf("expected fresh cache to avoid HTTP fetch, got %d calls", got)
	}
}

func TestGetQuotaStatusCachesOpenAIQuotaErrorsBriefly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tmpDir := t.TempDir()
	authPath := filepath.Join(tmpDir, "codex-test@example.com-pro.json")
	if err := os.WriteFile(authPath, []byte(`{"type":"codex"}`), 0o600); err != nil {
		t.Fatalf("WriteFile() auth error = %v", err)
	}
	manager := coreauth.NewManager(nil, nil, nil)
	auth := &coreauth.Auth{
		ID:       "auth-1",
		Provider: "codex",
		FileName: "codex-test@example.com-pro.json",
		Status:   coreauth.StatusActive,
		Attributes: map[string]string{
			"path": authPath,
		},
		Metadata: map[string]any{
			"email":        "test@example.com",
			"access_token": codexTestJWTToken("acc-123"),
			"account_id":   "acc-123",
			"expired":      time.Now().Add(time.Hour).Format(time.RFC3339),
		},
	}
	if _, err := manager.Register(context.Background(), auth); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	var calls int32
	originalFactory := newCodexAuth
	defer func() { newCodexAuth = originalFactory }()
	newCodexAuth = func(cfg *config.Config) *codex.CodexAuth {
		return codex.NewCodexAuthWithHTTPClient(&http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			atomic.AddInt32(&calls, 1)
			return nil, context.DeadlineExceeded
		})})
	}

	h := NewHandlerWithoutConfigFilePath(&config.Config{}, manager)
	rr := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rr)
	c.Request = httptest.NewRequest(http.MethodGet, "/v0/management/quota-status", nil)
	h.GetQuotaStatus(c)
	if !waitForCondition(500*time.Millisecond, func() bool {
		h.openAIQuotaMu.Lock()
		defer h.openAIQuotaMu.Unlock()
		for _, entry := range h.openAIQuotaCache {
			if entry != nil && entry.snapshot != nil && entry.snapshot.Error != "" {
				return true
			}
		}
		return false
	}) {
		t.Fatalf("expected cached error snapshot")
	}
	rr = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(rr)
	c.Request = httptest.NewRequest(http.MethodGet, "/v0/management/quota-status", nil)
	h.GetQuotaStatus(c)
	var payload struct {
		AuthFiles []struct {
			OpenAIQuota *codex.UsageQuotaSnapshot `json:"openai_quota"`
		} `json:"auth_files"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v body=%s", err, rr.Body.String())
	}
	if len(payload.AuthFiles) != 1 || payload.AuthFiles[0].OpenAIQuota == nil || payload.AuthFiles[0].OpenAIQuota.Error == "" {
		t.Fatalf("expected cached error quota, got %#v", payload.AuthFiles)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("expected cached error to avoid second fetch, got %d", got)
	}
}

func TestGetQuotaStatusReturnsMissingAccessTokenQuotaError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tmpDir := t.TempDir()
	authPath := filepath.Join(tmpDir, "codex-test@example.com-pro.json")
	if err := os.WriteFile(authPath, []byte(`{"type":"codex"}`), 0o600); err != nil {
		t.Fatalf("WriteFile() auth error = %v", err)
	}
	manager := coreauth.NewManager(nil, nil, nil)
	auth := &coreauth.Auth{
		ID:       "auth-1",
		Provider: "codex",
		FileName: "codex-test@example.com-pro.json",
		Status:   coreauth.StatusActive,
		Attributes: map[string]string{
			"path": authPath,
		},
		Metadata: map[string]any{
			"email": "test@example.com",
		},
	}
	if _, err := manager.Register(context.Background(), auth); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	h := NewHandlerWithoutConfigFilePath(&config.Config{}, manager)
	rr := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rr)
	c.Request = httptest.NewRequest(http.MethodGet, "/v0/management/quota-status", nil)
	h.GetQuotaStatus(c)
	var payload struct {
		AuthFiles []struct {
			OpenAIQuota *codex.UsageQuotaSnapshot `json:"openai_quota"`
		} `json:"auth_files"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v body=%s", err, rr.Body.String())
	}
	if len(payload.AuthFiles) != 1 || payload.AuthFiles[0].OpenAIQuota == nil {
		t.Fatalf("expected openai_quota missing-token error, got %#v", payload.AuthFiles)
	}
	if payload.AuthFiles[0].OpenAIQuota.Error != "Missing access token" {
		t.Fatalf("expected missing access token error, got %#v", payload.AuthFiles[0].OpenAIQuota)
	}
}

func TestRefreshOpenAIQuotaClearsRefreshingWhenConfigMissing(t *testing.T) {
	h := NewHandlerWithoutConfigFilePath(&config.Config{}, nil)
	h.openAIQuotaCache["auth-1:test"] = &cachedOpenAIQuota{refreshing: true}
	h.SetConfig(nil)
	h.refreshOpenAIQuota("auth-1:test", &coreauth.Auth{ID: "auth-1", Provider: "codex"})
	h.openAIQuotaMu.Lock()
	defer h.openAIQuotaMu.Unlock()
	if h.openAIQuotaCache["auth-1:test"].refreshing {
		t.Fatalf("expected refreshing flag to clear when config missing")
	}
}

func TestStoreOpenAIQuotaCacheEntryPrunesSupersededKeys(t *testing.T) {
	h := NewHandlerWithoutConfigFilePath(&config.Config{}, nil)
	auth := &coreauth.Auth{ID: "auth-1", Provider: "codex"}
	prefix := h.openAIQuotaCachePrefix(auth)
	oldKey := prefix + "old"
	newKey := prefix + "new"
	h.openAIQuotaCache[oldKey] = &cachedOpenAIQuota{snapshot: &codex.UsageQuotaSnapshot{Provider: "openai"}}
	h.storeOpenAIQuotaCacheEntry(newKey, auth, &codex.UsageQuotaSnapshot{Provider: "openai"}, true)
	h.openAIQuotaMu.Lock()
	defer h.openAIQuotaMu.Unlock()
	if _, ok := h.openAIQuotaCache[oldKey]; ok {
		t.Fatalf("expected old cache key to be pruned")
	}
	if _, ok := h.openAIQuotaCache[newKey]; !ok {
		t.Fatalf("expected new cache key to remain")
	}
}

func TestRefreshOpenAIQuotaDiscardsSupersededOldKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tmpDir := t.TempDir()
	authPath := filepath.Join(tmpDir, "codex-test@example.com-pro.json")
	if err := os.WriteFile(authPath, []byte(`{"type":"codex"}`), 0o600); err != nil {
		t.Fatalf("WriteFile() auth error = %v", err)
	}
	manager := coreauth.NewManager(nil, nil, nil)
	auth := &coreauth.Auth{
		ID:       "auth-1",
		Provider: "codex",
		FileName: "codex-test@example.com-pro.json",
		Status:   coreauth.StatusActive,
		Attributes: map[string]string{
			"path": authPath,
		},
		Metadata: map[string]any{
			"email":        "test@example.com",
			"access_token": "token-new",
			"account_id":   "acc-123",
			"expired":      time.Now().Add(time.Hour).Format(time.RFC3339),
		},
	}
	if _, err := manager.Register(context.Background(), auth); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	h := NewHandlerWithoutConfigFilePath(&config.Config{}, manager)
	oldAuth := auth.Clone()
	oldAuth.Metadata["access_token"] = "token-old"
	oldKey := h.openAIQuotaCacheKey(oldAuth)
	newKey := h.openAIQuotaCacheKey(auth)
	h.openAIQuotaCache[oldKey] = &cachedOpenAIQuota{refreshing: true}
	h.openAIQuotaCache[newKey] = &cachedOpenAIQuota{
		snapshot:  &codex.UsageQuotaSnapshot{Provider: "openai", Daily: &codex.UsageQuotaWindow{PercentRemaining: 80}},
		fetchedAt: time.Now(),
		expiresAt: time.Now().Add(time.Minute),
	}
	originalFactory := newCodexAuth
	defer func() { newCodexAuth = originalFactory }()
	newCodexAuth = func(cfg *config.Config) *codex.CodexAuth {
		return codex.NewCodexAuthWithHTTPClient(&http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"rate_limit":{"primary_window":{"used_percent":10}}}`)),
				Header:     make(http.Header),
				Request:    req,
			}, nil
		})})
	}
	h.refreshOpenAIQuota(oldKey, oldAuth)
	h.openAIQuotaMu.Lock()
	defer h.openAIQuotaMu.Unlock()
	if _, ok := h.openAIQuotaCache[oldKey]; ok {
		t.Fatalf("expected old key to be discarded after superseded refresh completion")
	}
	entry, ok := h.openAIQuotaCache[newKey]
	if !ok || entry == nil || entry.snapshot == nil || entry.snapshot.Daily == nil || entry.snapshot.Daily.PercentRemaining != 80 {
		t.Fatalf("expected new key cache entry to remain authoritative, got %#v", h.openAIQuotaCache[newKey])
	}
}

func TestGetQuotaStatusFallsBackToSQLiteRequestLogsWhenUsageStatsEmpty(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	dbPath := filepath.Join(tmpDir, "prompt_logs.sqlite")
	authDir := filepath.Join(tmpDir, "auths")
	if err := os.MkdirAll(authDir, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	cfg := &config.Config{SDKConfig: config.SDKConfig{APIKeys: []string{"k1"}}, AuthDir: authDir}
	cfg.SQLitePromptLog.Path = dbPath
	store, err := quota.OpenSQLiteStore(dbPath, 0)
	if err != nil {
		t.Fatalf("OpenSQLiteStore() error = %v", err)
	}
	now := time.Now().UTC()
	if err := store.InsertRequestLog(quota.RequestLogEntry{
		RequestID:        "req-1",
		URL:              "/v1/chat/completions",
		Method:           http.MethodPost,
		APIKeyHash:       quota.RequestLogAPIKeyIdentifiers("k1")[0],
		Provider:         "openai",
		Model:            "gpt-5.4-mini",
		StatusCode:       http.StatusOK,
		LatencyMS:        321,
		InputTokens:      11,
		OutputTokens:     22,
		ReasoningTokens:  3,
		RequestTimestamp: now,
	}); err != nil {
		t.Fatalf("InsertRequestLog() error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	manager := quota.NewManager()
	if err := manager.UpdateConfig(cfg, configPath); err != nil {
		t.Fatalf("UpdateConfig() error = %v", err)
	}
	h := NewHandlerWithoutConfigFilePath(cfg, nil)
	h.SetUsageStatistics(usage.NewRequestStatistics())
	h.SetQuotaManager(manager)
	req := httptest.NewRequest(http.MethodGet, "/v0/management/quota-status", nil)
	rr := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rr)
	c.Request = req
	h.GetQuotaStatus(c)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	var payload struct {
		Usage usage.StatisticsSnapshot `json:"usage"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v body=%s", err, rr.Body.String())
	}
	apiSnapshot, ok := payload.Usage.APIs["k1"]
	if !ok {
		t.Fatalf("expected sqlite fallback API snapshot, got %#v", payload.Usage.APIs)
	}
	if apiSnapshot.TotalRequests != 1 || apiSnapshot.TotalTokens != 36 {
		t.Fatalf("expected sqlite totals from request_logs, got %#v", apiSnapshot)
	}
	modelSnapshot, ok := apiSnapshot.Models["gpt-5.4-mini"]
	if !ok || len(modelSnapshot.Details) != 1 {
		t.Fatalf("expected sqlite model detail, got %#v", apiSnapshot.Models)
	}
	if modelSnapshot.Details[0].Tokens.OutputTokens != 22 {
		t.Fatalf("expected output tokens from sqlite request log, got %#v", modelSnapshot.Details[0])
	}
}

func TestGetQuotaStatusViewerScopesToProvidedAPIKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{SDKConfig: config.SDKConfig{
		APIKeys: []string{"k1", "k2"},
		ClientAPIKeyPolicies: []config.ClientAPIKeyPolicy{
			{APIKey: "k1", Alias: "Key One", OutputTokenQuota: 100, OutputTokenQuotaResetHours: 24},
			{APIKey: "k2", Alias: "Key Two", OutputTokenQuota: 200, OutputTokenQuotaResetHours: 24},
		},
	}}
	stats := usage.NewRequestStatistics()
	stats.Record(nil, coreusage.Record{APIKey: "k1", Model: "gpt-5.4-mini", RequestedAt: time.Now().UTC(), Detail: coreusage.Detail{InputTokens: 1, OutputTokens: 2, TotalTokens: 3}})
	stats.Record(nil, coreusage.Record{APIKey: "k2", Model: "gpt-5.4-mini", RequestedAt: time.Now().UTC(), Detail: coreusage.Detail{InputTokens: 4, OutputTokens: 5, TotalTokens: 9}})
	manager := quota.NewManager()
	if err := manager.UpdateConfig(cfg, filepath.Join(t.TempDir(), "config.yaml")); err != nil {
		t.Fatalf("UpdateConfig() error = %v", err)
	}
	h := NewHandlerWithoutConfigFilePath(cfg, nil)
	h.SetUsageStatistics(stats)
	h.SetQuotaManager(manager)
	req := httptest.NewRequest(http.MethodGet, "/v0/quota-status", nil)
	req.Header.Set("Authorization", "Bearer k2")
	rr := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rr)
	c.Request = req
	h.GetQuotaStatusViewer(c)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	var payload struct {
		Usage struct {
			APIs map[string]usage.APISnapshot `json:"apis"`
		} `json:"usage"`
		ClientAPIKeyQuotas []quota.Status    `json:"client_api_key_quotas"`
		APIKeyAliases      map[string]string `json:"api_key_aliases"`
		AuthFiles          []map[string]any  `json:"auth_files"`
		Viewer             struct {
			CanManage bool   `json:"can_manage"`
			Scope     string `json:"scope"`
			APIKey    string `json:"api_key"`
		} `json:"viewer"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v body=%s", err, rr.Body.String())
	}
	if payload.Viewer.CanManage || payload.Viewer.Scope != "self" || payload.Viewer.APIKey != "k2" {
		t.Fatalf("expected self viewer for k2, got %#v", payload.Viewer)
	}
	if len(payload.Usage.APIs) != 1 || payload.Usage.APIs["k2"].TotalTokens != 9 {
		t.Fatalf("expected only k2 usage, got %#v", payload.Usage.APIs)
	}
	if len(payload.ClientAPIKeyQuotas) != 1 || payload.ClientAPIKeyQuotas[0].APIKey != "k2" {
		t.Fatalf("expected only k2 quota, got %#v", payload.ClientAPIKeyQuotas)
	}
	if len(payload.AuthFiles) != 0 {
		t.Fatalf("expected no auth files in self view, got %#v", payload.AuthFiles)
	}
	if len(payload.APIKeyAliases) != 1 || payload.APIKeyAliases["k2"] != "Key Two" {
		t.Fatalf("expected only k2 alias, got %#v", payload.APIKeyAliases)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func codexTestJWTToken(accountID string) string {
	return "eyJhbGciOiJub25lIiwidHlwIjoiSldUIn0." +
		"eyJodHRwczovL2FwaS5vcGVuYWkuY29tL2F1dGgiOnsiY2hhdGdwdF9hY2NvdW50X2lkIjoi" +
		accountID +
		"In19.sig"
}

func waitForCondition(timeout time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return cond()
}
