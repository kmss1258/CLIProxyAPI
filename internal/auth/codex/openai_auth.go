// Package codex provides authentication and token management for OpenAI's Codex API.
// It handles the OAuth2 flow, including generating authorization URLs, exchanging
// authorization codes for tokens, and refreshing expired tokens. The package also
// defines data structures for storing and managing Codex authentication credentials.
package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/util"
	log "github.com/sirupsen/logrus"
)

// OAuth configuration constants for OpenAI Codex
const (
	AuthURL     = "https://auth.openai.com/oauth/authorize"
	TokenURL    = "https://auth.openai.com/oauth/token"
	ClientID    = "app_EMoamEEZ73f0CkXaXp7hrann"
	RedirectURI = "http://localhost:1455/auth/callback"
	UsageURL    = "https://chatgpt.com/backend-api/wham/usage"
)

type UsageQuotaWindow struct {
	PercentRemaining int    `json:"percent_remaining"`
	ResetTimeISO     string `json:"reset_time_iso,omitempty"`
}

type UsageQuotaSnapshot struct {
	Provider  string            `json:"provider"`
	AccountID string            `json:"account_id,omitempty"`
	Daily     *UsageQuotaWindow `json:"daily,omitempty"`
	Weekly    *UsageQuotaWindow `json:"weekly,omitempty"`
	Error     string            `json:"error,omitempty"`
}

type usageQuotaResponse struct {
	RateLimit struct {
		PrimaryWindow   *usageQuotaAPIRawWindow `json:"primary_window"`
		SecondaryWindow *usageQuotaAPIRawWindow `json:"secondary_window"`
	} `json:"rate_limit"`
}

type usageQuotaAPIRawWindow struct {
	UsedPercent      float64 `json:"used_percent"`
	ResetAt          any     `json:"reset_at"`
	ResetAfterSecond any     `json:"reset_after_seconds"`
}

// CodexAuth handles the OpenAI OAuth2 authentication flow.
// It manages the HTTP client and provides methods for generating authorization URLs,
// exchanging authorization codes for tokens, and refreshing access tokens.
type CodexAuth struct {
	httpClient *http.Client
}

// NewCodexAuth creates a new CodexAuth service instance.
// It initializes an HTTP client with proxy settings from the provided configuration.
func NewCodexAuth(cfg *config.Config) *CodexAuth {
	return &CodexAuth{
		httpClient: util.SetProxy(&cfg.SDKConfig, &http.Client{}),
	}
}

func NewCodexAuthWithHTTPClient(client *http.Client) *CodexAuth {
	if client == nil {
		client = &http.Client{}
	}
	return &CodexAuth{httpClient: client}
}

// GenerateAuthURL creates the OAuth authorization URL with PKCE (Proof Key for Code Exchange).
// It constructs the URL with the necessary parameters, including the client ID,
// response type, redirect URI, scopes, and PKCE challenge.
func (o *CodexAuth) GenerateAuthURL(state string, pkceCodes *PKCECodes) (string, error) {
	if pkceCodes == nil {
		return "", fmt.Errorf("PKCE codes are required")
	}

	params := url.Values{
		"client_id":                  {ClientID},
		"response_type":              {"code"},
		"redirect_uri":               {RedirectURI},
		"scope":                      {"openid email profile offline_access"},
		"state":                      {state},
		"code_challenge":             {pkceCodes.CodeChallenge},
		"code_challenge_method":      {"S256"},
		"prompt":                     {"login"},
		"id_token_add_organizations": {"true"},
		"codex_cli_simplified_flow":  {"true"},
	}

	authURL := fmt.Sprintf("%s?%s", AuthURL, params.Encode())
	return authURL, nil
}

// ExchangeCodeForTokens exchanges an authorization code for access and refresh tokens.
// It performs an HTTP POST request to the OpenAI token endpoint with the provided
// authorization code and PKCE verifier.
func (o *CodexAuth) ExchangeCodeForTokens(ctx context.Context, code string, pkceCodes *PKCECodes) (*CodexAuthBundle, error) {
	return o.ExchangeCodeForTokensWithRedirect(ctx, code, RedirectURI, pkceCodes)
}

