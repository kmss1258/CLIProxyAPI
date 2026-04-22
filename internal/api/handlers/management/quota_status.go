package management

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/auth/codex"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/quota"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/usage"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	"github.com/tidwall/gjson"
)

var newCodexAuth = codex.NewCodexAuth

const (
	openAIQuotaSuccessTTL   = 2 * time.Minute
	openAIQuotaErrorTTL     = 30 * time.Second
	openAIQuotaFetchTimeout = 4 * time.Second
)

type cachedOpenAIQuota struct {
	snapshot   *codex.UsageQuotaSnapshot
	fetchedAt  time.Time
	expiresAt  time.Time
	refreshing bool
}

func (h *Handler) GetQuotaStatus(c *gin.Context) {
	payload, err := h.buildQuotaStatusPayload(&quotaViewer{CanManage: true, Scope: "management"})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, payload)
}

func (h *Handler) GetQuotaStatusViewer(c *gin.Context) {
	viewer, status, message := h.resolveQuotaViewer(c)
	if viewer == nil {
		c.JSON(status, gin.H{"error": message})
		return
	}
	payload, err := h.buildQuotaStatusPayload(viewer)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, payload)
}

func (h *Handler) buildQuotaStatusPayload(viewer *quotaViewer) (gin.H, error) {
	var snapshot usage.StatisticsSnapshot
	var usageSnapshot any = gin.H{}
	apiKeyAliases := h.clientAPIKeyAliases()
	if h != nil && h.usageStats != nil {
		snapshot = h.usageStats.Snapshot()
		if shouldFallbackUsageSnapshot(snapshot) {
			snapshot = h.sqliteUsageSnapshotOr(snapshot)
		}
		usageSnapshot = gin.H{
			"total_requests":   snapshot.TotalRequests,
			"success_count":    snapshot.SuccessCount,
			"failure_count":    snapshot.FailureCount,
			"total_tokens":     snapshot.TotalTokens,
			"apis":             snapshot.APIs,
			"requests_by_day":  snapshot.RequestsByDay,
			"requests_by_hour": snapshot.RequestsByHour,
			"tokens_by_day":    snapshot.TokensByDay,
			"tokens_by_hour":   snapshot.TokensByHour,
		}
	}

	quotaStatuses := []any{}
	if h != nil && h.quotaManager != nil && h.cfg != nil {
		for _, status := range h.quotaManager.Statuses(h.cfg.APIKeys) {
			quotaStatuses = append(quotaStatuses, status)
		}
	}
	if viewer != nil && !viewer.CanManage {
		snapshot = filterUsageSnapshotToAPIKey(snapshot, viewer.APIKey)
		usageSnapshot = gin.H{
			"total_requests":   snapshot.TotalRequests,
			"success_count":    snapshot.SuccessCount,
			"failure_count":    snapshot.FailureCount,
			"total_tokens":     snapshot.TotalTokens,
			"apis":             snapshot.APIs,
			"requests_by_day":  snapshot.RequestsByDay,
			"requests_by_hour": snapshot.RequestsByHour,
			"tokens_by_day":    snapshot.TokensByDay,
			"tokens_by_hour":   snapshot.TokensByHour,
		}
		quotaStatuses = filterQuotaStatusesToAPIKey(quotaStatuses, viewer.APIKey)
		apiKeyAliases = filterAPIKeyAliases(apiKeyAliases, viewer.APIKey)
		return gin.H{
			"usage":                 usageSnapshot,
			"client_api_key_quotas": quotaStatuses,
			"api_key_aliases":       apiKeyAliases,
			"auth_files":            []gin.H{},
			"auth_usage":            []gin.H{},
			"viewer": gin.H{
				"can_manage": false,
				"scope":      viewer.Scope,
				"api_key":    viewer.APIKey,
				"alias":      apiKeyAliases[viewer.APIKey],
			},
		}, nil
	}

	authFiles, err := h.collectAuthFileEntries()
	if err != nil {
		return nil, err
	}
	authUsage := buildAuthUsage(snapshot)
	enrichAuthFilesWithUsage(authFiles, authUsage)
	h.enrichAuthFilesWithOpenAIQuota(authFiles)
	quotaStatuses = h.enrichQuotaStatusesWithSelectedAuth(quotaStatuses, authFiles)

	return gin.H{
		"usage":                 usageSnapshot,
		"client_api_key_quotas": quotaStatuses,
		"api_key_aliases":       apiKeyAliases,
		"auth_files":            authFiles,
		"auth_usage":            authUsage,
		"viewer": gin.H{
			"can_manage": true,
			"scope":      "management",
		},
	}, nil
}

