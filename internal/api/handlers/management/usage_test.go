package management

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/quota"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/usage"
	coreusage "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/usage"
)

func TestGetUsageStatisticsFallsBackToSQLiteRequestLogsWhenUsageStatsEmpty(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	dbPath := filepath.Join(tmpDir, "prompt_logs.sqlite")
	cfg := &config.Config{SDKConfig: config.SDKConfig{APIKeys: []string{"k1"}}}
	cfg.SQLitePromptLog.Path = dbPath
	store, err := quota.OpenSQLiteStore(dbPath, 0)
	if err != nil {
		t.Fatalf("OpenSQLiteStore() error = %v", err)
	}
	now := time.Now().UTC()
	if err := store.InsertRequestLog(quota.RequestLogEntry{
		RequestID:        "req-usage-1",
		URL:              "/v1/chat/completions",
		Method:           http.MethodPost,
		APIKeyHash:       quota.RequestLogAPIKeyIdentifiers("k1")[1],
		Provider:         "openai",
		Model:            "gpt-5.4-mini",
		StatusCode:       http.StatusOK,
		LatencyMS:        100,
		InputTokens:      7,
		OutputTokens:     9,
		ReasoningTokens:  1,
		RequestTimestamp: now,
	}); err != nil {
		t.Fatalf("InsertRequestLog() error = %v", err)
	}
	directSnapshot, err := store.LoadRequestLogUsageSnapshot([]string{"k1"})
	if err != nil {
		t.Fatalf("LoadRequestLogUsageSnapshot() error = %v", err)
	}
	if got := directSnapshot.APIs["k1"].TotalTokens; got != 17 {
		t.Fatalf("expected direct sqlite snapshot total tokens 17, got %#v", directSnapshot.APIs)
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
	req := httptest.NewRequest(http.MethodGet, "/v0/management/usage", nil)
	rr := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rr)
	c.Request = req
	h.GetUsageStatistics(c)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	var payload struct {
		Usage usage.StatisticsSnapshot `json:"usage"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v body=%s", err, rr.Body.String())
	}
	if got := payload.Usage.APIs["k1"].TotalTokens; got != 17 {
		t.Fatalf("expected sqlite fallback total tokens 17, got %#v", payload.Usage.APIs)
	}
}

func TestGetUsageStatisticsHydratesMemoryFromSQLiteFallback(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	dbPath := filepath.Join(tmpDir, "prompt_logs.sqlite")
	cfg := &config.Config{SDKConfig: config.SDKConfig{APIKeys: []string{"k1"}}}
	cfg.SQLitePromptLog.Path = dbPath
	store, err := quota.OpenSQLiteStore(dbPath, 0)
	if err != nil {
		t.Fatalf("OpenSQLiteStore() error = %v", err)
	}
	now := time.Now().UTC()
	if err := store.InsertRequestLog(quota.RequestLogEntry{
		RequestID:        "req-hydrate-1",
		URL:              "/v1/chat/completions",
		Method:           http.MethodPost,
		APIKeyHash:       quota.RequestLogAPIKeyIdentifiers("k1")[1],
		Provider:         "openai",
		Model:            "gpt-5.4-mini",
		StatusCode:       http.StatusOK,
		InputTokens:      2,
		OutputTokens:     3,
		ReasoningTokens:  0,
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
	stats := usage.NewRequestStatistics()
	h := NewHandlerWithoutConfigFilePath(cfg, nil)
	h.SetUsageStatistics(stats)
	h.SetQuotaManager(manager)
	rr := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rr)
	c.Request = httptest.NewRequest(http.MethodGet, "/v0/management/usage", nil)
	h.GetUsageStatistics(c)
	stats.Record(nil, coreusage.Record{APIKey: "k1", Model: "gpt-5.4-mini", RequestedAt: now.Add(time.Minute), Detail: coreusage.Detail{InputTokens: 1, OutputTokens: 4, TotalTokens: 5}})
	rr = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(rr)
	c.Request = httptest.NewRequest(http.MethodGet, "/v0/management/usage", nil)
	h.GetUsageStatistics(c)
	var payload struct {
		Usage usage.StatisticsSnapshot `json:"usage"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v body=%s", err, rr.Body.String())
	}
	if got := payload.Usage.APIs["k1"].TotalRequests; got != 2 {
		t.Fatalf("expected hydrated + live total requests 2, got %#v", payload.Usage.APIs)
	}
	if got := payload.Usage.APIs["k1"].TotalTokens; got != 10 {
		t.Fatalf("expected hydrated + live total tokens 10, got %#v", payload.Usage.APIs)
	}
}