// ExchangeCodeForTokensWithRedirect exchanges an authorization code for tokens using
// a caller-provided redirect URI. This supports alternate auth flows such as device
// login while preserving the existing token parsing and storage behavior.
func (o *CodexAuth) ExchangeCodeForTokensWithRedirect(ctx context.Context, code, redirectURI string, pkceCodes *PKCECodes) (*CodexAuthBundle, error) {
	if pkceCodes == nil {
		return nil, fmt.Errorf("PKCE codes are required for token exchange")
	}
	if strings.TrimSpace(redirectURI) == "" {
		return nil, fmt.Errorf("redirect URI is required for token exchange")
	}

	// Prepare token exchange request
	data := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {ClientID},
		"code":          {code},
		"redirect_uri":  {strings.TrimSpace(redirectURI)},
		"code_verifier": {pkceCodes.CodeVerifier},
	}

	req, err := http.NewRequestWithContext(ctx, "POST", TokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("failed to create token request: %w", err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := o.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token exchange request failed: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read token response: %w", err)
	}
	// log.Debugf("Token response: %s", string(body))

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token exchange failed with status %d: %s", resp.StatusCode, string(body))
	}

	// Parse token response
	var tokenResp struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		IDToken      string `json:"id_token"`
		TokenType    string `json:"token_type"`
		ExpiresIn    int    `json:"expires_in"`
	}

	if err = json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("failed to parse token response: %w", err)
	}

	// Extract account ID from ID token
	claims, err := ParseJWTToken(tokenResp.IDToken)
	if err != nil {
		log.Warnf("Failed to parse ID token: %v", err)
	}

	accountID := ""
	email := ""
	if claims != nil {
		accountID = claims.GetAccountID()
		email = claims.GetUserEmail()
	}

	// Create token data
	tokenData := CodexTokenData{
		IDToken:      tokenResp.IDToken,
		AccessToken:  tokenResp.AccessToken,
		RefreshToken: tokenResp.RefreshToken,
		AccountID:    accountID,
		Email:        email,
		Expire:       time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second).Format(time.RFC3339),
	}

	// Create auth bundle
	bundle := &CodexAuthBundle{
		TokenData:   tokenData,
		LastRefresh: time.Now().Format(time.RFC3339),
	}

	return bundle, nil
}

// RefreshTokens refreshes an access token using a refresh token.
// This method is called when an access token has expired. It makes a request to the
// token endpoint to obtain a new set of tokens.
func (o *CodexAuth) RefreshTokens(ctx context.Context, refreshToken string) (*CodexTokenData, error) {
	if refreshToken == "" {
		return nil, fmt.Errorf("refresh token is required")
	}

	data := url.Values{
		"client_id":     {ClientID},
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"scope":         {"openid profile email"},
	}

	req, err := http.NewRequestWithContext(ctx, "POST", TokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("failed to create refresh request: %w", err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := o.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token refresh request failed: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read refresh response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token refresh failed with status %d: %s", resp.StatusCode, string(body))
	}

	var tokenResp struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		IDToken      string `json:"id_token"`
		TokenType    string `json:"token_type"`
		ExpiresIn    int    `json:"expires_in"`
	}

	if err = json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("failed to parse refresh response: %w", err)
	}

	// Extract account ID from ID token
	claims, err := ParseJWTToken(tokenResp.IDToken)
	if err != nil {
		log.Warnf("Failed to parse refreshed ID token: %v", err)
	}

	accountID := ""
	email := ""
	if claims != nil {
		accountID = claims.GetAccountID()
		email = claims.Email
	}

	return &CodexTokenData{
		IDToken:      tokenResp.IDToken,
		AccessToken:  tokenResp.AccessToken,
		RefreshToken: tokenResp.RefreshToken,
		AccountID:    accountID,
		Email:        email,
		Expire:       time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second).Format(time.RFC3339),
	}, nil
}

func (o *CodexAuth) FetchUsageQuota(ctx context.Context, accessToken, expiresAt, explicitAccountID string) (*UsageQuotaSnapshot, error) {
	snapshot := &UsageQuotaSnapshot{Provider: "openai"}
	accessToken = strings.TrimSpace(accessToken)
	if accessToken == "" {
		snapshot.Error = "Missing access token"
		return snapshot, nil
	}
	if accountID := usageQuotaAccountID(accessToken, explicitAccountID); accountID != "" {
		snapshot.AccountID = accountID
	}
	if usageQuotaExpired(expiresAt) {
		snapshot.Error = "Token expired"
		return snapshot, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, UsageURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create usage quota request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("User-Agent", "OpenCode-Quota-Toast/1.0")
	if snapshot.AccountID != "" {
		req.Header.Set("ChatGPT-Account-Id", snapshot.AccountID)
	}

	resp, err := o.httpClient.Do(req)
	if err != nil {
		snapshot.Error = fmt.Sprintf("OpenAI API error: %v", err)
		return snapshot, nil
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read usage quota response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		trimmed := strings.TrimSpace(string(body))
		if len(trimmed) > 120 {
			trimmed = trimmed[:120]
		}
		if trimmed == "" {
			trimmed = resp.Status
		}
		snapshot.Error = fmt.Sprintf("OpenAI API error %d: %s", resp.StatusCode, trimmed)
		return snapshot, nil
	}

	var decoded usageQuotaResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return nil, fmt.Errorf("failed to parse usage quota response: %w", err)
	}
	snapshot.Daily = usageQuotaWindow(decoded.RateLimit.PrimaryWindow)
	snapshot.Weekly = usageQuotaWindow(decoded.RateLimit.SecondaryWindow)
	return snapshot, nil
}

