package codex

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestRefreshTokensWithRetry_NonRetryableOnlyAttemptsOnce(t *testing.T) {
	var calls int32
	auth := &CodexAuth{
		httpClient: &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				atomic.AddInt32(&calls, 1)
				return &http.Response{
					StatusCode: http.StatusBadRequest,
					Body:       io.NopCloser(strings.NewReader(`{"error":"invalid_grant","code":"refresh_token_reused"}`)),
					Header:     make(http.Header),
					Request:    req,
				}, nil
			}),
		},
	}

	_, err := auth.RefreshTokensWithRetry(context.Background(), "dummy_refresh_token", 3)
	if err == nil {
		t.Fatalf("expected error for non-retryable refresh failure")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "refresh_token_reused") {
		t.Fatalf("expected refresh_token_reused in error, got: %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("expected 1 refresh attempt, got %d", got)
	}
}

func TestNewCodexAuthWithProxyURL_OverrideDirectDisablesProxy(t *testing.T) {
	cfg := &config.Config{SDKConfig: config.SDKConfig{ProxyURL: "http://proxy.example.com:8080"}}
	auth := NewCodexAuthWithProxyURL(cfg, "direct")

	transport, ok := auth.httpClient.Transport.(*http.Transport)
	if !ok || transport == nil {
		t.Fatalf("expected http.Transport, got %T", auth.httpClient.Transport)
	}
	if transport.Proxy != nil {
		t.Fatal("expected direct transport to disable proxy function")
	}
}

func TestNewCodexAuthWithProxyURL_OverrideProxyTakesPrecedence(t *testing.T) {
	cfg := &config.Config{SDKConfig: config.SDKConfig{ProxyURL: "http://global.example.com:8080"}}
	auth := NewCodexAuthWithProxyURL(cfg, "http://override.example.com:8081")

	transport, ok := auth.httpClient.Transport.(*http.Transport)
	if !ok || transport == nil {
		t.Fatalf("expected http.Transport, got %T", auth.httpClient.Transport)
	}
	req, errReq := http.NewRequest(http.MethodGet, "https://example.com", nil)
	if errReq != nil {
		t.Fatalf("new request: %v", errReq)
	}
	proxyURL, errProxy := transport.Proxy(req)
	if errProxy != nil {
		t.Fatalf("proxy func: %v", errProxy)
	}
	if proxyURL == nil || proxyURL.String() != "http://override.example.com:8081" {
		t.Fatalf("proxy URL = %v, want http://override.example.com:8081", proxyURL)
	}
}

func TestFetchUsageQuota_ParsesDailyAndWeeklyWindows(t *testing.T) {
	var accountHeader string
	accessToken := testJWTToken("acc_from_jwt")
	auth := &CodexAuth{
		httpClient: &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				accountHeader = req.Header.Get("ChatGPT-Account-Id")
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(`{"rate_limit":{"primary_window":{"used_percent":36.4,"reset_at":1713312000},"secondary_window":{"used_percent":58.5,"reset_after_seconds":7200}}}`)),
					Header:     make(http.Header),
					Request:    req,
				}, nil
			}),
		},
	}

	snapshot, err := auth.FetchUsageQuota(context.Background(), accessToken, time.Now().Add(time.Hour).Format(time.RFC3339), "")
	if err != nil {
		t.Fatalf("FetchUsageQuota() error = %v", err)
	}
	if snapshot == nil {
		t.Fatalf("expected snapshot")
	}
	if snapshot.AccountID != "acc_from_jwt" {
		t.Fatalf("expected account ID from jwt, got %#v", snapshot)
	}
	if accountHeader != "acc_from_jwt" {
		t.Fatalf("expected ChatGPT-Account-Id header, got %q", accountHeader)
	}
	if snapshot.Daily == nil || snapshot.Daily.PercentRemaining != 64 {
		t.Fatalf("expected daily percent remaining 64, got %#v", snapshot.Daily)
	}
	if snapshot.Daily.ResetTimeISO != "2024-04-17T00:00:00Z" {
		t.Fatalf("expected daily reset time, got %#v", snapshot.Daily)
	}
	if snapshot.Weekly == nil || snapshot.Weekly.PercentRemaining != 42 {
		t.Fatalf("expected weekly percent remaining 42, got %#v", snapshot.Weekly)
	}
	if snapshot.Weekly.ResetTimeISO == "" {
		t.Fatalf("expected weekly reset time from reset_after_seconds")
	}
	if snapshot.Error != "" {
		t.Fatalf("expected empty error, got %#v", snapshot)
	}
}

func TestFetchUsageQuota_ExpiredTokenSkipsHTTP(t *testing.T) {
	var calls int32
	auth := &CodexAuth{
		httpClient: &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				atomic.AddInt32(&calls, 1)
				return nil, nil
			}),
		},
	}

	snapshot, err := auth.FetchUsageQuota(context.Background(), "tok", time.Now().Add(-time.Hour).Format(time.RFC3339), "acc-expired")
	if err != nil {
		t.Fatalf("FetchUsageQuota() error = %v", err)
	}
	if snapshot == nil || snapshot.Error != "Token expired" {
		t.Fatalf("expected token expired snapshot, got %#v", snapshot)
	}
	if got := atomic.LoadInt32(&calls); got != 0 {
		t.Fatalf("expected no HTTP call for expired token, got %d", got)
	}
}

func TestFetchUsageQuota_ExplicitAccountIDWins(t *testing.T) {
	var accountHeader string
	auth := &CodexAuth{
		httpClient: &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				accountHeader = req.Header.Get("ChatGPT-Account-Id")
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(`{"rate_limit":{}}`)),
					Header:     make(http.Header),
					Request:    req,
				}, nil
			}),
		},
	}

	snapshot, err := auth.FetchUsageQuota(context.Background(), testJWTToken("acc-from-jwt"), "", "acc-explicit")
	if err != nil {
		t.Fatalf("FetchUsageQuota() error = %v", err)
	}
	if snapshot.AccountID != "acc-explicit" {
		t.Fatalf("expected explicit account ID, got %#v", snapshot)
	}
	if accountHeader != "acc-explicit" {
		t.Fatalf("expected explicit header, got %q", accountHeader)
	}
}

func testJWTToken(accountID string) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"https://api.openai.com/auth":{"chatgpt_account_id":"` + accountID + `"}}`))
	return header + "." + payload + ".sig"
}
