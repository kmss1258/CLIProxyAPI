package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNormalizeClientAPIKeyPoliciesTrimsAlias(t *testing.T) {
	items := NormalizeClientAPIKeyPolicies([]ClientAPIKeyPolicy{{APIKey: "known-key", Alias: "  My Key  "}})
	if len(items) != 1 {
		t.Fatalf("expected one normalized policy, got %d", len(items))
	}
	if got := items[0].Alias; got != "My Key" {
		t.Fatalf("expected trimmed alias, got %q", got)
	}
}

func TestNormalizeClientAPIKeyPoliciesTrimsSelectedAuthIndex(t *testing.T) {
	items := NormalizeClientAPIKeyPolicies([]ClientAPIKeyPolicy{{APIKey: "known-key", SelectedAuthIndex: "  auth-123  "}})
	if len(items) != 1 {
		t.Fatalf("expected one normalized policy, got %d", len(items))
	}
	if got := items[0].SelectedAuthIndex; got != "auth-123" {
		t.Fatalf("expected trimmed selected auth index, got %q", got)
	}
}

func TestNormalizeClientAPIKeyPoliciesTrimsSelectedAuthID(t *testing.T) {
	items := NormalizeClientAPIKeyPolicies([]ClientAPIKeyPolicy{{APIKey: "known-key", SelectedAuthID: "  auth-id-123  "}})
	if len(items) != 1 {
		t.Fatalf("expected one normalized policy, got %d", len(items))
	}
	if got := items[0].SelectedAuthID; got != "auth-id-123" {
		t.Fatalf("expected trimmed selected auth id, got %q", got)
	}
}

func TestValidateClientAPIKeyPoliciesRejectsLongAlias(t *testing.T) {
	items := []ClientAPIKeyPolicy{{APIKey: "known-key", Alias: "12345678901234567890123456789012345678901234567890123456789012345"}}
	if err := ValidateClientAPIKeyPolicies([]string{"known-key"}, items); err == nil {
		t.Fatalf("expected long alias to be rejected")
	}
}

func TestValidateClientAPIKeyPoliciesRejectsSelectedAuthIndexControlCharacters(t *testing.T) {
	items := []ClientAPIKeyPolicy{{APIKey: "known-key", SelectedAuthIndex: "auth\n-123"}}
	if err := ValidateClientAPIKeyPolicies([]string{"known-key"}, items); err == nil {
		t.Fatalf("expected selected auth index with control characters to be rejected")
	}
}

func TestValidateClientAPIKeyPoliciesRejectsSelectedAuthIDControlCharacters(t *testing.T) {
	items := []ClientAPIKeyPolicy{{APIKey: "known-key", SelectedAuthID: "auth\n-id-123"}}
	if err := ValidateClientAPIKeyPolicies([]string{"known-key"}, items); err == nil {
		t.Fatalf("expected selected auth id with control characters to be rejected")
	}
}

func TestLoadConfigOptionalRejectsInvalidClientAPIKeyPolicyReset(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	configYAML := []byte(`
api-keys:
  - known-key
client-api-key-policies:
  - api-key: known-key
    output-token-quota: 100
    output-token-quota-reset: monthly
`)
	if err := os.WriteFile(configPath, configYAML, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if _, err := LoadConfigOptional(configPath, false); err == nil {
		t.Fatalf("expected invalid reset mode to be rejected")
	}
}

func TestLoadConfigOptionalRejectsResetHoursWithoutQuota(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	configYAML := []byte(`
api-keys:
  - known-key
client-api-key-policies:
  - api-key: known-key
    output-token-quota: 0
    output-token-quota-reset-hours: 24
`)
	if err := os.WriteFile(configPath, configYAML, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if _, err := LoadConfigOptional(configPath, false); err == nil {
		t.Fatalf("expected reset hours without positive quota to be rejected")
	}
}

func TestLoadConfigOptionalMapsDeprecatedWeeklyResetToHours(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	configYAML := []byte(`
api-keys:
  - known-key
client-api-key-policies:
  - api-key: known-key
    output-token-quota: 100
    output-token-quota-reset: weekly-from-first-use
`)
	if err := os.WriteFile(configPath, configYAML, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	cfg, err := LoadConfigOptional(configPath, false)
	if err != nil {
		t.Fatalf("LoadConfigOptional() error = %v", err)
	}
	if got := cfg.ClientAPIKeyPolicies[0].OutputTokenQuotaResetHours; got != ClientAPIKeyPolicyQuotaResetWeeklyHours {
		t.Fatalf("expected deprecated weekly reset to map to %d hours, got %d", ClientAPIKeyPolicyQuotaResetWeeklyHours, got)
	}
	if got := cfg.ClientAPIKeyPolicies[0].LegacyOutputTokenQuotaReset; got != "" {
		t.Fatalf("expected deprecated reset field to be cleared after normalization, got %q", got)
	}
}

func TestLoadConfigOptionalRejectsConflictingDeprecatedAndHourReset(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	configYAML := []byte(`
api-keys:
  - known-key
client-api-key-policies:
  - api-key: known-key
    output-token-quota: 100
    output-token-quota-reset: weekly-from-first-use
    output-token-quota-reset-hours: 24
`)
	if err := os.WriteFile(configPath, configYAML, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if _, err := LoadConfigOptional(configPath, false); err == nil {
		t.Fatalf("expected conflicting deprecated and hour reset values to be rejected")
	}
}

func TestLoadConfigOptionalPreservesSelectedAuthIndex(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	configYAML := []byte(`
api-keys:
  - known-key
client-api-key-policies:
  - api-key: known-key
    selected-auth-index: auth-123
`)
	if err := os.WriteFile(configPath, configYAML, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	cfg, err := LoadConfigOptional(configPath, false)
	if err != nil {
		t.Fatalf("LoadConfigOptional() error = %v", err)
	}
	if len(cfg.ClientAPIKeyPolicies) != 1 {
		t.Fatalf("expected one policy, got %d", len(cfg.ClientAPIKeyPolicies))
	}
	if got := cfg.ClientAPIKeyPolicies[0].SelectedAuthIndex; got != "auth-123" {
		t.Fatalf("expected selected auth index to persist, got %q", got)
	}
}

func TestLoadConfigOptionalPreservesSelectedAuthIDAndIndex(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	configYAML := []byte(`
api-keys:
  - known-key
client-api-key-policies:
  - api-key: known-key
    selected-auth-id: auth-id-123
    selected-auth-index: auth-123
`)
	if err := os.WriteFile(configPath, configYAML, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	cfg, err := LoadConfigOptional(configPath, false)
	if err != nil {
		t.Fatalf("LoadConfigOptional() error = %v", err)
	}
	if len(cfg.ClientAPIKeyPolicies) != 1 {
		t.Fatalf("expected one policy, got %d", len(cfg.ClientAPIKeyPolicies))
	}
	if got := cfg.ClientAPIKeyPolicies[0].SelectedAuthID; got != "auth-id-123" {
		t.Fatalf("expected selected auth id to persist, got %q", got)
	}
	if got := cfg.ClientAPIKeyPolicies[0].SelectedAuthIndex; got != "auth-123" {
		t.Fatalf("expected selected auth index to persist, got %q", got)
	}
}
