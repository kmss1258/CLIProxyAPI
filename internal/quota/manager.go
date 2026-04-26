package quota

import (
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/usage"
	log "github.com/sirupsen/logrus"
)

const defaultSQLiteRelativePath = "logs/prompt_logs.sqlite"

type Status struct {
	APIKey                     string     `json:"api_key"`
	Alias                      string     `json:"alias,omitempty"`
	OutputTokenQuota           int64      `json:"output_token_quota"`
	OutputTokenQuotaResetHours int        `json:"output_token_quota_reset_hours,omitempty"`
	UsedOutputTokens           int64      `json:"used_output_tokens"`
	Remaining                  int64      `json:"remaining_output_tokens,omitempty"`
	WindowStartedAt            *time.Time `json:"window_started_at,omitempty"`
	WindowEndsAt               *time.Time `json:"window_ends_at,omitempty"`
	WindowRemainingSeconds     int64      `json:"window_remaining_seconds,omitempty"`
	Unlimited                  bool       `json:"unlimited"`
}

type policy struct {
	quota      int64
	resetHours int
	alias      string
}

type usageState struct {
	used            int64
	windowStartedAt time.Time
}

type Manager struct {
	mu       sync.RWMutex
	policies map[string]policy
	usage    map[string]usageState
	store    *SQLiteStore
	path     string
	ready    bool
	now      func() time.Time
}

var defaultManager = NewManager()

func DefaultManager() *Manager {
	return defaultManager
}

func NewManager() *Manager {
	return &Manager{
		policies: make(map[string]policy),
		usage:    make(map[string]usageState),
		now:      time.Now,
	}
}

func (m *Manager) SetNowForTest(now time.Time) {
	if m == nil {
		return
	}
	m.now = func() time.Time { return now }
}

func (m *Manager) currentTime() time.Time {
	if m == nil || m.now == nil {
		return time.Now()
	}
	return m.now()
}

func ResolveSharedSQLitePath(cfg *config.Config, configPath string) string {
	path := defaultSQLiteRelativePath
	if cfg != nil {
		if candidate := strings.TrimSpace(cfg.SQLitePromptLog.Path); candidate != "" {
			path = candidate
		}
	}
	if filepath.IsAbs(path) {
		return path
	}
	baseDir := "."
	if trimmed := strings.TrimSpace(configPath); trimmed != "" {
		baseDir = filepath.Dir(trimmed)
	}
	return filepath.Clean(filepath.Join(baseDir, path))
}

