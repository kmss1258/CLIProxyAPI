package management

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
)

type clientAPIKeyConcurrencyPolicyPayload struct {
	APIKey      string `json:"api-key"`
	Alias       string `json:"alias,omitempty"`
	Concurrency int    `json:"concurrency"`
}

func (h *Handler) GetClientAPIKeyConcurrencyPolicies(c *gin.Context) {
	items := make([]clientAPIKeyConcurrencyPolicyPayload, 0)
	for _, item := range h.cfg.ClientAPIKeyPolicies {
		if item.Concurrency <= 0 {
			continue
		}
		items = append(items, clientAPIKeyConcurrencyPolicyPayload{APIKey: item.APIKey, Alias: item.Alias, Concurrency: item.Concurrency})
	}
	c.JSON(http.StatusOK, gin.H{"client-api-key-concurrency-policies": items})
}

func (h *Handler) PutClientAPIKeyConcurrencyPolicies(c *gin.Context) {
	data, err := c.GetRawData()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed to read body"})
		return
	}
	var arr []clientAPIKeyConcurrencyPolicyPayload
	if err = json.Unmarshal(data, &arr); err != nil {
		var obj struct {
			Items    []clientAPIKeyConcurrencyPolicyPayload `json:"items"`
			Policies []clientAPIKeyConcurrencyPolicyPayload `json:"client-api-key-concurrency-policies"`
		}
		if err2 := json.Unmarshal(data, &obj); err2 != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
			return
		}
		if len(obj.Policies) > 0 {
			arr = obj.Policies
		} else {
			arr = obj.Items
		}
	}
	nextPolicies, err := h.mergeClientAPIKeyConcurrencyPolicies(arr, true)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	h.saveClientAPIKeyPolicies(c, nextPolicies)
}

func (h *Handler) PatchClientAPIKeyConcurrencyPolicies(c *gin.Context) {
	var body struct {
		Value *clientAPIKeyConcurrencyPolicyPayload `json:"value"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || body.Value == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}
	nextPolicies, err := h.mergeClientAPIKeyConcurrencyPolicies([]clientAPIKeyConcurrencyPolicyPayload{*body.Value}, false)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	h.saveClientAPIKeyPolicies(c, nextPolicies)
}

func (h *Handler) DeleteClientAPIKeyConcurrencyPolicies(c *gin.Context) {
	apiKey := strings.TrimSpace(c.Query("api-key"))
	if apiKey == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing api-key"})
		return
	}
	nextPolicies := append([]config.ClientAPIKeyPolicy(nil), h.cfg.ClientAPIKeyPolicies...)
	for i := range nextPolicies {
		if strings.TrimSpace(nextPolicies[i].APIKey) != apiKey {
			continue
		}
		nextPolicies[i].Concurrency = 0
	}
	nextPolicies = normalizePolicyList(nextPolicies)
	h.saveClientAPIKeyPolicies(c, nextPolicies)
}

func (h *Handler) mergeClientAPIKeyConcurrencyPolicies(items []clientAPIKeyConcurrencyPolicyPayload, replace bool) ([]config.ClientAPIKeyPolicy, error) {
	nextPolicies := append([]config.ClientAPIKeyPolicy(nil), h.cfg.ClientAPIKeyPolicies...)
	if replace {
		for i := range nextPolicies {
			nextPolicies[i].Concurrency = 0
		}
	}
	for _, item := range items {
		apiKey := strings.TrimSpace(item.APIKey)
		if apiKey == "" {
			return nil, config.ValidateClientAPIKeyPolicies(h.cfg.APIKeys, []config.ClientAPIKeyPolicy{{APIKey: apiKey, Concurrency: item.Concurrency}})
		}
		found := false
		for i := range nextPolicies {
			if strings.TrimSpace(nextPolicies[i].APIKey) != apiKey {
				continue
			}
			nextPolicies[i].Concurrency = item.Concurrency
			found = true
			break
		}
		if !found {
			nextPolicies = append(nextPolicies, config.ClientAPIKeyPolicy{APIKey: apiKey, Concurrency: item.Concurrency})
		}
	}
	nextPolicies = normalizePolicyList(nextPolicies)
	if err := config.ValidateClientAPIKeyPolicies(h.cfg.APIKeys, nextPolicies); err != nil {
		return nil, err
	}
	return nextPolicies, nil
}

func normalizePolicyList(items []config.ClientAPIKeyPolicy) []config.ClientAPIKeyPolicy {
	filtered := make([]config.ClientAPIKeyPolicy, 0, len(items))
	for _, item := range items {
		if clientAPIKeyPolicyHasMeaningfulConfig(item) {
			filtered = append(filtered, item)
		}
	}
	return config.NormalizeClientAPIKeyPolicies(filtered)
}
