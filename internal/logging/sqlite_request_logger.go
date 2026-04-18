package logging

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/interfaces"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/quota"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/util"
	log "github.com/sirupsen/logrus"
	"github.com/tidwall/gjson"
)

type SQLiteRequestLogger struct {
	enabled          bool
	capturePrompts   bool
	captureResponses bool
	bodyMaxBytes     int
	retentionDays    int
	hashAPIKeys      bool
	store            *quota.SQLiteStore
}

func NewSQLiteRequestLogger(cfg *config.Config, configPath string) (*SQLiteRequestLogger, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config is nil")
	}
	store, err := quota.OpenSQLiteStore(quota.ResolveSharedSQLitePath(cfg, configPath), cfg.SQLitePromptLog.RetentionDays)
	if err != nil {
		return nil, err
	}
	bodyMaxBytes := cfg.SQLitePromptLog.BodyMaxBytes
	if bodyMaxBytes <= 0 {
		bodyMaxBytes = 256 * 1024
	}
	return &SQLiteRequestLogger{
		enabled:          cfg.SQLitePromptLog.Enabled,
		capturePrompts:   cfg.SQLitePromptLog.CapturePrompts,
		captureResponses: cfg.SQLitePromptLog.CaptureResponses,
		bodyMaxBytes:     bodyMaxBytes,
		retentionDays:    cfg.SQLitePromptLog.RetentionDays,
		hashAPIKeys:      cfg.SQLitePromptLog.HashAPIKeys,
		store:            store,
	}, nil
}

func (l *SQLiteRequestLogger) IsEnabled() bool {
	return l != nil && l.enabled
}

func (l *SQLiteRequestLogger) SetEnabled(enabled bool) {
	if l == nil {
		return
	}
	l.enabled = enabled
}

func (l *SQLiteRequestLogger) LogRequest(url, method string, requestHeaders map[string][]string, body []byte, statusCode int, responseHeaders map[string][]string, response, websocketTimeline, apiRequest, apiResponse, apiWebsocketTimeline []byte, apiResponseErrors []*interfaces.ErrorMessage, requestID string, requestTimestamp, apiResponseTimestamp time.Time) error {
	if l == nil || !l.enabled || l.store == nil {
		return nil
	}
	entry, err := l.buildEntry(false, url, method, requestHeaders, body, statusCode, responseHeaders, response, websocketTimeline, apiRequest, apiResponse, apiWebsocketTimeline, apiResponseErrors, requestID, requestTimestamp, apiResponseTimestamp)
	if err != nil {
		return err
	}
	if err = l.store.InsertRequestLog(entry); err != nil {
		log.WithError(err).Warn("sqlite request logger insert failed")
	}
	return nil
}

func (l *SQLiteRequestLogger) LogStreamingRequest(url, method string, headers map[string][]string, body []byte, requestID string) (StreamingLogWriter, error) {
	if l == nil || !l.enabled {
		return &NoOpStreamingLogWriter{}, nil
	}
	writer := &SQLiteStreamingLogWriter{
		logger:         l,
		url:            url,
		method:         method,
		requestHeaders: cloneHeaders(headers),
		requestBody:    body,
		requestID:      requestID,
		requestTS:      time.Now(),
	}
	return writer, nil
}

func (l *SQLiteRequestLogger) buildEntry(isStream bool, url, method string, requestHeaders map[string][]string, body []byte, statusCode int, responseHeaders map[string][]string, response, websocketTimeline, apiRequest, apiResponse, apiWebsocketTimeline []byte, apiResponseErrors []*interfaces.ErrorMessage, requestID string, requestTimestamp, apiResponseTimestamp time.Time) (quota.RequestLogEntry, error) {
	metadata := deriveLogMetadata(url, method, requestTimestamp, apiResponseTimestamp, body, response, apiRequest, apiResponse)
	requestHeadersJSON, err := marshalJSON(sanitizeHeaders(requestHeaders))
	if err != nil {
		return quota.RequestLogEntry{}, err
	}
	responseHeadersJSON, err := marshalJSON(sanitizeHeaders(responseHeaders))
	if err != nil {
		return quota.RequestLogEntry{}, err
	}
	apiErrorsJSON, err := marshalJSON(apiResponseErrors)
	if err != nil {
		return quota.RequestLogEntry{}, err
	}
	requestBody, requestTruncated := l.captureRequestBody(body)
	responseBody, responseTruncated := l.captureResponseBody(response)
	apiRequestBody, apiRequestTruncated := trimBytes(apiRequest, l.bodyMaxBytes)
	apiResponseBody, apiResponseTruncated := trimBytes(apiResponse, l.bodyMaxBytes)
	apiTimeline, apiTimelineTruncated := trimBytes(apiWebsocketTimeline, l.bodyMaxBytes)
	wsTimeline, wsTimelineTruncated := trimBytes(websocketTimeline, l.bodyMaxBytes)
	metadata.TruncationFlags = truncationFlagsJSON(requestTruncated, responseTruncated, apiRequestTruncated, apiResponseTruncated, apiTimelineTruncated, wsTimelineTruncated)
	entry := quota.RequestLogEntry{
		RequestID:            requestID,
		URL:                  url,
		Method:               method,
		APIKeyHash:           l.apiKeyIdentifier(requestHeaders),
		Provider:             metadata.Provider,
		Model:                metadata.Model,
		StatusCode:           statusCode,
		IsStream:             isStream,
		LatencyMS:            metadata.LatencyMS,
		InputTokens:          metadata.InputTokens,
		OutputTokens:         metadata.OutputTokens,
		ReasoningTokens:      metadata.ReasoningTokens,
		RequestTimestamp:     requestTimestamp,
		APIResponseTimestamp: apiResponseTimestamp,
		RequestHeadersJSON:   requestHeadersJSON,
		ResponseHeadersJSON:  responseHeadersJSON,
		RequestBody:          requestBody,
		ResponseBody:         responseBody,
		APIRequestBody:       apiRequestBody,
		APIResponseBody:      apiResponseBody,
		APIWebsocketTimeline: apiTimeline,
		WebsocketTimeline:    wsTimeline,
		TruncationFlagsJSON:  metadata.TruncationFlags,
		APIErrorsJSON:        apiErrorsJSON,
	}
	return entry, nil
}

