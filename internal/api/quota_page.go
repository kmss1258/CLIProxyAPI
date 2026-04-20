package api

import (
	_ "embed"
	"net/http"

	"github.com/gin-gonic/gin"
)

//go:embed assets/quota.html
var quotaStatusHTML string

//go:embed assets/favicon.ico
var quotaStatusFavicon []byte

func (s *Server) serveQuotaStatusPage(c *gin.Context) {
	c.Header("Content-Type", "text/html; charset=utf-8")
	c.String(http.StatusOK, quotaStatusHTML)
}

func (s *Server) serveQuotaStatusFavicon(c *gin.Context) {
	c.Data(http.StatusOK, "image/x-icon", quotaStatusFavicon)
}
