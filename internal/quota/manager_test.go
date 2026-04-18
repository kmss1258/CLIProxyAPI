package quota

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
)

func TestManagerAllowApplyAndReset(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	manager := NewManager()
	cfg := &config.Config{}
	cfg.SQLitePromptLog.Path = filepath.Join(tmpDir, "quota.sqlite")
	cfg.ClientAPIKeyPolicies = []config.ClientAPIKeyPolicy{{APIKey: "user-key", OutputTokenQuota: 10}}
	if err := manager.UpdateConfig(cfg, configPath); err != nil {
		t.Fatalf("UpdateConfig() error = %v", err)
	}
	allowed, status := manager.Allow("user-key")
	if !allowed || status.Remaining != 10 {
		t.Fatalf("expected initial allow with 10 remaining, got allowed=%v status=%+v", allowed, status)
	}
	if err := manager.ApplyUsage("user-key", 4); err != nil {
		t.Fatalf("ApplyUsage(4) error = %v", err)
	}
	allowed, status = manager.Allow("user-key")
	if !allowed || status.Remaining != 6 || status.UsedOutputTokens != 4 {
		t.Fatalf("expected allow with 6 remaining, got allowed=%v status=%+v", allowed, status)
	}
	if err := manager.ApplyUsage("user-key", 6); err != nil {
		t.Fatalf("ApplyUsage(6) error = %v", err)
	}
	allowed, status = manager.Allow("user-key")
	if allowed || status.Remaining != 0 || status.UsedOutputTokens != 10 {
		t.Fatalf("expected exhausted key, got allowed=%v status=%+v", allowed, status)
	}
	if err := manager.ResetUsage("user-key"); err != nil {
		t.Fatalf("ResetUsage() error = %v", err)
	}
	allowed, status = manager.Allow("user-key")
	if !allowed || status.UsedOutputTokens != 0 || status.Remaining != 10 {
		t.Fatalf("expected reset state, got allowed=%v status=%+v", allowed, status)
	}
}

func TestManagerUnlimitedWithoutPolicy(t *testing.T) {
	manager := NewManager()
	allowed, status := manager.Allow("free-key")
	if !allowed || !status.Unlimited {
		t.Fatalf("expected unlimited key to be allowed, got allowed=%v status=%+v", allowed, status)
	}
}

func TestManagerResetHoursResetsAnchoredWindowAfterBoundary(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	manager := NewManager()
	baseTime := time.Date(2026, time.April, 1, 0, 0, 0, 0, time.UTC)
	manager.now = func() time.Time { return baseTime }
	cfg := &config.Config{}
	cfg.SQLitePromptLog.Path = filepath.Join(tmpDir, "quota.sqlite")
	cfg.ClientAPIKeyPolicies = []config.ClientAPIKeyPolicy{{
		APIKey:                     "window-key",
		OutputTokenQuota:           10,
		OutputTokenQuotaResetHours: 24,
	}}
	if err := manager.UpdateConfig(cfg, configPath); err != nil {
		t.Fatalf("UpdateConfig() error = %v", err)
	}
	if err := manager.ApplyUsage("window-key", 10); err != nil {
		t.Fatalf("ApplyUsage() error = %v", err)
	}
	allowed, status := manager.Allow("window-key")
	if allowed || status.Remaining != 0 || status.UsedOutputTokens != 10 || status.OutputTokenQuotaResetHours != 24 || status.WindowStartedAt == nil || status.WindowEndsAt == nil {
		t.Fatalf("expected exhausted reset-hours key, got allowed=%v status=%+v", allowed, status)
	}
	manager.now = func() time.Time { return baseTime.Add(23 * time.Hour) }
	allowed, status = manager.Allow("window-key")
	if allowed || status.Remaining != 0 {
		t.Fatalf("expected key still exhausted before reset boundary, got allowed=%v status=%+v", allowed, status)
	}
	manager.now = func() time.Time { return baseTime.Add(25 * time.Hour) }
	allowed, status = manager.Allow("window-key")
	if !allowed || status.Remaining != 10 || status.UsedOutputTokens != 0 || status.WindowStartedAt != nil || status.WindowEndsAt != nil {
		t.Fatalf("expected reset after boundary, got allowed=%v status=%+v", allowed, status)
	}
	if err := manager.ApplyUsage("window-key", 3); err != nil {
		t.Fatalf("ApplyUsage() after reset error = %v", err)
	}
	allowed, status = manager.Allow("window-key")
	if !allowed || status.Remaining != 7 || status.UsedOutputTokens != 3 || status.WindowStartedAt == nil || status.WindowEndsAt == nil {
		t.Fatalf("expected new reset-hours window usage, got allowed=%v status=%+v", allowed, status)
	}
	managerReloaded := NewManager()
	managerReloaded.now = manager.now
	if err := managerReloaded.UpdateConfig(cfg, configPath); err != nil {
		t.Fatalf("reloaded UpdateConfig() error = %v", err)
	}
	allowed, status = managerReloaded.Allow("window-key")
	if !allowed || status.Remaining != 7 || status.UsedOutputTokens != 3 {
		t.Fatalf("expected persisted reset-hours state after reload, got allowed=%v status=%+v", allowed, status)
	}

	manager168 := NewManager()
	manager168.now = func() time.Time { return baseTime }
	cfg168 := &config.Config{}
	cfg168.SQLitePromptLog.Path = filepath.Join(tmpDir, "quota-168.sqlite")
	cfg168.ClientAPIKeyPolicies = []config.ClientAPIKeyPolicy{{
		APIKey:                     "weekly-parity-key",
		OutputTokenQuota:           10,
		OutputTokenQuotaResetHours: config.ClientAPIKeyPolicyQuotaResetWeeklyHours,
	}}
	if err := manager168.UpdateConfig(cfg168, filepath.Join(tmpDir, "config-168.yaml")); err != nil {
		t.Fatalf("UpdateConfig(168) error = %v", err)
	}
	if err := manager168.ApplyUsage("weekly-parity-key", 10); err != nil {
		t.Fatalf("ApplyUsage(168) error = %v", err)
	}
	manager168.now = func() time.Time { return baseTime.Add(6 * 24 * time.Hour) }
	allowed, status = manager168.Allow("weekly-parity-key")
	if allowed || status.Remaining != 0 {
		t.Fatalf("expected 168-hour policy to remain exhausted before seven days, got allowed=%v status=%+v", allowed, status)
	}
	manager168.now = func() time.Time { return baseTime.Add(8 * 24 * time.Hour) }
	allowed, status = manager168.Allow("weekly-parity-key")
	if !allowed || status.Remaining != 10 || status.UsedOutputTokens != 0 {
		t.Fatalf("expected 168-hour policy to match weekly behavior after boundary, got allowed=%v status=%+v", allowed, status)
	}
}