func (m *Manager) UpdateConfig(cfg *config.Config, configPath string) error {
	if m == nil {
		return nil
	}
	resolved := ResolveSharedSQLitePath(cfg, configPath)
	retentionDays := 0
	if cfg != nil {
		retentionDays = cfg.SQLitePromptLog.RetentionDays
	}
	store, err := OpenSQLiteStore(resolved, retentionDays)
	if err != nil {
		return err
	}
	usageByKey, err := store.LoadUsage()
	if err != nil {
		return err
	}
	policies := make(map[string]policy)
	if cfg != nil {
		for _, item := range cfg.ClientAPIKeyPolicies {
			key := strings.TrimSpace(item.APIKey)
			if key == "" {
				continue
			}
			quotaValue := item.OutputTokenQuota
			if quotaValue < 0 {
				quotaValue = 0
			}
			policies[key] = policy{quota: quotaValue, resetHours: item.OutputTokenQuotaResetHours, alias: strings.TrimSpace(item.Alias)}
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.store != nil && m.store != store {
		_ = m.store.Close()
	}
	m.store = store
	m.path = resolved
	m.policies = policies
	m.usage = usageByKey
	m.ready = true
	return nil
}

func (m *Manager) ApplyUsage(apiKey string, outputTokens int64) error {
	if m == nil {
		return nil
	}
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" || outputTokens <= 0 {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.store == nil {
		return nil
	}
	previousUsage := m.usage[apiKey]
	policyConfig, usage := m.effectiveStateLocked(apiKey)
	if policyConfig.resetHours > 0 {
		if usage.windowStartedAt.IsZero() {
			usage.windowStartedAt = m.currentTime().UTC()
		}
	}
	usage.used += outputTokens
	m.usage[apiKey] = usage
	if err := m.store.StoreUsage(apiKey, usage); err != nil {
		m.usage[apiKey] = previousUsage
		return err
	}
	return nil
}

func (m *Manager) Allow(apiKey string) (bool, Status) {
	status := Status{APIKey: strings.TrimSpace(apiKey), Unlimited: true}
	if m == nil || status.APIKey == "" {
		return true, status
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	policyConfig, ok := m.policies[status.APIKey]
	status.Alias = strings.TrimSpace(policyConfig.alias)
	if !ok || policyConfig.quota <= 0 {
		status.Unlimited = true
		return true, status
	}
	usage := m.effectiveUsageLocked(status.APIKey)
	used := usage.used
	status.UsedOutputTokens = used
	status.OutputTokenQuotaResetHours = policyConfig.resetHours
	if policyConfig.resetHours > 0 && !usage.windowStartedAt.IsZero() {
		windowStartedAt := usage.windowStartedAt.UTC()
		windowEndsAt := windowStartedAt.Add(time.Duration(policyConfig.resetHours) * time.Hour)
		status.WindowStartedAt = &windowStartedAt
		status.WindowEndsAt = &windowEndsAt
		remainingSeconds := int64(windowEndsAt.Sub(m.currentTime().UTC()).Seconds())
		if remainingSeconds < 0 {
			remainingSeconds = 0
		}
		status.WindowRemainingSeconds = remainingSeconds
	}
	status.Unlimited = false
	status.OutputTokenQuota = policyConfig.quota
	remaining := policyConfig.quota - used
	if remaining < 0 {
		remaining = 0
	}
	status.Remaining = remaining
	return used < policyConfig.quota, status
}

func (m *Manager) ResetUsage(apiKey string) error {
	if m == nil {
		return nil
	}
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.store == nil {
		delete(m.usage, apiKey)
		return nil
	}
	if err := m.store.ResetUsage(apiKey); err != nil {
		return err
	}
	delete(m.usage, apiKey)
	return nil
}

func (m *Manager) effectiveStateLocked(apiKey string) (policy, usageState) {
	policyConfig := m.policies[apiKey]
	return policyConfig, m.effectiveUsageLocked(apiKey)
}

func (m *Manager) effectiveUsageLocked(apiKey string) usageState {
	usage := m.usage[apiKey]
	policyConfig := m.policies[apiKey]
	if policyConfig.resetHours <= 0 {
		return usage
	}
	if usage.windowStartedAt.IsZero() {
		return usageState{}
	}
	if m.currentTime().UTC().Before(usage.windowStartedAt.Add(time.Duration(policyConfig.resetHours) * time.Hour)) {
		return usage
	}
	return usageState{}
}

func (m *Manager) Statuses(apiKeys []string) []Status {
	if m == nil {
		return nil
	}
	statuses := make([]Status, 0, len(apiKeys))
	seen := make(map[string]struct{}, len(apiKeys))
	for _, apiKey := range apiKeys {
		key := strings.TrimSpace(apiKey)
		if key == "" {
			continue
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		_, status := m.Allow(key)
		statuses = append(statuses, status)
	}
	return statuses
}

func (m *Manager) LoadRequestLogUsageSnapshot(apiKeys []string) (usage.StatisticsSnapshot, error) {
	if m == nil {
		return usage.StatisticsSnapshot{}, nil
	}
	m.mu.RLock()
	store := m.store
	m.mu.RUnlock()
	if store == nil {
		return usage.StatisticsSnapshot{}, nil
	}
	return store.LoadRequestLogUsageSnapshot(apiKeys)
}

func (m *Manager) StoreUsageSnapshot(stats *usage.RequestStatistics) error {
	if m == nil || stats == nil {
		return nil
	}
	m.mu.RLock()
	store := m.store
	m.mu.RUnlock()
	if store == nil {
		return nil
	}
	return store.StoreUsageSnapshot(stats.Snapshot())
}

func (m *Manager) LoadUsageSnapshot() (usage.StatisticsSnapshot, bool, error) {
	if m == nil {
		return usage.StatisticsSnapshot{}, false, nil
	}
	m.mu.RLock()
	store := m.store
	m.mu.RUnlock()
	if store == nil {
		return usage.StatisticsSnapshot{}, false, nil
	}
	return store.LoadUsageSnapshot()
}

func (m *Manager) RestoreUsageStatistics(stats *usage.RequestStatistics) (usage.MergeResult, bool, error) {
	if m == nil || stats == nil {
		return usage.MergeResult{}, false, nil
	}
	snapshot, ok, err := m.LoadUsageSnapshot()
	if err != nil || !ok {
		return usage.MergeResult{}, ok, err
	}
	return stats.MergeSnapshot(snapshot), true, nil
}

func (m *Manager) Close() error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.store == nil {
		return nil
	}
	err := m.store.Close()
	m.store = nil
	return err
}

func LogApplyUsageError(apiKey string, err error) {
	if err == nil {
		return
	}
	log.WithError(err).WithField("apiKey", apiKey).Warn("quota: failed to persist usage")
}
