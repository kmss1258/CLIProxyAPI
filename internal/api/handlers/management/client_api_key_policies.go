package management

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/quota"
)

func (h *Handler) refreshQuotaManager() {
	if h == nil || h.quotaManager == nil {
		return
	}
	_ = h.quotaManager.UpdateConfig(h.cfg, h.configFilePath)
}

func (h *Handler) refreshConcurrencyManager() {
	if h == nil || h.concurrencyManager == nil {
		return
	}
	h.concurrencyManager.UpdateConfig(h.cfg)
}

func (h *Handler) canonicalizeClientAPIKeyPolicies(policies []config.ClientAPIKeyPolicy) []config.ClientAPIKeyPolicy {
	if len(policies) == 0 {
		return policies
	}
	auths := h.liveAuths()
	if len(auths) == 0 {
		return policies
	}
	out := append([]config.ClientAPIKeyPolicy(nil), policies...)
	for i := range out {
		ref := canonicalizeSelectedAuthRef(auths, selectedAuthPolicyRef{
			ID:    out[i].SelectedAuthID,
			Index: out[i].SelectedAuthIndex,
		})
		out[i].SelectedAuthID = ref.ID
		out[i].SelectedAuthIndex = ref.Index
	}
	return out
}

func (h *Handler) saveClientAPIKeyPolicies(c *gin.Context, policies []config.ClientAPIKeyPolicy) bool {
	policies = h.canonicalizeClientAPIKeyPolicies(policies)
	tempCfg := *h.cfg
	tempCfg.ClientAPIKeyPolicies = append([]config.ClientAPIKeyPolicy(nil), policies...)
	if h.quotaManager != nil {
		candidateManager := quota.NewManager()
		if err := candidateManager.UpdateConfig(&tempCfg, h.configFilePath); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return false
		}
	}
	if h.concurrencyManager != nil {
		h.concurrencyManager.UpdateConfig(&tempCfg)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := config.SaveConfigPreserveComments(h.configFilePath, &tempCfg); err != nil {
		if h.quotaManager != nil {
			_ = h.quotaManager.UpdateConfig(h.cfg, h.configFilePath)
		}
		if h.concurrencyManager != nil {
			h.concurrencyManager.UpdateConfig(h.cfg)
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to save config: %v", err)})
		return false
	}
	if h.quotaManager != nil {
		if err := h.quotaManager.UpdateConfig(&tempCfg, h.configFilePath); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return false
		}
	}
	if h.concurrencyManager != nil {
		h.concurrencyManager.UpdateConfig(&tempCfg)
	}
	h.cfg.ClientAPIKeyPolicies = tempCfg.ClientAPIKeyPolicies
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
	return true
}

func (h *Handler) GetClientAPIKeyPolicies(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"client-api-key-policies": h.cfg.ClientAPIKeyPolicies})
}

func (h *Handler) PutClientAPIKeyPolicies(c *gin.Context) {
	data, err := c.GetRawData()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed to read body"})
		return
	}
	var arr []config.ClientAPIKeyPolicy
	if err = json.Unmarshal(data, &arr); err != nil {
		var obj struct {
			Items []config.ClientAPIKeyPolicy `json:"items"`
		}
		if err2 := json.Unmarshal(data, &obj); err2 != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
			return
		}
		arr = obj.Items
	}
	normalized := config.NormalizeClientAPIKeyPolicies(arr)
	if err = config.ValidateClientAPIKeyPolicies(h.cfg.APIKeys, normalized); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	h.saveClientAPIKeyPolicies(c, normalized)
}

