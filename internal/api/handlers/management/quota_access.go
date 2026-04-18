package management

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
)

type quotaViewer struct {
	CanManage bool
	APIKey    string
	Scope     string
}

func managementKeyFromRequest(c *gin.Context) string {
	if c == nil {
		return ""
	}
	var provided string
	if ah := c.GetHeader("Authorization"); ah != "" {
		parts := strings.SplitN(ah, " ", 2)
		if len(parts) == 2 && strings.EqualFold(strings.TrimSpace(parts[0]), "bearer") {
			provided = strings.TrimSpace(parts[1])
		} else {
			provided = strings.TrimSpace(ah)
		}
	}
	if provided == "" {
		provided = strings.TrimSpace(c.GetHeader("X-Management-Key"))
	}
	return provided
}

func (h *Handler) resolveQuotaViewer(c *gin.Context) (*quotaViewer, int, string) {
	provided := managementKeyFromRequest(c)
	if provided == "" {
		return nil, http.StatusUnauthorized, "missing access key"
	}
	clientIP := ""
	if c != nil {
		clientIP = c.ClientIP()
	}
	localClient := clientIP == "127.0.0.1" || clientIP == "::1"
	allowRemote := false
	secretHash := ""
	if h != nil && h.cfg != nil {
		allowRemote = h.cfg.RemoteManagement.AllowRemote
		secretHash = h.cfg.RemoteManagement.SecretKey
	}
	if h != nil && h.allowRemoteOverride {
		allowRemote = true
	}
	if localClient && h != nil && h.localPassword != "" && subtle.ConstantTimeCompare([]byte(provided), []byte(h.localPassword)) == 1 {
		return &quotaViewer{CanManage: true, Scope: "management"}, http.StatusOK, ""
	}
	if h != nil && h.envSecret != "" && subtle.ConstantTimeCompare([]byte(provided), []byte(h.envSecret)) == 1 {
		return &quotaViewer{CanManage: true, Scope: "management"}, http.StatusOK, ""
	}
	if secretHash != "" && bcrypt.CompareHashAndPassword([]byte(secretHash), []byte(provided)) == nil {
		if !localClient && !allowRemote {
			return nil, http.StatusForbidden, "remote management disabled"
		}
		return &quotaViewer{CanManage: true, Scope: "management"}, http.StatusOK, ""
	}
	if h != nil && h.cfg != nil {
		for _, apiKey := range h.cfg.APIKeys {
			if subtle.ConstantTimeCompare([]byte(strings.TrimSpace(apiKey)), []byte(provided)) == 1 {
				return &quotaViewer{CanManage: false, APIKey: provided, Scope: "self"}, http.StatusOK, ""
			}
		}
	}
	return nil, http.StatusUnauthorized, "invalid key"
}