func filterQuotaStatusesToAPIKey(statuses []any, apiKey string) []any {
	filtered := make([]any, 0, len(statuses))
	for _, item := range statuses {
		switch typed := item.(type) {
		case quota.Status:
			if strings.TrimSpace(typed.APIKey) == strings.TrimSpace(apiKey) {
				filtered = append(filtered, typed)
			}
		case gin.H:
			if strings.TrimSpace(stringFromAny(typed["api_key"])) == strings.TrimSpace(apiKey) {
				filtered = append(filtered, typed)
			}
		}
	}
	return filtered
}

func filterAPIKeyAliases(aliases map[string]string, apiKey string) map[string]string {
	if aliases == nil {
		return map[string]string{}
	}
	filtered := map[string]string{}
	if alias, ok := aliases[apiKey]; ok && strings.TrimSpace(alias) != "" {
		filtered[apiKey] = alias
	}
	return filtered
}

func filterUsageSnapshotToAPIKey(snapshot usage.StatisticsSnapshot, apiKey string) usage.StatisticsSnapshot {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return usage.StatisticsSnapshot{APIs: map[string]usage.APISnapshot{}, RequestsByDay: map[string]int64{}, RequestsByHour: map[string]int64{}, TokensByDay: map[string]int64{}, TokensByHour: map[string]int64{}}
	}
	filtered := usage.StatisticsSnapshot{
		APIs:           map[string]usage.APISnapshot{},
		RequestsByDay:  map[string]int64{},
		RequestsByHour: map[string]int64{},
		TokensByDay:    map[string]int64{},
		TokensByHour:   map[string]int64{},
	}
	apiSnapshot, ok := snapshot.APIs[apiKey]
	if !ok {
		return filtered
	}
	filtered.APIs[apiKey] = apiSnapshot
	filtered.TotalRequests = apiSnapshot.TotalRequests
	filtered.TotalTokens = apiSnapshot.TotalTokens
	for _, model := range apiSnapshot.Models {
		for _, detail := range model.Details {
			if detail.Failed {
				filtered.FailureCount++
			} else {
				filtered.SuccessCount++
			}
			ts := detail.Timestamp
			if ts.IsZero() {
				continue
			}
			dayKey := ts.UTC().Format("2006-01-02")
			hourKey := ts.UTC().Format("15:00")
			filtered.RequestsByDay[dayKey]++
			filtered.RequestsByHour[hourKey]++
			filtered.TokensByDay[dayKey] += detail.Tokens.TotalTokens
			filtered.TokensByHour[hourKey] += detail.Tokens.TotalTokens
		}
	}
	return filtered
}

func shouldFallbackUsageSnapshot(snapshot usage.StatisticsSnapshot) bool {
	return len(snapshot.APIs) == 0
}

func (h *Handler) sqliteUsageSnapshotOr(snapshot usage.StatisticsSnapshot) usage.StatisticsSnapshot {
	if h == nil || h.quotaManager == nil || h.cfg == nil {
		return snapshot
	}
	fallback, err := h.quotaManager.LoadRequestLogUsageSnapshot(h.cfg.APIKeys)
	if err != nil || len(fallback.APIs) == 0 {
		return snapshot
	}
	if h.usageStats != nil {
		h.usageStats.MergeSnapshot(fallback)
		return h.usageStats.Snapshot()
	}
	return fallback
}

