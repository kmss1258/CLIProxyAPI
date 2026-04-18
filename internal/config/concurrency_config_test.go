package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateConcurrencyConfigRejectsDuplicateEndpointPath(t *testing.T) {
	cfg := ConcurrencyConfig{
		Endpoints: []EndpointConcurrencyLimit{{Path: "/v1/chat/completions", InFlight: 1}, {Path: "/v1/chat/completions", InFlight: 2}},
	}
	if err := ValidateConcurrencyConfig(cfg); err == nil {
		t.Fatalf("expected duplicate endpoint path to be rejected")
	}
}

func TestLoadConfigOptionalRejectsEmptyConcurrencyEndpointPath(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	configYAML := []byte(`
concurrency:
  enabled: true
  endpoints:
    - path: "   "
      inflight: 10
`)
	if err := os.WriteFile(configPath, configYAML, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if _, err := LoadConfigOptional(configPath, false); err == nil {
		t.Fatalf("expected empty concurrency endpoint path to be rejected")
	}
}

func TestLoadConfigOptionalRejectsNegativeClientPolicyConcurrency(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	configYAML := []byte(`
api-keys:
  - known-key
client-api-key-policies:
  - api-key: known-key
    concurrency: -1
`)
	if err := os.WriteFile(configPath, configYAML, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if _, err := LoadConfigOptional(configPath, false); err == nil {
		t.Fatalf("expected negative client policy concurrency to be rejected")
	}
}

func TestLoadConfigOptionalRejectsNegativeProviderConcurrency(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	configYAML := []byte(`
claude-api-key:
  - api-key: test-key
    base-url: https://api.anthropic.com
    concurrency: -1
    models:
      - name: claude-sonnet-4
        alias: claude-sonnet-4
`)
	if err := os.WriteFile(configPath, configYAML, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if _, err := LoadConfigOptional(configPath, false); err == nil {
		t.Fatalf("expected negative provider concurrency to be rejected")
	}
}

func TestLoadConfigOptionalAcceptsValidConcurrencyConfig(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	configYAML := []byte(`
api-keys:
  - known-key
concurrency:
  enabled: true
  defaults:
    global-inflight: 200
    client-api-key: 8
    upstream-account: 2
  endpoints:
    - path: /v1/chat/completions
      inflight: 40
client-api-key-policies:
  - api-key: known-key
    concurrency: 4
gemini-api-key:
  - api-key: gem-key
    concurrency: 3
    models:
      - name: gemini-1.5-pro
        alias: gemini-1.5-pro
`)
	if err := os.WriteFile(configPath, configYAML, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	cfg, err := LoadConfigOptional(configPath, false)
	if err != nil {
		t.Fatalf("LoadConfigOptional() error = %v", err)
	}
	if !cfg.Concurrency.Enabled {
		t.Fatalf("expected concurrency.enabled to be true")
	}
	if got := cfg.Concurrency.Defaults.ClientAPIKey; got != 8 {
		t.Fatalf("expected client-api-key default 8, got %d", got)
	}
	if got := cfg.ClientAPIKeyPolicies[0].Concurrency; got != 4 {
		t.Fatalf("expected client policy concurrency 4, got %d", got)
	}
	if got := cfg.GeminiKey[0].Concurrency; got != 3 {
		t.Fatalf("expected gemini key concurrency 3, got %d", got)
	}
}
