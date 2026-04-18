package management

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
)

func (h *Handler) saveConcurrencyConfig(c *gin.Context, cfg config.ConcurrencyConfig) bool {
	tempCfg := *h.cfg
	tempCfg.Concurrency = config.NormalizeConcurrencyConfig(cfg)
	if err := config.ValidateConcurrencyConfig(tempCfg.Concurrency); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_concurrency_config", "message": err.Error()})
		return false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := config.SaveConfigPreserveComments(h.configFilePath, &tempCfg); err != nil {
		if h.concurrencyManager != nil {
			h.concurrencyManager.UpdateConfig(h.cfg)
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to save config: %v", err)})
		return false
	}
	if h.concurrencyManager != nil {
		h.concurrencyManager.UpdateConfig(&tempCfg)
	}
	h.cfg.Concurrency = tempCfg.Concurrency
	c.JSON(http.StatusOK, gin.H{"ok": true, "concurrency": h.cfg.Concurrency})
	return true
}

func (h *Handler) GetConcurrencyConfig(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"concurrency": h.cfg.Concurrency})
}

func (h *Handler) PutConcurrencyConfig(c *gin.Context) {
	data, err := c.GetRawData()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed to read body"})
		return
	}
	var cfg config.ConcurrencyConfig
	if err = json.Unmarshal(data, &cfg); err != nil {
		var wrapper struct {
			Concurrency config.ConcurrencyConfig `json:"concurrency"`
		}
		if err2 := json.Unmarshal(data, &wrapper); err2 != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
			return
		}
		cfg = wrapper.Concurrency
	}
	h.saveConcurrencyConfig(c, cfg)
}

func (h *Handler) PatchConcurrencyConfig(c *gin.Context) {
	type defaultsPatch struct {
		GlobalInFlight  *int `json:"global-inflight"`
		ClientAPIKey    *int `json:"client-api-key"`
		UpstreamAccount *int `json:"upstream-account"`
	}
	type patch struct {
		Enabled   *bool                              `json:"enabled"`
		Defaults  *defaultsPatch                     `json:"defaults"`
		Endpoints *[]config.EndpointConcurrencyLimit `json:"endpoints"`
	}
	var body patch
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}
	next := h.cfg.Concurrency
	if body.Enabled != nil {
		next.Enabled = *body.Enabled
	}
	if body.Defaults != nil {
		if body.Defaults.GlobalInFlight != nil {
			next.Defaults.GlobalInFlight = *body.Defaults.GlobalInFlight
		}
		if body.Defaults.ClientAPIKey != nil {
			next.Defaults.ClientAPIKey = *body.Defaults.ClientAPIKey
		}
		if body.Defaults.UpstreamAccount != nil {
			next.Defaults.UpstreamAccount = *body.Defaults.UpstreamAccount
		}
	}
	if body.Endpoints != nil {
		next.Endpoints = append([]config.EndpointConcurrencyLimit(nil), (*body.Endpoints)...)
	}
	h.saveConcurrencyConfig(c, next)
}