func TestGetUsageStatisticsPrefersPersistedUsageSnapshotOverRequestLogs(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	dbPath := filepath.Join(tmpDir, "prompt_logs.sqlite")
	cfg := &config.Config{SDKConfig: config.SDKConfig{APIKeys: []string{"k1"}}}
	cfg.SQLitePromptLog.Path = dbPath
	manager := quota.NewManager()
	if err := manager.UpdateConfig(cfg, configPath); err != nil {
		t.Fatalf("UpdateConfig() error = %v", err)
	}
	stats := usage.NewRequestStatistics()
	persisted := usage.StatisticsSnapshot{
		APIs: map[string]usage.APISnapshot{
			"k1": {
				Models: map[string]usage.ModelSnapshot{
					"gpt-5.4-mini": {
						Details: []usage.RequestDetail{{
							Timestamp: time.Date(2026, 4, 23, 10, 0, 0, 0, time.UTC),
							Source:    "openai",
							Tokens:    usage.TokenStats{InputTokens: 2, OutputTokens: 3, TotalTokens: 5},
						}},
					},
				},
			},
		},
	}
	if err := manager.StoreUsageSnapshot(statsFromSnapshot(t, persisted)); err != nil {
		t.Fatalf("StoreUsageSnapshot() error = %v", err)
	}
	store, err := quota.OpenSQLiteStore(dbPath, 0)
	if err != nil {
		t.Fatalf("OpenSQLiteStore() error = %v", err)
	}
	defer func() { _ = store.Close() }()
	if err := store.InsertRequestLog(quota.RequestLogEntry{
		RequestID:        "req-persisted-priority-1",
		URL:              "/v1/chat/completions",
		Method:           http.MethodPost,
		APIKeyHash:       quota.RequestLogAPIKeyIdentifiers("k1")[1],
		Provider:         "openai",
		Model:            "gpt-5.4-mini",
		StatusCode:       http.StatusOK,
		InputTokens:      7,
		OutputTokens:     9,
		ReasoningTokens:  1,
		RequestTimestamp: time.Date(2026, 4, 23, 11, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("InsertRequestLog() error = %v", err)
	}
	h := NewHandlerWithoutConfigFilePath(cfg, nil)
	h.SetUsageStatistics(stats)
	h.SetQuotaManager(manager)
	rr := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rr)
	c.Request = httptest.NewRequest(http.MethodGet, "/v0/management/usage", nil)
	h.GetUsageStatistics(c)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	var payload struct {
		Usage usage.StatisticsSnapshot `json:"usage"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v body=%s", err, rr.Body.String())
	}
	if got := payload.Usage.APIs["k1"].TotalTokens; got != 5 {
		t.Fatalf("expected persisted usage snapshot to win over request log fallback, got %#v", payload.Usage.APIs)
	}
	if got := stats.Snapshot().APIs["k1"].TotalTokens; got != 5 {
		t.Fatalf("expected in-memory stats to hydrate from persisted snapshot only, got %#v", stats.Snapshot().APIs)
	}
}

func statsFromSnapshot(t *testing.T, snapshot usage.StatisticsSnapshot) *usage.RequestStatistics {
	t.Helper()
	stats := usage.NewRequestStatistics()
	result := stats.MergeSnapshot(snapshot)
	if result.Added == 0 && len(snapshot.APIs) > 0 {
		t.Fatalf("expected snapshot merge to add records, got %+v", result)
	}
	return stats
}