func (h *Handler) clientAPIKeyAliases() map[string]string {
	if h == nil || h.cfg == nil || len(h.cfg.ClientAPIKeyPolicies) == 0 {
		return map[string]string{}
	}
	aliases := make(map[string]string, len(h.cfg.ClientAPIKeyPolicies))
	for _, item := range h.cfg.ClientAPIKeyPolicies {
		key := strings.TrimSpace(item.APIKey)
		alias := strings.TrimSpace(item.Alias)
		if key == "" || alias == "" {
			continue
		}
		aliases[key] = alias
	}
	return aliases
}

func (h *Handler) enrichQuotaStatusesWithSelectedAuth(statuses []any, authFiles []gin.H) []any {
	if h == nil || h.cfg == nil || len(statuses) == 0 {
		return statuses
	}
	type selectedAuthPolicySnapshot struct {
		id    string
		index string
	}
	selectedByKey := make(map[string]selectedAuthPolicySnapshot)
	for _, item := range h.cfg.ClientAPIKeyPolicies {
		key := strings.TrimSpace(item.APIKey)
		selectedAuthID := strings.TrimSpace(item.SelectedAuthID)
		selectedAuthIndex := strings.TrimSpace(item.SelectedAuthIndex)
		if key == "" || (selectedAuthID == "" && selectedAuthIndex == "") {
			continue
		}
		selectedByKey[key] = selectedAuthPolicySnapshot{id: selectedAuthID, index: selectedAuthIndex}
	}
	if len(selectedByKey) == 0 {
		return statuses
	}
	auths := h.liveAuths()
	authByID := make(map[string]gin.H, len(authFiles))
	authByIndex := make(map[string]gin.H, len(authFiles))
	for _, entry := range authFiles {
		id := strings.TrimSpace(stringFromAny(entry["id"]))
		if id != "" {
			authByID[id] = entry
		}
		index := strings.TrimSpace(stringFromAny(entry["auth_index"]))
		if index == "" {
			continue
		}
		authByIndex[index] = entry
	}
	enriched := make([]any, 0, len(statuses))
	for _, item := range statuses {
		row := quotaStatusRow(item)
		if row == nil {
			enriched = append(enriched, item)
			continue
		}
		apiKey := strings.TrimSpace(stringFromAny(row["api_key"]))
		selected := selectedByKey[apiKey]
		if selected.id == "" && selected.index == "" {
			enriched = append(enriched, row)
			continue
		}
		row["selected_auth_id"] = selected.id
		row["selected_auth_index"] = selected.index
		resolution := resolveSelectedAuthRef(auths, selectedAuthPolicyRef{ID: selected.id, Index: selected.index})
		row["selected_auth_resolved_via"] = resolution.resolvedBy
		row["selected_auth_mismatch"] = resolution.mismatch
		if resolution.auth != nil {
			authEntry, ok := authByID[strings.TrimSpace(resolution.auth.ID)]
			if !ok {
				authEntry, ok = authByIndex[strings.TrimSpace(resolution.auth.EnsureIndex())]
			}
			if !ok {
				row["selected_auth_label"] = ""
				row["selected_auth_provider"] = ""
				row["selected_auth_disabled"] = false
				row["selected_auth_missing"] = true
				enriched = append(enriched, row)
				continue
			}
			row["selected_auth_label"] = selectedAuthLabel(authEntry)
			row["selected_auth_provider"] = stringFromAny(authEntry["provider"])
			row["selected_auth_disabled"] = boolFromAny(authEntry["disabled"])
			row["selected_auth_missing"] = false
		} else {
			row["selected_auth_label"] = ""
			row["selected_auth_provider"] = ""
			row["selected_auth_disabled"] = false
			row["selected_auth_missing"] = true
		}
		enriched = append(enriched, row)
	}
	return enriched
}

func quotaStatusRow(item any) gin.H {
	switch typed := item.(type) {
	case gin.H:
		row := gin.H{}
		for key, value := range typed {
			row[key] = value
		}
		return row
	case quota.Status:
		row := gin.H{
			"api_key":                        typed.APIKey,
			"alias":                          typed.Alias,
			"output_token_quota":             typed.OutputTokenQuota,
			"output_token_quota_reset_hours": typed.OutputTokenQuotaResetHours,
			"used_output_tokens":             typed.UsedOutputTokens,
			"remaining_output_tokens":        typed.Remaining,
			"window_remaining_seconds":       typed.WindowRemainingSeconds,
			"unlimited":                      typed.Unlimited,
		}
		if typed.WindowStartedAt != nil {
			row["window_started_at"] = typed.WindowStartedAt
		}
		if typed.WindowEndsAt != nil {
			row["window_ends_at"] = typed.WindowEndsAt
		}
		return row
	default:
		return nil
	}
}