// CreateTokenStorage creates a new CodexTokenStorage from a CodexAuthBundle.
// It populates the storage struct with token data, user information, and timestamps.
func (o *CodexAuth) CreateTokenStorage(bundle *CodexAuthBundle) *CodexTokenStorage {
	storage := &CodexTokenStorage{
		IDToken:      bundle.TokenData.IDToken,
		AccessToken:  bundle.TokenData.AccessToken,
		RefreshToken: bundle.TokenData.RefreshToken,
		AccountID:    bundle.TokenData.AccountID,
		LastRefresh:  bundle.LastRefresh,
		Email:        bundle.TokenData.Email,
		Expire:       bundle.TokenData.Expire,
	}

	return storage
}

// RefreshTokensWithRetry refreshes tokens with a built-in retry mechanism.
// It attempts to refresh the tokens up to a specified maximum number of retries,
// with an exponential backoff strategy to handle transient network errors.
func (o *CodexAuth) RefreshTokensWithRetry(ctx context.Context, refreshToken string, maxRetries int) (*CodexTokenData, error) {
	var lastErr error

	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			// Wait before retry
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(attempt) * time.Second):
			}
		}

		tokenData, err := o.RefreshTokens(ctx, refreshToken)
		if err == nil {
			return tokenData, nil
		}
		if isNonRetryableRefreshErr(err) {
			log.Warnf("Token refresh attempt %d failed with non-retryable error: %v", attempt+1, err)
			return nil, err
		}

		lastErr = err
		log.Warnf("Token refresh attempt %d failed: %v", attempt+1, err)
	}

	return nil, fmt.Errorf("token refresh failed after %d attempts: %w", maxRetries, lastErr)
}

func isNonRetryableRefreshErr(err error) bool {
	if err == nil {
		return false
	}
	raw := strings.ToLower(err.Error())
	return strings.Contains(raw, "refresh_token_reused")
}

func usageQuotaAccountID(accessToken, explicitAccountID string) string {
	if accountID := strings.TrimSpace(explicitAccountID); accountID != "" {
		return accountID
	}
	claims, err := ParseJWTToken(strings.TrimSpace(accessToken))
	if err != nil || claims == nil {
		return ""
	}
	return strings.TrimSpace(claims.GetAccountID())
}

func usageQuotaExpired(expiresAt string) bool {
	expiresAt = strings.TrimSpace(expiresAt)
	if expiresAt == "" {
		return false
	}
	parsed, err := time.Parse(time.RFC3339, expiresAt)
	if err != nil {
		return false
	}
	return parsed.Before(time.Now())
}

func usageQuotaWindow(window *usageQuotaAPIRawWindow) *UsageQuotaWindow {
	if window == nil {
		return nil
	}
	return &UsageQuotaWindow{
		PercentRemaining: usageQuotaPercentRemaining(window.UsedPercent),
		ResetTimeISO:     usageQuotaResetTime(window.ResetAt, window.ResetAfterSecond),
	}
}

func usageQuotaPercentRemaining(usedPercent float64) int {
	remaining := 100 - usedPercent
	if math.IsNaN(remaining) || math.IsInf(remaining, 0) {
		return 0
	}
	if remaining < 0 {
		remaining = 0
	}
	if remaining > 100 {
		remaining = 100
	}
	return int(math.Round(remaining))
}

func usageQuotaResetTime(resetAt any, resetAfterSeconds any) string {
	if ts, ok := usageQuotaAbsoluteTime(resetAt); ok {
		return ts.Format(time.RFC3339)
	}
	if seconds, ok := usageQuotaNumber(resetAfterSeconds); ok {
		return time.Now().Add(time.Duration(seconds * float64(time.Second))).UTC().Format(time.RFC3339)
	}
	return ""
}

func usageQuotaAbsoluteTime(value any) (time.Time, bool) {
	numeric, ok := usageQuotaNumber(value)
	if !ok || numeric <= 0 {
		return time.Time{}, false
	}
	if numeric > 100000000000 {
		return time.UnixMilli(int64(numeric)).UTC(), true
	}
	return time.Unix(int64(numeric), 0).UTC(), true
}

func usageQuotaNumber(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case int32:
		return float64(typed), true
	case json.Number:
		parsed, err := typed.Float64()
		if err == nil {
			return parsed, true
		}
	case string:
		trimmed := strings.TrimSpace(typed)
		if trimmed == "" {
			return 0, false
		}
		parsed, err := json.Number(trimmed).Float64()
		if err == nil {
			return parsed, true
		}
	}
	return 0, false
}

// UpdateTokenStorage updates an existing CodexTokenStorage with new token data.
// This is typically called after a successful token refresh to persist the new credentials.
func (o *CodexAuth) UpdateTokenStorage(storage *CodexTokenStorage, tokenData *CodexTokenData) {
	storage.IDToken = tokenData.IDToken
	storage.AccessToken = tokenData.AccessToken
	storage.RefreshToken = tokenData.RefreshToken
	storage.AccountID = tokenData.AccountID
	storage.LastRefresh = time.Now().Format(time.RFC3339)
	storage.Email = tokenData.Email
	storage.Expire = tokenData.Expire
}
