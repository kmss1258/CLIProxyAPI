package concurrency

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
)

const (
	scopeGlobalPrefix   = "global"
	scopeEndpointPrefix = "endpoint:"
	scopeClientPrefix   = "client:"
	scopeUpstreamPrefix = "upstream:"
)

type LimitExceeded struct {
	Scope    string
	Path     string
	APIKey   string
	AuthID   string
	Limit    int
	InFlight int
	Code     string
}

type StatusSnapshot struct {
	GlobalInFlight   int
	Endpoints        map[string]int
	ClientAPIKeys    map[string]int
	UpstreamAccounts map[string]int
}

type Manager struct {
	mu     sync.Mutex
	cfg    *config.Config
	counts map[string]int
}

var defaultManager = NewManager()

func DefaultManager() *Manager {
	return defaultManager
}

func NewManager() *Manager {
	return &Manager{counts: make(map[string]int)}
}

func (m *Manager) UpdateConfig(cfg *config.Config) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cfg = cfg
	if m.counts == nil {
		m.counts = make(map[string]int)
	}
}

func (m *Manager) Current(scopeKey string) int {
	if m == nil {
		return 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.counts[scopeKey]
}

func (m *Manager) Snapshot() StatusSnapshot {
	if m == nil {
		return StatusSnapshot{Endpoints: map[string]int{}, ClientAPIKeys: map[string]int{}, UpstreamAccounts: map[string]int{}}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	snapshot := StatusSnapshot{
		Endpoints:        map[string]int{},
		ClientAPIKeys:    map[string]int{},
		UpstreamAccounts: map[string]int{},
	}
	for key, count := range m.counts {
		if count <= 0 {
			continue
		}
		switch {
		case key == scopeGlobalPrefix:
			snapshot.GlobalInFlight = count
		case strings.HasPrefix(key, scopeEndpointPrefix):
			snapshot.Endpoints[strings.TrimPrefix(key, scopeEndpointPrefix)] = count
		case strings.HasPrefix(key, scopeClientPrefix):
			snapshot.ClientAPIKeys[strings.TrimPrefix(key, scopeClientPrefix)] = count
		case strings.HasPrefix(key, scopeUpstreamPrefix):
			snapshot.UpstreamAccounts[strings.TrimPrefix(key, scopeUpstreamPrefix)] = count
		}
	}
	return snapshot
}

func (m *Manager) Acquire(scopeKey string, limit int) (func(), bool) {
	if m == nil || limit <= 0 {
		return func() {}, true
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.counts == nil {
		m.counts = make(map[string]int)
	}
	if m.counts[scopeKey] >= limit {
		return func() {}, false
	}
	m.counts[scopeKey]++
	var once sync.Once
	return func() {
		once.Do(func() {
			m.mu.Lock()
			defer m.mu.Unlock()
			if current := m.counts[scopeKey]; current <= 1 {
				delete(m.counts, scopeKey)
			} else {
				m.counts[scopeKey] = current - 1
			}
		})
	}, true
}

func (m *Manager) AcquireHTTP(apiKey, path string) (func(), *LimitExceeded) {
	if m == nil {
		return func() {}, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cfg == nil || !m.cfg.Concurrency.Enabled {
		return func() {}, nil
	}
	keys := make([]string, 0, 3)
	limits := make([]int, 0, 3)
	trimmedPath := strings.TrimSpace(path)
	trimmedAPIKey := strings.TrimSpace(apiKey)
	if limit := m.cfg.Concurrency.Defaults.GlobalInFlight; limit > 0 {
		keys = append(keys, scopeGlobalPrefix)
		limits = append(limits, limit)
	}
	if limit := resolveEndpointLimit(m.cfg, trimmedPath); limit > 0 {
		keys = append(keys, endpointScopeKey(trimmedPath))
		limits = append(limits, limit)
	}
	if limit := resolveClientAPIKeyLimit(m.cfg, trimmedAPIKey); limit > 0 {
		keys = append(keys, clientScopeKey(trimmedAPIKey))
		limits = append(limits, limit)
	}
	if len(keys) == 0 {
		return func() {}, nil
	}
	for idx, key := range keys {
		current := m.counts[key]
		if current >= limits[idx] {
			return func() {}, m.limitExceededForKey(key, trimmedPath, trimmedAPIKey, limits[idx], current)
		}
	}
	for _, key := range keys {
		m.counts[key]++
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			m.mu.Lock()
			defer m.mu.Unlock()
			for _, key := range keys {
				if current := m.counts[key]; current <= 1 {
					delete(m.counts, key)
				} else {
					m.counts[key] = current - 1
				}
			}
		})
	}, nil
}

func (m *Manager) AcquireUpstream(authID string, limit int) (func(), *LimitExceeded) {
	trimmedAuthID := strings.TrimSpace(authID)
	if trimmedAuthID == "" || limit <= 0 {
		return func() {}, nil
	}
	release, acquired := m.Acquire(upstreamScopeKey(trimmedAuthID), limit)
	if acquired {
		return release, nil
	}
	inflight := m.Current(upstreamScopeKey(trimmedAuthID))
	return func() {}, &LimitExceeded{Scope: "upstream-account", AuthID: trimmedAuthID, Limit: limit, InFlight: inflight, Code: "upstream_account_concurrency_limit_exceeded"}
}

func (m *Manager) limitExceededForKey(scopeKey, path, apiKey string, limit, inflight int) *LimitExceeded {
	switch {
	case scopeKey == scopeGlobalPrefix:
		return &LimitExceeded{Scope: "global", Limit: limit, InFlight: inflight, Code: "global_concurrency_limit_exceeded"}
	case strings.HasPrefix(scopeKey, scopeEndpointPrefix):
		return &LimitExceeded{Scope: "endpoint", Path: path, Limit: limit, InFlight: inflight, Code: "endpoint_concurrency_limit_exceeded"}
	case strings.HasPrefix(scopeKey, scopeClientPrefix):
		return &LimitExceeded{Scope: "client-api-key", APIKey: apiKey, Limit: limit, InFlight: inflight, Code: "client_concurrency_limit_exceeded"}
	default:
		return &LimitExceeded{Scope: scopeKey, Limit: limit, InFlight: inflight, Code: "concurrency_limit_exceeded"}
	}
}

func endpointScopeKey(path string) string {
	return scopeEndpointPrefix + strings.TrimSpace(path)
}

func clientScopeKey(apiKey string) string {
	return scopeClientPrefix + strings.TrimSpace(apiKey)
}

func upstreamScopeKey(authID string) string {
	return scopeUpstreamPrefix + strings.TrimSpace(authID)
}

func resolveEndpointLimit(cfg *config.Config, path string) int {
	if cfg == nil {
		return 0
	}
	path = strings.TrimSpace(path)
	for _, item := range cfg.Concurrency.Endpoints {
		if strings.TrimSpace(item.Path) == path {
			return item.InFlight
		}
	}
	return 0
}

func resolveClientAPIKeyLimit(cfg *config.Config, apiKey string) int {
	if cfg == nil {
		return 0
	}
	apiKey = strings.TrimSpace(apiKey)
	for _, item := range cfg.ClientAPIKeyPolicies {
		if strings.TrimSpace(item.APIKey) == apiKey && item.Concurrency > 0 {
			return item.Concurrency
		}
	}
	return cfg.Concurrency.Defaults.ClientAPIKey
}

func ResolveUpstreamLimit(cfg *config.Config, provider, authID string) int {
	if cfg == nil {
		return 0
	}
	authID = strings.TrimSpace(authID)
	if authID == "" {
		return cfg.Concurrency.Defaults.UpstreamAccount
	}
	if limit, ok := buildUpstreamLimitMap(cfg)[authID]; ok && limit > 0 {
		return limit
	}
	return cfg.Concurrency.Defaults.UpstreamAccount
}

func buildUpstreamLimitMap(cfg *config.Config) map[string]int {
	if cfg == nil {
		return map[string]int{}
	}
	idGen := newStableIDGenerator()
	limits := map[string]int{}
	for _, entry := range cfg.GeminiKey {
		if entry.Concurrency <= 0 {
			continue
		}
		id, _ := idGen.Next("gemini:apikey", entry.APIKey, entry.BaseURL)
		limits[id] = entry.Concurrency
	}
	for _, entry := range cfg.ClaudeKey {
		if entry.Concurrency <= 0 {
			continue
		}
		id, _ := idGen.Next("claude:apikey", entry.APIKey, entry.BaseURL)
		limits[id] = entry.Concurrency
	}
	for _, entry := range cfg.CodexKey {
		if entry.Concurrency <= 0 {
			continue
		}
		id, _ := idGen.Next("codex:apikey", entry.APIKey, entry.BaseURL)
		limits[id] = entry.Concurrency
	}
	for _, compat := range cfg.OpenAICompatibility {
		providerName := strings.ToLower(strings.TrimSpace(compat.Name))
		if providerName == "" {
			providerName = "openai-compatibility"
		}
		base := strings.TrimSpace(compat.BaseURL)
		idKind := fmt.Sprintf("openai-compatibility:%s", providerName)
		for _, entry := range compat.APIKeyEntries {
			if entry.Concurrency <= 0 {
				continue
			}
			id, _ := idGen.Next(idKind, entry.APIKey, base, entry.ProxyURL)
			limits[id] = entry.Concurrency
		}
	}
	for _, entry := range cfg.VertexCompatAPIKey {
		if entry.Concurrency <= 0 {
			continue
		}
		id, _ := idGen.Next("vertex:apikey", entry.APIKey, entry.BaseURL, entry.ProxyURL)
		limits[id] = entry.Concurrency
	}
	return limits
}

type stableIDGenerator struct {
	counters map[string]int
}

func newStableIDGenerator() *stableIDGenerator {
	return &stableIDGenerator{counters: make(map[string]int)}
}

func (g *stableIDGenerator) Next(kind string, parts ...string) (string, string) {
	if g == nil {
		return kind + ":000000000000", "000000000000"
	}
	hasher := sha256.New()
	hasher.Write([]byte(kind))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		hasher.Write([]byte{0})
		hasher.Write([]byte(trimmed))
	}
	digest := hex.EncodeToString(hasher.Sum(nil))
	if len(digest) < 12 {
		digest = fmt.Sprintf("%012s", digest)
	}
	short := digest[:12]
	key := kind + ":" + short
	index := g.counters[key]
	g.counters[key] = index + 1
	if index > 0 {
		short = fmt.Sprintf("%s-%d", short, index)
	}
	return fmt.Sprintf("%s:%s", kind, short), short
}

func (e *LimitExceeded) Error() string {
	if e == nil {
		return ""
	}
	if e.Scope == "endpoint" && e.Path != "" {
		return fmt.Sprintf("%s: %s", e.Code, e.Path)
	}
	if e.Scope == "client-api-key" && e.APIKey != "" {
		return fmt.Sprintf("%s: %s", e.Code, e.APIKey)
	}
	if e.Scope == "upstream-account" && e.AuthID != "" {
		return fmt.Sprintf("%s: %s", e.Code, e.AuthID)
	}
	return e.Code
}