func selectedAuthLabel(entry gin.H) string {
	return firstNonEmptyQuotaString(
		stringFromAny(entry["label"]),
		stringFromAny(entry["email"]),
		stringFromAny(entry["account"]),
		stringFromAny(entry["name"]),
		stringFromAny(entry["id"]),
	)
}

func firstNonEmptyQuotaString(values ...string) string {
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func boolFromAny(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	default:
		return false
	}
}

func (h *Handler) enrichAuthFilesWithOpenAIQuota(authFiles []gin.H) {
	if h == nil || h.authManager == nil || h.cfg == nil || len(authFiles) == 0 {
		return
	}
	auths := h.authManager.List()
	if len(auths) == 0 {
		return
	}
	authByIndex := make(map[string]*coreauth.Auth, len(auths))
	for _, auth := range auths {
		if auth == nil {
			continue
		}
		index := strings.TrimSpace(auth.EnsureIndex())
		if index == "" {
			continue
		}
		authByIndex[index] = auth
	}
	for _, entry := range authFiles {
		index, _ := entry["auth_index"].(string)
		auth := authByIndex[strings.TrimSpace(index)]
		if auth == nil || !supportsOpenAIQuota(auth) {
			continue
		}
		quota := h.getOpenAIQuotaForAuth(auth)
		if quota != nil {
			entry["openai_quota"] = quota
		}
	}
}

func (h *Handler) getOpenAIQuotaForAuth(auth *coreauth.Auth) *codex.UsageQuotaSnapshot {
	if h == nil || auth == nil || h.cfg == nil {
		return nil
	}
	key := h.openAIQuotaCacheKey(auth)
	if key == "" {
		return nil
	}
	if authMetadataString(auth.Metadata, "access_token") == "" {
		snapshot := &codex.UsageQuotaSnapshot{Provider: "openai", Error: "Missing access token"}
		h.storeOpenAIQuotaCacheEntry(key, auth, snapshot, false)
		return cloneUsageQuotaSnapshot(snapshot)
	}
	now := time.Now()
	h.openAIQuotaMu.Lock()
	entry := h.openAIQuotaCache[key]
	if entry != nil {
		snapshot := cloneUsageQuotaSnapshot(entry.snapshot)
		fresh := now.Before(entry.expiresAt)
		shouldRefresh := !entry.refreshing && !fresh
		if shouldRefresh {
			entry.refreshing = true
		}
		h.openAIQuotaMu.Unlock()
		if shouldRefresh {
			go h.refreshOpenAIQuota(key, auth.Clone())
		}
		if snapshot != nil {
			return snapshot
		}
		return nil
	}
	h.openAIQuotaCache[key] = &cachedOpenAIQuota{refreshing: true}
	h.pruneOpenAIQuotaEntriesLocked(h.openAIQuotaCachePrefix(auth), key)
	h.openAIQuotaMu.Unlock()
	return h.fetchAndStoreOpenAIQuota(key, auth.Clone())
}

func (h *Handler) refreshOpenAIQuota(key string, auth *coreauth.Auth) {
	if h == nil || auth == nil || key == "" {
		return
	}
	_ = h.fetchAndStoreOpenAIQuota(key, auth)
}

func (h *Handler) fetchAndStoreOpenAIQuota(key string, auth *coreauth.Auth) *codex.UsageQuotaSnapshot {
	if h == nil || auth == nil || key == "" {
		return nil
	}
	if h.cfg == nil {
		h.clearOpenAIQuotaRefreshing(key)
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), openAIQuotaFetchTimeout)
	defer cancel()
	quota := fetchOpenAIQuotaForAuth(ctx, newCodexAuth(h.cfg), auth)
	currentKey := h.currentOpenAIQuotaCacheKey(auth)
	if currentKey != "" && currentKey != key {
		h.discardOpenAIQuotaCacheEntry(key)
		return nil
	}
	h.storeOpenAIQuotaCacheEntry(key, auth, quota, true)
	return cloneUsageQuotaSnapshot(quota)
}

