package api

import (
	_ "embed"
	"net/http"

	"github.com/gin-gonic/gin"
)

//go:embed assets/quota.html
var quotaStatusHTML string

func (s *Server) serveQuotaStatusPage(c *gin.Context) {
	c.Header("Content-Type", "text/html; charset=utf-8")
	c.String(http.StatusOK, quotaStatusHTML)
}
