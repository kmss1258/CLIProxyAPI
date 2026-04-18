package config

import (
	"fmt"
	"strings"
)

type ConcurrencyConfig struct {
	Enabled   bool                       `yaml:"enabled" json:"enabled"`
	Defaults  ConcurrencyDefaults        `yaml:"defaults" json:"defaults"`
	Endpoints []EndpointConcurrencyLimit `yaml:"endpoints" json:"endpoints"`
}

type ConcurrencyDefaults struct {
	GlobalInFlight  int `yaml:"global-inflight" json:"global-inflight"`
	ClientAPIKey    int `yaml:"client-api-key" json:"client-api-key"`
	UpstreamAccount int `yaml:"upstream-account" json:"upstream-account"`
}

type EndpointConcurrencyLimit struct {
	Path     string `yaml:"path" json:"path"`
	InFlight int    `yaml:"inflight" json:"inflight"`
}

func NormalizeConcurrencyConfig(cfg ConcurrencyConfig) ConcurrencyConfig {
	if len(cfg.Endpoints) == 0 {
		cfg.Endpoints = nil
		return cfg
	}
	result := make([]EndpointConcurrencyLimit, 0, len(cfg.Endpoints))
	for _, item := range cfg.Endpoints {
		item.Path = strings.TrimSpace(item.Path)
		result = append(result, item)
	}
	cfg.Endpoints = result
	return cfg
}

func ValidateConcurrencyConfig(cfg ConcurrencyConfig) error {
	if cfg.Defaults.GlobalInFlight < 0 {
		return fmt.Errorf("concurrency.defaults.global-inflight must be greater than or equal to 0")
	}
	if cfg.Defaults.ClientAPIKey < 0 {
		return fmt.Errorf("concurrency.defaults.client-api-key must be greater than or equal to 0")
	}
	if cfg.Defaults.UpstreamAccount < 0 {
		return fmt.Errorf("concurrency.defaults.upstream-account must be greater than or equal to 0")
	}
	seen := make(map[string]struct{}, len(cfg.Endpoints))
	for _, item := range cfg.Endpoints {
		path := strings.TrimSpace(item.Path)
		if path == "" {
			return fmt.Errorf("concurrency.endpoints.path must not be empty")
		}
		if item.InFlight < 0 {
			return fmt.Errorf("concurrency.endpoints.inflight must be greater than or equal to 0")
		}
		if _, exists := seen[path]; exists {
			return fmt.Errorf("duplicate concurrency endpoint path: %s", path)
		}
		seen[path] = struct{}{}
	}
	return nil
}

func ValidateProviderConcurrency(cfg *Config) error {
	if cfg == nil {
		return nil
	}
	for _, item := range cfg.ClaudeKey {
		if item.Concurrency < 0 {
			return fmt.Errorf("claude-api-key concurrency must be greater than or equal to 0")
		}
	}
	for _, item := range cfg.CodexKey {
		if item.Concurrency < 0 {
			return fmt.Errorf("codex-api-key concurrency must be greater than or equal to 0")
		}
	}
	for _, item := range cfg.GeminiKey {
		if item.Concurrency < 0 {
			return fmt.Errorf("gemini-api-key concurrency must be greater than or equal to 0")
		}
	}
	for _, item := range cfg.VertexCompatAPIKey {
		if item.Concurrency < 0 {
			return fmt.Errorf("vertex-api-key concurrency must be greater than or equal to 0")
		}
	}
	for _, provider := range cfg.OpenAICompatibility {
		for _, item := range provider.APIKeyEntries {
			if item.Concurrency < 0 {
				return fmt.Errorf("openai-compatibility api-key-entries concurrency must be greater than or equal to 0")
			}
		}
	}
	return nil
}
