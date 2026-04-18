package quota

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/usage"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/util"
	_ "modernc.org/sqlite"
)

type SQLiteStore struct {
	db            *sql.DB
	retentionDays int
}

func OpenSQLiteStore(path string, retentionDays int) (*SQLiteStore, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	store := &SQLiteStore{db: db, retentionDays: retentionDays}
	if err := store.init(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *SQLiteStore) init() error {
	if s == nil || s.db == nil {
		return nil
	}
	stmts := []string{
		`PRAGMA journal_mode=WAL;`,
		`PRAGMA busy_timeout=5000;`,
		`PRAGMA synchronous=NORMAL;`,
		`CREATE TABLE IF NOT EXISTS client_key_usage (
			api_key TEXT PRIMARY KEY,
			output_tokens_used INTEGER NOT NULL DEFAULT 0,
			window_started_at TEXT,
			updated_at TEXT NOT NULL
		);`,
		`ALTER TABLE client_key_usage ADD COLUMN window_started_at TEXT;`,
		`CREATE TABLE IF NOT EXISTS request_logs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			request_id TEXT,
			url TEXT NOT NULL,
			method TEXT NOT NULL,
			api_key_hash TEXT,
			provider TEXT,
			model TEXT,
			status_code INTEGER NOT NULL,
			is_stream INTEGER NOT NULL DEFAULT 0,
			latency_ms INTEGER NOT NULL DEFAULT 0,
			input_tokens INTEGER NOT NULL DEFAULT 0,
			output_tokens INTEGER NOT NULL DEFAULT 0,
			reasoning_tokens INTEGER NOT NULL DEFAULT 0,
			request_timestamp TEXT,
			api_response_timestamp TEXT,
			created_at TEXT NOT NULL,
			request_headers TEXT,
			response_headers TEXT,
			request_body BLOB,
			response_body BLOB,
			api_request_body BLOB,
			api_response_body BLOB,
			api_websocket_timeline BLOB,
			websocket_timeline BLOB,
			truncation_flags TEXT,
			api_errors TEXT
		);`,
		`ALTER TABLE request_logs ADD COLUMN provider TEXT;`,
		`ALTER TABLE request_logs ADD COLUMN model TEXT;`,
		`ALTER TABLE request_logs ADD COLUMN latency_ms INTEGER NOT NULL DEFAULT 0;`,
		`ALTER TABLE request_logs ADD COLUMN input_tokens INTEGER NOT NULL DEFAULT 0;`,
		`ALTER TABLE request_logs ADD COLUMN output_tokens INTEGER NOT NULL DEFAULT 0;`,
		`ALTER TABLE request_logs ADD COLUMN reasoning_tokens INTEGER NOT NULL DEFAULT 0;`,
		`ALTER TABLE request_logs ADD COLUMN truncation_flags TEXT;`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil && !strings.Contains(err.Error(), "duplicate column name") {
			return err
		}
	}
	if s.retentionDays > 0 {
		if err := s.CleanupExpiredLogs(); err != nil {
			return err
		}
	}
	return nil
}

func (s *SQLiteStore) LoadUsage() (map[string]usageState, error) {
	result := make(map[string]usageState)
	if s == nil || s.db == nil {
		return result, nil
	}
	rows, err := s.db.Query(`SELECT api_key, output_tokens_used, window_started_at FROM client_key_usage`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var apiKey string
		var used int64
		var windowStartedAt sql.NullString
		if err := rows.Scan(&apiKey, &used, &windowStartedAt); err != nil {
			return nil, err
		}
		state := usageState{used: used}
		if windowStartedAt.Valid && strings.TrimSpace(windowStartedAt.String) != "" {
			if parsed, err := time.Parse(time.RFC3339Nano, windowStartedAt.String); err == nil {
				state.windowStartedAt = parsed.UTC()
			}
		}
		result[apiKey] = state
	}
	return result, rows.Err()
}

func (s *SQLiteStore) StoreUsage(apiKey string, usage usageState) error {
	if s == nil || s.db == nil || strings.TrimSpace(apiKey) == "" {
		return nil
	}
	var windowStartedAt any
	if !usage.windowStartedAt.IsZero() {
		windowStartedAt = usage.windowStartedAt.UTC().Format(time.RFC3339Nano)
	}
	_, err := s.db.Exec(`
		INSERT INTO client_key_usage(api_key, output_tokens_used, window_started_at, updated_at)
		VALUES(?, ?, ?, ?)
		ON CONFLICT(api_key) DO UPDATE SET
			output_tokens_used = excluded.output_tokens_used,
			window_started_at = excluded.window_started_at,
			updated_at = excluded.updated_at
	`, apiKey, usage.used, windowStartedAt, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

func (s *SQLiteStore) ResetUsage(apiKey string) error {
	if s == nil || s.db == nil {
		return nil
	}
	_, err := s.db.Exec(`DELETE FROM client_key_usage WHERE api_key = ?`, apiKey)
	return err
}

func (s *SQLiteStore) InsertRequestLog(entry RequestLogEntry) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("sqlite store unavailable")
	}
	if s.retentionDays > 0 {
		if err := s.CleanupExpiredLogs(); err != nil {
			return err
		}
	}
	_, err := s.db.Exec(`
		INSERT INTO request_logs(
			request_id, url, method, api_key_hash, provider, model, status_code, is_stream, latency_ms,
			input_tokens, output_tokens, reasoning_tokens,
			request_timestamp, api_response_timestamp, created_at,
			request_headers, response_headers,
			request_body, response_body, api_request_body, api_response_body,
			api_websocket_timeline, websocket_timeline, truncation_flags, api_errors
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		entry.RequestID,
		entry.URL,
		entry.Method,
		entry.APIKeyHash,
		entry.Provider,
		entry.Model,
		entry.StatusCode,
		boolToInt(entry.IsStream),
		entry.LatencyMS,
		entry.InputTokens,
		entry.OutputTokens,
		entry.ReasoningTokens,
		formatNullableTime(entry.RequestTimestamp),
		formatNullableTime(entry.APIResponseTimestamp),
		time.Now().UTC().Format(time.RFC3339Nano),
		entry.RequestHeadersJSON,
		entry.ResponseHeadersJSON,
		entry.RequestBody,
		entry.ResponseBody,
		entry.APIRequestBody,
		entry.APIResponseBody,
		entry.APIWebsocketTimeline,
		entry.WebsocketTimeline,
		entry.TruncationFlagsJSON,
		entry.APIErrorsJSON,
	)
	return err
}

func RequestLogAPIKeyIdentifiers(apiKey string) []string {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return nil
	}
	identifiers := []string{util.MaskSensitiveHeaderValue("Authorization", "Bearer "+apiKey)}
	sum := sha256.Sum256([]byte(apiKey))
	identifiers = append(identifiers, hex.EncodeToString(sum[:]))
	return identifiers
}

func (s *SQLiteStore) LoadRequestLogUsageSnapshot(apiKeys []string) (usage.StatisticsSnapshot, error) {
	result := usage.StatisticsSnapshot{
		APIs:           map[string]usage.APISnapshot{},
		RequestsByDay:  map[string]int64{},
		RequestsByHour: map[string]int64{},
		TokensByDay:    map[string]int64{},
		TokensByHour:   map[string]int64{},
	}
	if s == nil || s.db == nil || len(apiKeys) == 0 {
		return result, nil
	}
	identifierToAPIKey := make(map[string]string, len(apiKeys)*2)
	placeholders := make([]string, 0, len(apiKeys)*2)
	args := make([]any, 0, len(apiKeys)*2)
	seen := make(map[string]struct{}, len(apiKeys)*2)
	for _, apiKey := range apiKeys {
		for _, identifier := range RequestLogAPIKeyIdentifiers(apiKey) {
			if identifier == "" {
				continue
			}
			if _, ok := seen[identifier]; ok {
				continue
			}
			seen[identifier] = struct{}{}
			identifierToAPIKey[identifier] = strings.TrimSpace(apiKey)
			placeholders = append(placeholders, "?")
			args = append(args, identifier)
		}
	}
	if len(args) == 0 {
		return result, nil
	}
	query := `SELECT api_key_hash, provider, model, status_code, latency_ms, input_tokens, output_tokens, reasoning_tokens, request_timestamp, api_response_timestamp, created_at FROM request_logs WHERE api_key_hash IN (` + strings.Join(placeholders, ",") + `) ORDER BY id ASC`
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return result, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var apiKeyHash, provider, model string
		var statusCode int
		var latencyMS, inputTokens, outputTokens, reasoningTokens int64
		var requestTimestamp, apiResponseTimestamp, createdAt sql.NullString
		if err := rows.Scan(&apiKeyHash, &provider, &model, &statusCode, &latencyMS, &inputTokens, &outputTokens, &reasoningTokens, &requestTimestamp, &apiResponseTimestamp, &createdAt); err != nil {
			return result, err
		}
		apiKey := identifierToAPIKey[apiKeyHash]
		if apiKey == "" {
			continue
		}
		timestamp := parseRequestLogTimestamp(requestTimestamp.String, apiResponseTimestamp.String, createdAt.String)
		tokens := usage.TokenStats{
			InputTokens:     inputTokens,
			OutputTokens:    outputTokens,
			ReasoningTokens: reasoningTokens,
			TotalTokens:     inputTokens + outputTokens + reasoningTokens,
		}
		result.TotalRequests++
		if statusCode >= 400 {
			result.FailureCount++
		} else {
			result.SuccessCount++
		}
		result.TotalTokens += tokens.TotalTokens
		dayKey := timestamp.Format("2006-01-02")
		hourKey := formatSnapshotHour(timestamp.Hour())
		result.RequestsByDay[dayKey]++
		result.RequestsByHour[hourKey]++
		result.TokensByDay[dayKey] += tokens.TotalTokens
		result.TokensByHour[hourKey] += tokens.TotalTokens
		apiSnapshot := result.APIs[apiKey]
		if apiSnapshot.Models == nil {
			apiSnapshot.Models = make(map[string]usage.ModelSnapshot)
		}
		apiSnapshot.TotalRequests++
		apiSnapshot.TotalTokens += tokens.TotalTokens
		modelName := strings.TrimSpace(model)
		if modelName == "" {
			modelName = "unknown"
		}
		modelSnapshot := apiSnapshot.Models[modelName]
		modelSnapshot.TotalRequests++
		modelSnapshot.TotalTokens += tokens.TotalTokens
		modelSnapshot.Details = append(modelSnapshot.Details, usage.RequestDetail{
			Timestamp: timestamp,
			LatencyMs: latencyMS,
			Source:    strings.TrimSpace(provider),
			Tokens:    tokens,
			Failed:    statusCode >= 400,
		})
		apiSnapshot.Models[modelName] = modelSnapshot
		result.APIs[apiKey] = apiSnapshot
	}
	return result, rows.Err()
}

func (s *SQLiteStore) CleanupExpiredLogs() error {
	if s == nil || s.db == nil || s.retentionDays <= 0 {
		return nil
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -s.retentionDays).Format(time.RFC3339Nano)
	_, err := s.db.Exec(`DELETE FROM request_logs WHERE created_at < ?`, cutoff)
	return err
}

func parseRequestLogTimestamp(values ...string) time.Time {
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if parsed, err := time.Parse(time.RFC3339Nano, trimmed); err == nil {
			return parsed.UTC()
		}
		if parsed, err := time.Parse(time.RFC3339, trimmed); err == nil {
			return parsed.UTC()
		}
	}
	return time.Now().UTC()
}

func formatSnapshotHour(hour int) string {
	if hour < 0 {
		hour = 0
	}
	if hour > 23 {
		hour = 23
	}
	return fmt.Sprintf("%02d:00", hour)
}

func formatNullableTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func (s *SQLiteStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

type RequestLogEntry struct {
	RequestID            string
	URL                  string
	Method               string
	APIKeyHash           string
	Provider             string
	Model                string
	StatusCode           int
	IsStream             bool
	LatencyMS            int64
	InputTokens          int64
	OutputTokens         int64
	ReasoningTokens      int64
	RequestTimestamp     time.Time
	APIResponseTimestamp time.Time
	RequestHeadersJSON   string
	ResponseHeadersJSON  string
	RequestBody          []byte
	ResponseBody         []byte
	APIRequestBody       []byte
	APIResponseBody      []byte
	APIWebsocketTimeline []byte
	WebsocketTimeline    []byte
	TruncationFlagsJSON  string
	APIErrorsJSON        string
}