func (h *Handler) openAIQuotaCacheKey(auth *coreauth.Auth) string {
	if auth == nil {
		return ""
	}
	index := strings.TrimSpace(auth.EnsureIndex())
	if index == "" {
		index = strings.TrimSpace(auth.ID)
	}
	accessToken := authMetadataString(auth.Metadata, "access_token")
	if index == "" {
		return ""
	}
	if accessToken == "" {
		return index + ":missing-token"
	}
	sum := sha256.Sum256([]byte(accessToken))
	return index + ":" + hex.EncodeToString(sum[:8])
}

func (h *Handler) openAIQuotaCachePrefix(auth *coreauth.Auth) string {
	if auth == nil {
		return ""
	}
	index := strings.TrimSpace(auth.EnsureIndex())
	if index == "" {
		index = strings.TrimSpace(auth.ID)
	}
	if index == "" {
		return ""
	}
	return index + ":"
}

func (h *Handler) clearOpenAIQuotaRefreshing(key string) {
	h.openAIQuotaMu.Lock()
	defer h.openAIQuotaMu.Unlock()
	if entry := h.openAIQuotaCache[key]; entry != nil {
		entry.refreshing = false
	}
}

func (h *Handler) discardOpenAIQuotaCacheEntry(key string) {
	h.openAIQuotaMu.Lock()
	defer h.openAIQuotaMu.Unlock()
	if entry := h.openAIQuotaCache[key]; entry != nil {
		if entry.refreshing {
			entry.refreshing = false
		}
		delete(h.openAIQuotaCache, key)
	}
}

func (h *Handler) currentOpenAIQuotaCacheKey(auth *coreauth.Auth) string {
	if h == nil || auth == nil || h.authManager == nil {
		return ""
	}
	authID := strings.TrimSpace(auth.ID)
	authIndex := strings.TrimSpace(auth.EnsureIndex())
	for _, candidate := range h.authManager.List() {
		if candidate == nil {
			continue
		}
		candidateID := strings.TrimSpace(candidate.ID)
		candidateIndex := strings.TrimSpace(candidate.EnsureIndex())
		if authID != "" && candidateID == authID {
			return h.openAIQuotaCacheKey(candidate)
		}
		if authIndex != "" && candidateIndex == authIndex {
			return h.openAIQuotaCacheKey(candidate)
		}
	}
	return ""
}

func (h *Handler) storeOpenAIQuotaCacheEntry(key string, auth *coreauth.Auth, snapshot *codex.UsageQuotaSnapshot, prune bool) {
	if h == nil || key == "" {
		return
	}
	now := time.Now()
	h.openAIQuotaMu.Lock()
	defer h.openAIQuotaMu.Unlock()
	entry := h.openAIQuotaCache[key]
	if entry == nil {
		entry = &cachedOpenAIQuota{}
		h.openAIQuotaCache[key] = entry
	}
	entry.refreshing = false
	entry.fetchedAt = now
	entry.snapshot = cloneUsageQuotaSnapshot(snapshot)
	if snapshot != nil && strings.TrimSpace(snapshot.Error) != "" {
		entry.expiresAt = now.Add(openAIQuotaErrorTTL)
	} else {
		entry.expiresAt = now.Add(openAIQuotaSuccessTTL)
	}
	if prune {
		h.pruneOpenAIQuotaEntriesLocked(h.openAIQuotaCachePrefix(auth), key)
	}
}

func (h *Handler) pruneOpenAIQuotaEntriesLocked(prefix, keepKey string) {
	if prefix == "" {
		return
	}
	for key, entry := range h.openAIQuotaCache {
		if key == keepKey || !strings.HasPrefix(key, prefix) {
			continue
		}
		if entry != nil && entry.refreshing {
			continue
		}
		delete(h.openAIQuotaCache, key)
	}
}

