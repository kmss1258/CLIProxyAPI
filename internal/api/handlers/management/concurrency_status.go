package management

import (
	"net/http"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/concurrency"
)

func (h *Handler) GetConcurrencyStatus(c *gin.Context) {
	if h == nil || h.cfg == nil {
		c.JSON(http.StatusOK, gin.H{"enabled": false})
		return
	}
	snapshot := concurrency.StatusSnapshot{Endpoints: map[string]int{}, ClientAPIKeys: map[string]int{}, UpstreamAccounts: map[string]int{}}
	if h.concurrencyManager != nil {
		snapshot = h.concurrencyManager.Snapshot()
	}
	endpoints := make([]gin.H, 0, len(h.cfg.Concurrency.Endpoints))
	for _, item := range h.cfg.Concurrency.Endpoints {
		path := strings.TrimSpace(item.Path)
		endpoints = append(endpoints, gin.H{"path": path, "limit": item.InFlight, "inflight": snapshot.Endpoints[path]})
	}
	clientPolicies := make([]gin.H, 0)
	seenClientKeys := make(map[string]struct{})
	for _, item := range h.cfg.ClientAPIKeyPolicies {
		if item.Concurrency <= 0 && snapshot.ClientAPIKeys[item.APIKey] <= 0 {
			continue
		}
		limit := item.Concurrency
		if limit <= 0 {
			limit = h.cfg.Concurrency.Defaults.ClientAPIKey
		}
		entry := gin.H{"api-key": item.APIKey, "limit": limit, "inflight": snapshot.ClientAPIKeys[item.APIKey]}
		if alias := strings.TrimSpace(item.Alias); alias != "" {
			entry["alias"] = alias
		}
		clientPolicies = append(clientPolicies, entry)
		seenClientKeys[item.APIKey] = struct{}{}
	}
	for apiKey, inflight := range snapshot.ClientAPIKeys {
		if _, exists := seenClientKeys[apiKey]; exists {
			continue
		}
		clientPolicies = append(clientPolicies, gin.H{"api-key": apiKey, "limit": h.cfg.Concurrency.Defaults.ClientAPIKey, "inflight": inflight})
	}
	sort.Slice(clientPolicies, func(i, j int) bool {
		return stringFromAny(clientPolicies[i]["api-key"]) < stringFromAny(clientPolicies[j]["api-key"])
	})
	upstreamStatuses := make([]gin.H, 0)
	if h.authManager != nil {
		for _, auth := range h.authManager.List() {
			if auth == nil {
				continue
			}
			limit := concurrency.ResolveUpstreamLimit(h.cfg, auth.Provider, auth.ID)
			inflight := snapshot.UpstreamAccounts[auth.ID]
			if limit <= 0 && inflight <= 0 {
				continue
			}
			label := strings.TrimSpace(auth.Label)
			if label == "" {
				label = strings.TrimSpace(auth.EnsureIndex())
			}
			upstreamStatuses = append(upstreamStatuses, gin.H{
				"provider": auth.Provider,
				"auth-id":  auth.ID,
				"label":    label,
				"limit":    limit,
				"inflight": inflight,
			})
		}
	}
	sort.Slice(upstreamStatuses, func(i, j int) bool {
		return stringFromAny(upstreamStatuses[i]["auth-id"]) < stringFromAny(upstreamStatuses[j]["auth-id"])
	})
	c.JSON(http.StatusOK, gin.H{
		"enabled":  h.cfg.Concurrency.Enabled,
		"defaults": h.cfg.Concurrency.Defaults,
		"current": gin.H{
			"global-inflight": snapshot.GlobalInFlight,
		},
		"endpoints":         endpoints,
		"client-api-keys":   clientPolicies,
		"upstream-accounts": upstreamStatuses,
	})
}