func (l *SQLiteRequestLogger) captureRequestBody(body []byte) ([]byte, bool) {
	if l == nil || !l.capturePrompts {
		return nil, false
	}
	return trimBytes(body, l.bodyMaxBytes)
}

func (l *SQLiteRequestLogger) captureResponseBody(body []byte) ([]byte, bool) {
	if l == nil || !l.captureResponses {
		return nil, false
	}
	return trimBytes(body, l.bodyMaxBytes)
}

func (l *SQLiteRequestLogger) apiKeyIdentifier(headers map[string][]string) string {
	apiKey := extractAPIKeyFromHeaders(headers)
	if apiKey == "" {
		return ""
	}
	if !l.hashAPIKeys {
		return util.MaskSensitiveHeaderValue("Authorization", "Bearer "+apiKey)
	}
	sum := sha256.Sum256([]byte(apiKey))
	return hex.EncodeToString(sum[:])
}

type SQLiteStreamingLogWriter struct {
	logger          *SQLiteRequestLogger
	url             string
	method          string
	requestHeaders  map[string][]string
	requestBody     []byte
	requestID       string
	requestTS       time.Time
	status          int
	responseHeaders map[string][]string
	firstChunkTS    time.Time
	mu              sync.Mutex
	response        []byte
	apiRequest      []byte
	apiResponse     []byte
	apiTimeline     []byte
}

