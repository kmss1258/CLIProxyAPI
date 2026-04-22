// Package config provides configuration management for the CLI Proxy API server.
// It handles loading and parsing YAML configuration files, and provides structured
// access to application settings including server port, authentication directory,
// debug settings, proxy configuration, and API keys.
package config

import (
	"fmt"
	"strings"
)

// SDKConfig represents the application's configuration, loaded from a YAML file.
type SDKConfig struct {
	// ProxyURL is the URL of an optional proxy server to use for outbound requests.
	ProxyURL string `yaml:"proxy-url" json:"proxy-url"`

	// EnableGeminiCLIEndpoint controls whether Gemini CLI internal endpoints (/v1internal:*) are enabled.
	// Default is false for safety; when false, /v1internal:* requests are rejected.
	EnableGeminiCLIEndpoint bool `yaml:"enable-gemini-cli-endpoint" json:"enable-gemini-cli-endpoint"`

	// ForceModelPrefix requires explicit model prefixes (e.g., "teamA/gemini-3-pro-preview")
	// to target prefixed credentials. When false, unprefixed model requests may use prefixed
	// credentials as well.
	ForceModelPrefix bool `yaml:"force-model-prefix" json:"force-model-prefix"`

	// RequestLog enables or disables detailed request logging functionality.
	RequestLog bool `yaml:"request-log" json:"request-log"`

	// APIKeys is a list of keys for authenticating clients to this proxy server.
	APIKeys []string `yaml:"api-keys" json:"api-keys"`

	// ClientAPIKeyPolicies configures per-client API key controls.
	ClientAPIKeyPolicies []ClientAPIKeyPolicy `yaml:"client-api-key-policies,omitempty" json:"client-api-key-policies,omitempty"`

	// SQLitePromptLog configures SQLite-backed request logging.
	SQLitePromptLog SQLitePromptLogConfig `yaml:"sqlite-prompt-log,omitempty" json:"sqlite-prompt-log,omitempty"`

	// PassthroughHeaders controls whether upstream response headers are forwarded to downstream clients.
	// Default is false (disabled).
	PassthroughHeaders bool `yaml:"passthrough-headers" json:"passthrough-headers"`

	// Streaming configures server-side streaming behavior (keep-alives and safe bootstrap retries).
	Streaming StreamingConfig `yaml:"streaming" json:"streaming"`

	// NonStreamKeepAliveInterval controls how often blank lines are emitted for non-streaming responses.
	// <= 0 disables keep-alives. Value is in seconds.
	NonStreamKeepAliveInterval int `yaml:"nonstream-keepalive-interval,omitempty" json:"nonstream-keepalive-interval,omitempty"`
}

// ClientAPIKeyPolicy configures per-client API key output quota.
type ClientAPIKeyPolicy struct {
	APIKey                      string `yaml:"api-key" json:"api-key"`
	Alias                       string `yaml:"alias,omitempty" json:"alias,omitempty"`
	SelectedAuthID              string `yaml:"selected-auth-id,omitempty" json:"selected-auth-id,omitempty"`
	SelectedAuthIndex           string `yaml:"selected-auth-index,omitempty" json:"selected-auth-index,omitempty"`
	OutputTokenQuota            int64  `yaml:"output-token-quota" json:"output-token-quota"`
	OutputTokenQuotaResetHours  int    `yaml:"output-token-quota-reset-hours,omitempty" json:"output-token-quota-reset-hours,omitempty"`
	LegacyOutputTokenQuotaReset string `yaml:"output-token-quota-reset,omitempty" json:"output-token-quota-reset,omitempty"`
	Concurrency                 int    `yaml:"concurrency,omitempty" json:"concurrency,omitempty"`
}

const (
	ClientAPIKeyPolicyQuotaResetWeeklyFromFirstUse = "weekly-from-first-use"
	ClientAPIKeyPolicyQuotaResetWeeklyHours        = 168
)

