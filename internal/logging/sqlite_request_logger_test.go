package logging

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	_ "modernc.org/sqlite"
)

func TestSQLiteRequestLoggerLogRequest(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "request_logs.sqlite")
	cfg := &config.Config{}
	cfg.RequestLog = true
	cfg.SQLitePromptLog.Enabled = true
	cfg.SQLitePromptLog.Path = dbPath
	cfg.SQLitePromptLog.CapturePrompts = true
	cfg.SQLitePromptLog.CaptureResponses = true
	cfg.SQLitePromptLog.RetentionDays = 7
	cfg.SQLitePromptLog.HashAPIKeys = true
	logger, err := NewSQLiteRequestLogger(cfg, filepath.Join(tmpDir, "config.yaml"))
	if err != nil {
		t.Fatalf("NewSQLiteRequestLogger() error = %v", err)
	}
	requestTS := time.Now().UTC()
	responseTS := requestTS.Add(100 * time.Millisecond)
	err = logger.LogRequest(
		"/api/provider/openai/v1/chat/completions",
		"POST",
		map[string][]string{"Authorization": {"Bearer test-key"}},
		[]byte(`{"model":"mock-model","messages":[{"role":"user","content":"hello"}]}`),
		200,
		map[string][]string{"Content-Type": {"application/json"}},
		[]byte(`{"id":"1","model":"mock-model","choices":[{"message":{"content":"world"}}],"usage":{"prompt_tokens":2,"completion_tokens":3}}`),
		nil,
		nil,
		nil,
		nil,
		nil,
		"req-1",
		requestTS,
		responseTS,
	)
	if err != nil {
		t.Fatalf("LogRequest() error = %v", err)
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer func() { _ = db.Close() }()
	var count int
	if err = db.QueryRow(`SELECT COUNT(*) FROM request_logs`).Scan(&count); err != nil {
		t.Fatalf("COUNT query error = %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 request log row, got %d", count)
	}
	var requestBody string
	var responseBody string
	var apiKeyHash string
	var provider string
	var model string
	var inputTokens int64
	var outputTokens int64
	var latencyMS int64
	var requestHeaders string
	if err = db.QueryRow(`SELECT request_body, response_body, api_key_hash, provider, model, input_tokens, output_tokens, latency_ms, request_headers FROM request_logs LIMIT 1`).Scan(&requestBody, &responseBody, &apiKeyHash, &provider, &model, &inputTokens, &outputTokens, &latencyMS, &requestHeaders); err != nil {
		t.Fatalf("SELECT request_logs error = %v", err)
	}
	if requestBody == "" || responseBody == "" {
		t.Fatalf("expected bodies to be stored, got request=%q response=%q", requestBody, responseBody)
	}
	if apiKeyHash == "" || apiKeyHash == "test-key" {
		t.Fatalf("expected hashed API key, got %q", apiKeyHash)
	}
	if provider != "openai" || model != "mock-model" {
		t.Fatalf("expected provider/model metadata, got provider=%q model=%q", provider, model)
	}
	if inputTokens != 2 || outputTokens != 3 {
		t.Fatalf("expected token metadata 2/3, got %d/%d", inputTokens, outputTokens)
	}
	if latencyMS <= 0 {
		t.Fatalf("expected latency > 0, got %d", latencyMS)
	}
	if requestHeaders == "" || requestHeaders == `{"Authorization":["Bearer test-key"]}` {
		t.Fatalf("expected masked request headers, got %q", requestHeaders)
	}
}