func fetchOpenAIQuotaForAuth(ctx context.Context, client *codex.CodexAuth, auth *coreauth.Auth) *codex.UsageQuotaSnapshot {
	if client == nil || auth == nil {
		return nil
	}
	accessToken := authMetadataString(auth.Metadata, "access_token")
	if accessToken == "" {
		return &codex.UsageQuotaSnapshot{Provider: "openai", Error: "Missing access token"}
	}
	expiresAt := ""
	if exp, ok := auth.ExpirationTime(); ok {
		expiresAt = exp.UTC().Format(time.RFC3339)
	}
	accountID := authMetadataString(auth.Metadata, "account_id")
	quota, err := client.FetchUsageQuota(ctx, accessToken, expiresAt, accountID)
	if err != nil {
		resolvedAccountID := strings.TrimSpace(accountID)
		if resolvedAccountID == "" {
			claims, parseErr := codex.ParseJWTToken(accessToken)
			if parseErr == nil && claims != nil {
				resolvedAccountID = strings.TrimSpace(claims.GetAccountID())
			}
		}
		return &codex.UsageQuotaSnapshot{Provider: "openai", AccountID: resolvedAccountID, Error: err.Error()}
	}
	return quota
}

func cloneUsageQuotaSnapshot(snapshot *codex.UsageQuotaSnapshot) *codex.UsageQuotaSnapshot {
	if snapshot == nil {
		return nil
	}
	cloned := *snapshot
	if snapshot.Daily != nil {
		daily := *snapshot.Daily
		cloned.Daily = &daily
	}
	if snapshot.Weekly != nil {
		weekly := *snapshot.Weekly
		cloned.Weekly = &weekly
	}
	return &cloned
}

func supportsOpenAIQuota(auth *coreauth.Auth) bool {
	if auth == nil {
		return false
	}
	provider := strings.ToLower(strings.TrimSpace(auth.Provider))
	return provider == "codex" || provider == "openai"
}

func authMetadataString(meta map[string]any, keys ...string) string {
	if meta == nil {
		return ""
	}
	for _, key := range keys {
		if value, ok := meta[key]; ok {
			if resolved := stringFromAny(value); resolved != "" {
				return resolved
			}
		}
	}
	for _, nestedKey := range []string{"token", "Token"} {
		nested, ok := meta[nestedKey]
		if !ok {
			continue
		}
		switch typed := nested.(type) {
		case map[string]any:
			if resolved := authMetadataString(typed, keys...); resolved != "" {
				return resolved
			}
		case map[string]string:
			for _, key := range keys {
				if resolved := strings.TrimSpace(typed[key]); resolved != "" {
					return resolved
				}
			}
		}
	}
	return ""
}

func stringFromAny(value any) string {
	if text, ok := value.(string); ok {
		return strings.TrimSpace(text)
	}
	return ""
}