func NormalizeClientAPIKeyPolicies(items []ClientAPIKeyPolicy) []ClientAPIKeyPolicy {
	if len(items) == 0 {
		return nil
	}
	result := make([]ClientAPIKeyPolicy, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		key := strings.TrimSpace(item.APIKey)
		if key == "" {
			continue
		}
		if _, exists := seen[key]; exists {
			continue
		}
		if item.OutputTokenQuota < 0 {
			item.OutputTokenQuota = 0
		}
		item.APIKey = key
		item.Alias = strings.TrimSpace(item.Alias)
		item.SelectedAuthID = strings.TrimSpace(item.SelectedAuthID)
		item.SelectedAuthIndex = strings.TrimSpace(item.SelectedAuthIndex)
		item.LegacyOutputTokenQuotaReset = strings.TrimSpace(item.LegacyOutputTokenQuotaReset)
		if item.LegacyOutputTokenQuotaReset == ClientAPIKeyPolicyQuotaResetWeeklyFromFirstUse {
			if item.OutputTokenQuotaResetHours == 0 || item.OutputTokenQuotaResetHours == ClientAPIKeyPolicyQuotaResetWeeklyHours {
				item.OutputTokenQuotaResetHours = ClientAPIKeyPolicyQuotaResetWeeklyHours
				item.LegacyOutputTokenQuotaReset = ""
			}
		}
		seen[key] = struct{}{}
		result = append(result, item)
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func ValidateClientAPIKeyPolicies(apiKeys []string, items []ClientAPIKeyPolicy) error {
	if len(items) == 0 {
		return nil
	}
	validKeys := make(map[string]struct{}, len(apiKeys))
	for _, apiKey := range apiKeys {
		key := strings.TrimSpace(apiKey)
		if key != "" {
			validKeys[key] = struct{}{}
		}
	}
	for _, item := range items {
		if _, ok := validKeys[strings.TrimSpace(item.APIKey)]; !ok {
			return fmt.Errorf("unknown api-key: %s", strings.TrimSpace(item.APIKey))
		}
		legacyReset := strings.TrimSpace(item.LegacyOutputTokenQuotaReset)
		switch legacyReset {
		case "":
		case ClientAPIKeyPolicyQuotaResetWeeklyFromFirstUse:
			if item.OutputTokenQuotaResetHours > 0 && item.OutputTokenQuotaResetHours != ClientAPIKeyPolicyQuotaResetWeeklyHours {
				return fmt.Errorf("output-token-quota-reset-hours conflicts with deprecated output-token-quota-reset")
			}
		default:
			return fmt.Errorf("invalid output-token-quota-reset: %s", legacyReset)
		}
		if item.OutputTokenQuotaResetHours < 0 {
			return fmt.Errorf("output-token-quota-reset-hours must be greater than or equal to 0")
		}
		if item.Concurrency < 0 {
			return fmt.Errorf("concurrency must be greater than or equal to 0")
		}
		if item.OutputTokenQuotaResetHours > 0 && item.OutputTokenQuota <= 0 {
			return fmt.Errorf("output-token-quota must be greater than 0 when output-token-quota-reset-hours is set")
		}
		if item.Alias != "" {
			if len(item.Alias) > 64 {
				return fmt.Errorf("alias must be 64 characters or fewer")
			}
			for _, r := range item.Alias {
				if r == '\n' || r == '\r' || r == '\t' {
					return fmt.Errorf("alias contains unsupported control characters")
				}
			}
		}
		if item.SelectedAuthIndex != "" {
			for _, r := range item.SelectedAuthIndex {
				if r == '\n' || r == '\r' || r == '\t' {
					return fmt.Errorf("selected-auth-index contains unsupported control characters")
				}
			}
		}
		if item.SelectedAuthID != "" {
			for _, r := range item.SelectedAuthID {
				if r == '\n' || r == '\r' || r == '\t' {
					return fmt.Errorf("selected-auth-id contains unsupported control characters")
				}
			}
		}
	}
	return nil
}

// SQLitePromptLogConfig configures SQLite-backed prompt/response logging.
type SQLitePromptLogConfig struct {
	Enabled          bool   `yaml:"enabled" json:"enabled"`
	Path             string `yaml:"path,omitempty" json:"path,omitempty"`
	CapturePrompts   bool   `yaml:"capture-prompts,omitempty" json:"capture-prompts,omitempty"`
	CaptureResponses bool   `yaml:"capture-responses,omitempty" json:"capture-responses,omitempty"`
	BodyMaxBytes     int    `yaml:"body-max-bytes,omitempty" json:"body-max-bytes,omitempty"`
	RetentionDays    int    `yaml:"retention-days,omitempty" json:"retention-days,omitempty"`
	HashAPIKeys      bool   `yaml:"hash-api-keys,omitempty" json:"hash-api-keys,omitempty"`
}

// StreamingConfig holds server streaming behavior configuration.
type StreamingConfig struct {
	// KeepAliveSeconds controls how often the server emits SSE heartbeats (": keep-alive\n\n").
	// <= 0 disables keep-alives. Default is 0.
	KeepAliveSeconds int `yaml:"keepalive-seconds,omitempty" json:"keepalive-seconds,omitempty"`

	// BootstrapRetries controls how many times the server may retry a streaming request before any bytes are sent,
	// to allow auth rotation / transient recovery.
	// <= 0 disables bootstrap retries. Default is 0.
	BootstrapRetries int `yaml:"bootstrap-retries,omitempty" json:"bootstrap-retries,omitempty"`
}