func (w *SQLiteStreamingLogWriter) WriteChunkAsync(chunk []byte) {
	if w == nil || len(chunk) == 0 {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.logger != nil && w.logger.captureResponses {
		trimmed, _ := trimBytes(chunk, w.logger.bodyMaxBytes)
		w.response = append(w.response, trimmed...)
		w.response, _ = trimBytes(w.response, w.logger.bodyMaxBytes)
	}
}

func (w *SQLiteStreamingLogWriter) WriteStatus(status int, headers map[string][]string) error {
	if w == nil {
		return nil
	}
	w.status = status
	w.responseHeaders = cloneHeaders(headers)
	return nil
}

func (w *SQLiteStreamingLogWriter) WriteAPIRequest(apiRequest []byte) error {
	if w == nil {
		return nil
	}
	w.apiRequest = apiRequest
	return nil
}

func (w *SQLiteStreamingLogWriter) WriteAPIResponse(apiResponse []byte) error {
	if w == nil {
		return nil
	}
	w.apiResponse = apiResponse
	return nil
}

func (w *SQLiteStreamingLogWriter) WriteAPIWebsocketTimeline(apiWebsocketTimeline []byte) error {
	if w == nil {
		return nil
	}
	w.apiTimeline = apiWebsocketTimeline
	return nil
}

func (w *SQLiteStreamingLogWriter) SetFirstChunkTimestamp(timestamp time.Time) {
	if w == nil {
		return
	}
	w.firstChunkTS = timestamp
}

func (w *SQLiteStreamingLogWriter) Close() error {
	if w == nil || w.logger == nil || w.logger.store == nil {
		return nil
	}
	entry, err := w.logger.buildEntry(true, w.url, w.method, w.requestHeaders, w.requestBody, w.status, w.responseHeaders, w.response, nil, w.apiRequest, w.apiResponse, w.apiTimeline, nil, w.requestID, w.requestTS, w.firstChunkTS)
	if err != nil {
		return err
	}
	return w.logger.store.InsertRequestLog(entry)
}

type derivedMetadata struct {
	Provider        string
	Model           string
	LatencyMS       int64
	InputTokens     int64
	OutputTokens    int64
	ReasoningTokens int64
	TruncationFlags string
}

func deriveLogMetadata(url, method string, requestTimestamp, responseTimestamp time.Time, requestBody, responseBody, apiRequest, apiResponse []byte) derivedMetadata {
	provider := inferProviderFromURL(url)
	model := firstNonEmpty(
		jsonString(requestBody, "model"),
		jsonString(apiRequest, "model"),
		jsonString(apiResponse, "model"),
		jsonString(responseBody, "model"),
	)
	inputTokens := firstPositive(
		jsonInt(apiResponse, "usage.prompt_tokens"),
		jsonInt(responseBody, "usage.prompt_tokens"),
	)
	outputTokens := firstPositive(
		jsonInt(apiResponse, "usage.completion_tokens"),
		jsonInt(apiResponse, "usage.output_tokens"),
		jsonInt(responseBody, "usage.completion_tokens"),
		jsonInt(responseBody, "usage.output_tokens"),
	)
	reasoningTokens := firstPositive(
		jsonInt(apiResponse, "usage.output_tokens_details.reasoning_tokens"),
		jsonInt(apiResponse, "usage.reasoning_tokens"),
		jsonInt(responseBody, "usage.output_tokens_details.reasoning_tokens"),
		jsonInt(responseBody, "usage.reasoning_tokens"),
	)
	latency := int64(0)
	if !requestTimestamp.IsZero() && !responseTimestamp.IsZero() && responseTimestamp.After(requestTimestamp) {
		latency = responseTimestamp.Sub(requestTimestamp).Milliseconds()
	}
	return derivedMetadata{
		Provider:        provider,
		Model:           model,
		LatencyMS:       latency,
		InputTokens:     inputTokens,
		OutputTokens:    outputTokens,
		ReasoningTokens: reasoningTokens,
	}
}

func inferProviderFromURL(url string) string {
	clean := path.Clean(strings.TrimSpace(url))
	if strings.HasPrefix(clean, "/api/provider/") {
		parts := strings.Split(strings.TrimPrefix(clean, "/api/provider/"), "/")
		if len(parts) > 0 {
			return parts[0]
		}
	}
	if strings.HasPrefix(clean, "/v1beta") {
		return "gemini"
	}
	if strings.Contains(clean, "/messages") {
		return "anthropic"
	}
	if strings.HasPrefix(clean, "/v1") {
		return "openai"
	}
	return ""
}

func sanitizeHeaders(headers map[string][]string) map[string][]string {
	if len(headers) == 0 {
		return map[string][]string{}
	}
	result := make(map[string][]string, len(headers))
	for key, values := range headers {
		masked := make([]string, len(values))
		for i, value := range values {
			masked[i] = util.MaskSensitiveHeaderValue(key, value)
		}
		result[key] = masked
	}
	return result
}

func trimBytes(body []byte, max int) ([]byte, bool) {
	if len(body) == 0 {
		return nil, false
	}
	if max <= 0 || len(body) <= max {
		copied := make([]byte, len(body))
		copy(copied, body)
		return copied, false
	}
	copied := make([]byte, max)
	copy(copied, body[:max])
	return copied, true
}

func truncationFlagsJSON(requestTruncated, responseTruncated, apiRequestTruncated, apiResponseTruncated, apiTimelineTruncated, wsTimelineTruncated bool) string {
	flags := map[string]bool{}
	if requestTruncated {
		flags["request_body"] = true
	}
	if responseTruncated {
		flags["response_body"] = true
	}
	if apiRequestTruncated {
		flags["api_request_body"] = true
	}
	if apiResponseTruncated {
		flags["api_response_body"] = true
	}
	if apiTimelineTruncated {
		flags["api_websocket_timeline"] = true
	}
	if wsTimelineTruncated {
		flags["websocket_timeline"] = true
	}
	if len(flags) == 0 {
		return ""
	}
	data, err := json.Marshal(flags)
	if err != nil {
		return ""
	}
	return string(data)
}

func cloneHeaders(headers map[string][]string) map[string][]string {
	if len(headers) == 0 {
		return map[string][]string{}
	}
	result := make(map[string][]string, len(headers))
	for key, values := range headers {
		copied := make([]string, len(values))
		copy(copied, values)
		result[key] = copied
	}
	return result
}

func marshalJSON(value any) (string, error) {
	if value == nil {
		return "", nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func extractAPIKeyFromHeaders(headers map[string][]string) string {
	for key, values := range headers {
		if len(values) == 0 {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "authorization":
			parts := strings.SplitN(values[0], " ", 2)
			if len(parts) == 2 && strings.EqualFold(strings.TrimSpace(parts[0]), "bearer") {
				return strings.TrimSpace(parts[1])
			}
			return strings.TrimSpace(values[0])
		case "x-goog-api-key", "x-api-key":
			return strings.TrimSpace(values[0])
		}
	}
	return ""
}

func jsonString(data []byte, path string) string {
	if len(data) == 0 {
		return ""
	}
	value := gjson.GetBytes(data, path)
	if !value.Exists() {
		return ""
	}
	return strings.TrimSpace(value.String())
}

func jsonInt(data []byte, path string) int64 {
	if len(data) == 0 {
		return 0
	}
	value := gjson.GetBytes(data, path)
	if !value.Exists() {
		return 0
	}
	return value.Int()
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func firstPositive(values ...int64) int64 {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}