func (h *Handler) PatchClientAPIKeyPolicy(c *gin.Context) {
	type valuePatch struct {
		APIKey                      *string `json:"api-key"`
		Alias                       *string `json:"alias"`
		SelectedAuthID              *string `json:"selected-auth-id"`
		SelectedAuthIndex           *string `json:"selected-auth-index"`
		OutputTokenQuota            *int64  `json:"output-token-quota"`
		OutputTokenQuotaResetHours  *int    `json:"output-token-quota-reset-hours"`
		LegacyOutputTokenQuotaReset *string `json:"output-token-quota-reset"`
		Concurrency                 *int    `json:"concurrency"`
	}
	var body struct {
		Index *int        `json:"index"`
		Match *string     `json:"match"`
		Value *valuePatch `json:"value"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || body.Value == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}
	targetIndex := -1
	if body.Index != nil && *body.Index >= 0 && *body.Index < len(h.cfg.ClientAPIKeyPolicies) {
		targetIndex = *body.Index
	}
	if targetIndex == -1 && body.Match != nil {
		match := strings.TrimSpace(*body.Match)
		for i := range h.cfg.ClientAPIKeyPolicies {
			if h.cfg.ClientAPIKeyPolicies[i].APIKey == match {
				targetIndex = i
				break
			}
		}
	}
	if targetIndex == -1 {
		if body.Value.APIKey == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "item not found"})
			return
		}
		entry := config.ClientAPIKeyPolicy{APIKey: strings.TrimSpace(*body.Value.APIKey)}
		if entry.APIKey == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "api-key is required"})
			return
		}
		if body.Value.Alias != nil {
			entry.Alias = strings.TrimSpace(*body.Value.Alias)
		}
		if body.Value.SelectedAuthID != nil {
			entry.SelectedAuthID = strings.TrimSpace(*body.Value.SelectedAuthID)
		}
		if body.Value.SelectedAuthIndex != nil {
			entry.SelectedAuthIndex = strings.TrimSpace(*body.Value.SelectedAuthIndex)
		}
		if body.Value.SelectedAuthID != nil && entry.SelectedAuthID == "" && body.Value.SelectedAuthIndex == nil {
			entry.SelectedAuthIndex = ""
		}
		if body.Value.SelectedAuthIndex != nil && entry.SelectedAuthIndex == "" && body.Value.SelectedAuthID == nil {
			entry.SelectedAuthID = ""
		}
		if body.Value.OutputTokenQuota != nil && *body.Value.OutputTokenQuota > 0 {
			entry.OutputTokenQuota = *body.Value.OutputTokenQuota
		}
		if body.Value.OutputTokenQuotaResetHours != nil {
			entry.OutputTokenQuotaResetHours = *body.Value.OutputTokenQuotaResetHours
		}
		if body.Value.LegacyOutputTokenQuotaReset != nil {
			if body.Value.OutputTokenQuotaResetHours == nil {
				entry.OutputTokenQuotaResetHours = 0
			}
			entry.LegacyOutputTokenQuotaReset = strings.TrimSpace(*body.Value.LegacyOutputTokenQuotaReset)
		}
		if body.Value.Concurrency != nil {
			entry.Concurrency = *body.Value.Concurrency
		}
		if !clientAPIKeyPolicyHasMeaningfulConfig(entry) {
			c.JSON(http.StatusOK, gin.H{"message": "no policy changes to apply"})
			return
		}
		normalizedEntry := config.NormalizeClientAPIKeyPolicies([]config.ClientAPIKeyPolicy{entry})
		if err := config.ValidateClientAPIKeyPolicies(h.cfg.APIKeys, normalizedEntry); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		h.saveClientAPIKeyPolicies(c, config.NormalizeClientAPIKeyPolicies(append(h.cfg.ClientAPIKeyPolicies, normalizedEntry[0])))
		return
	}
	entry := h.cfg.ClientAPIKeyPolicies[targetIndex]
	if body.Value.APIKey != nil {
		entry.APIKey = strings.TrimSpace(*body.Value.APIKey)
		if entry.APIKey == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "api-key is required"})
			return
		}
	}
	if body.Value.Alias != nil {
		entry.Alias = strings.TrimSpace(*body.Value.Alias)
	}
	if body.Value.SelectedAuthID != nil {
		entry.SelectedAuthID = strings.TrimSpace(*body.Value.SelectedAuthID)
	}
	if body.Value.SelectedAuthIndex != nil {
		entry.SelectedAuthIndex = strings.TrimSpace(*body.Value.SelectedAuthIndex)
	}
	if body.Value.SelectedAuthID != nil && entry.SelectedAuthID == "" && body.Value.SelectedAuthIndex == nil {
		entry.SelectedAuthIndex = ""
	}
	if body.Value.SelectedAuthIndex != nil && entry.SelectedAuthIndex == "" && body.Value.SelectedAuthID == nil {
		entry.SelectedAuthID = ""
	}
	if body.Value.OutputTokenQuota != nil {
		entry.OutputTokenQuota = *body.Value.OutputTokenQuota
		if entry.OutputTokenQuota < 0 {
			entry.OutputTokenQuota = 0
		}
	}
	if body.Value.OutputTokenQuotaResetHours != nil {
		entry.OutputTokenQuotaResetHours = *body.Value.OutputTokenQuotaResetHours
	}
	if body.Value.LegacyOutputTokenQuotaReset != nil {
		if body.Value.OutputTokenQuotaResetHours == nil {
			entry.OutputTokenQuotaResetHours = 0
		}
		entry.LegacyOutputTokenQuotaReset = strings.TrimSpace(*body.Value.LegacyOutputTokenQuotaReset)
	}
	if body.Value.Concurrency != nil {
		entry.Concurrency = *body.Value.Concurrency
	}
	if !clientAPIKeyPolicyHasMeaningfulConfig(entry) {
		nextPolicies := append([]config.ClientAPIKeyPolicy(nil), h.cfg.ClientAPIKeyPolicies[:targetIndex]...)
		nextPolicies = append(nextPolicies, h.cfg.ClientAPIKeyPolicies[targetIndex+1:]...)
		h.saveClientAPIKeyPolicies(c, nextPolicies)
		return
	}
	normalizedEntry := config.NormalizeClientAPIKeyPolicies([]config.ClientAPIKeyPolicy{entry})
	if err := config.ValidateClientAPIKeyPolicies(h.cfg.APIKeys, normalizedEntry); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	nextPolicies := append([]config.ClientAPIKeyPolicy(nil), h.cfg.ClientAPIKeyPolicies...)
	nextPolicies[targetIndex] = normalizedEntry[0]
	h.saveClientAPIKeyPolicies(c, config.NormalizeClientAPIKeyPolicies(nextPolicies))
}

func clientAPIKeyPolicyHasMeaningfulConfig(item config.ClientAPIKeyPolicy) bool {
	if strings.TrimSpace(item.Alias) != "" {
		return true
	}
	if strings.TrimSpace(item.SelectedAuthID) != "" {
		return true
	}
	if strings.TrimSpace(item.SelectedAuthIndex) != "" {
		return true
	}
	if item.OutputTokenQuota > 0 {
		return true
	}
	if item.OutputTokenQuotaResetHours > 0 {
		return true
	}
	if item.Concurrency > 0 {
		return true
	}
	return strings.TrimSpace(item.LegacyOutputTokenQuotaReset) != ""
}

func (h *Handler) DeleteClientAPIKeyPolicy(c *gin.Context) {
	if idxStr := c.Query("index"); idxStr != "" {
		var idx int
		if _, err := fmt.Sscanf(idxStr, "%d", &idx); err == nil && idx >= 0 && idx < len(h.cfg.ClientAPIKeyPolicies) {
			nextPolicies := append([]config.ClientAPIKeyPolicy(nil), h.cfg.ClientAPIKeyPolicies[:idx]...)
			nextPolicies = append(nextPolicies, h.cfg.ClientAPIKeyPolicies[idx+1:]...)
			h.saveClientAPIKeyPolicies(c, nextPolicies)
			return
		}
	}
	apiKey := strings.TrimSpace(c.Query("api-key"))
	if apiKey == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing index or api-key"})
		return
	}
	out := make([]config.ClientAPIKeyPolicy, 0, len(h.cfg.ClientAPIKeyPolicies))
	for _, item := range h.cfg.ClientAPIKeyPolicies {
		if item.APIKey != apiKey {
			out = append(out, item)
		}
	}
	h.saveClientAPIKeyPolicies(c, out)
}

func (h *Handler) ResetClientAPIKeyPolicyUsage(c *gin.Context) {
	var body struct {
		APIKey *string `json:"api-key"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || body.APIKey == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}
	apiKey := strings.TrimSpace(*body.APIKey)
	if apiKey == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "api-key is required"})
		return
	}
	if h.quotaManager == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "quota manager unavailable"})
		return
	}
	if err := h.quotaManager.ResetUsage(apiKey); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok", "api-key": apiKey})
}