func buildAuthUsage(snapshot usage.StatisticsSnapshot) []gin.H {
	now := time.Now()
	oneDayAgo := now.Add(-24 * time.Hour)
	sevenDaysAgo := now.Add(-7 * 24 * time.Hour)
	type authAccumulator struct {
		AuthIndex   string
		Source      string
		Requests24h int64
		Tokens24h   int64
		Requests7d  int64
		Tokens7d    int64
		TotalReqs   int64
		TotalTokens int64
		LastSeen    *time.Time
	}
	byAuth := make(map[string]*authAccumulator)
	for _, apiSnapshot := range snapshot.APIs {
		for _, modelSnapshot := range apiSnapshot.Models {
			for _, detail := range modelSnapshot.Details {
				if strings.TrimSpace(detail.AuthIndex) == "" {
					continue
				}
				acc := byAuth[detail.AuthIndex]
				if acc == nil {
					acc = &authAccumulator{AuthIndex: detail.AuthIndex, Source: detail.Source}
					byAuth[detail.AuthIndex] = acc
				}
				acc.TotalReqs++
				acc.TotalTokens += detail.Tokens.TotalTokens
				if !detail.Timestamp.IsZero() {
					ts := detail.Timestamp
					if acc.LastSeen == nil || ts.After(*acc.LastSeen) {
						copied := ts
						acc.LastSeen = &copied
					}
					if !ts.Before(oneDayAgo) {
						acc.Requests24h++
						acc.Tokens24h += detail.Tokens.TotalTokens
					}
					if !ts.Before(sevenDaysAgo) {
						acc.Requests7d++
						acc.Tokens7d += detail.Tokens.TotalTokens
					}
				}
			}
		}
	}
	rows := make([]gin.H, 0, len(byAuth))
	for _, acc := range byAuth {
		row := gin.H{
			"auth_index":     acc.AuthIndex,
			"source":         acc.Source,
			"requests_24h":   acc.Requests24h,
			"tokens_24h":     acc.Tokens24h,
			"requests_7d":    acc.Requests7d,
			"tokens_7d":      acc.Tokens7d,
			"total_requests": acc.TotalReqs,
			"total_tokens":   acc.TotalTokens,
		}
		if acc.LastSeen != nil {
			row["last_seen"] = acc.LastSeen.UTC()
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool {
		left := rows[i]
		right := rows[j]
		li, _ := left["auth_index"].(string)
		ri, _ := right["auth_index"].(string)
		return strings.ToLower(li) < strings.ToLower(ri)
	})
	return rows
}

func enrichAuthFilesWithUsage(authFiles []gin.H, authUsage []gin.H) {
	if len(authFiles) == 0 || len(authUsage) == 0 {
		return
	}
	usageByIndex := make(map[string]gin.H, len(authUsage))
	for _, row := range authUsage {
		index, _ := row["auth_index"].(string)
		if strings.TrimSpace(index) == "" {
			continue
		}
		usageByIndex[index] = row
	}
	for _, entry := range authFiles {
		index, _ := entry["auth_index"].(string)
		if strings.TrimSpace(index) == "" {
			continue
		}
		row, ok := usageByIndex[index]
		if !ok {
			continue
		}
		for _, key := range []string{"requests_24h", "tokens_24h", "requests_7d", "tokens_7d", "total_requests", "total_tokens", "last_seen"} {
			if value, exists := row[key]; exists {
				entry[key] = value
			}
		}
	}
}

func (h *Handler) collectAuthFileEntries() ([]gin.H, error) {
	if h == nil {
		return nil, nil
	}
	if h.authManager != nil {
		auths := h.authManager.List()
		files := make([]gin.H, 0, len(auths))
		for _, auth := range auths {
			if entry := h.buildAuthFileEntry(auth); entry != nil {
				files = append(files, entry)
			}
		}
		sort.Slice(files, func(i, j int) bool {
			nameI, _ := files[i]["name"].(string)
			nameJ, _ := files[j]["name"].(string)
			return strings.ToLower(nameI) < strings.ToLower(nameJ)
		})
		return files, nil
	}
	if h.cfg == nil {
		return nil, nil
	}
	entries, err := os.ReadDir(h.cfg.AuthDir)
	if err != nil {
		return nil, err
	}
	files := make([]gin.H, 0)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(strings.ToLower(name), ".json") {
			continue
		}
		full := filepath.Join(h.cfg.AuthDir, name)
		if info, errInfo := e.Info(); errInfo == nil {
			fileData := gin.H{"name": name, "size": info.Size(), "modtime": info.ModTime(), "source": "file"}
			if data, errRead := os.ReadFile(full); errRead == nil {
				typeValue := gjson.GetBytes(data, "type").String()
				emailValue := gjson.GetBytes(data, "email").String()
				disabledValue := gjson.GetBytes(data, "disabled").Bool()
				fileData["type"] = typeValue
				fileData["provider"] = typeValue
				fileData["email"] = emailValue
				fileData["disabled"] = disabledValue
				if disabledValue {
					fileData["status"] = "disabled"
				} else {
					fileData["status"] = "active"
				}
			}
			files = append(files, fileData)
		}
	}
	sort.Slice(files, func(i, j int) bool {
		nameI, _ := files[i]["name"].(string)
		nameJ, _ := files[j]["name"].(string)
		return strings.ToLower(nameI) < strings.ToLower(nameJ)
	})
	return files, nil
}
