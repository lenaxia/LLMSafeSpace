package handlers

import (
	"net/http"
	"regexp"

	"github.com/gin-gonic/gin"
)

var (
	questionIDPattern   = regexp.MustCompile(`^que_[a-zA-Z0-9]+$`)
	permissionIDPattern = regexp.MustCompile(`^per_[a-zA-Z0-9_]+$`)
)

// ListQuestions proxies GET /question to the workspace pod.
func (h *ProxyHandler) ListQuestions(c *gin.Context) {
	if h.dialect == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "dialect not configured"})
		return
	}
	h.proxyToWorkspace(c, h.dialect.QuestionListPath(), false, "")
}

// QuestionReply proxies POST /question/:requestID/reply to the workspace pod.
func (h *ProxyHandler) QuestionReply(c *gin.Context) {
	if h.dialect == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "dialect not configured"})
		return
	}
	requestID := c.Param("requestID")
	if !questionIDPattern.MatchString(requestID) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid question request ID format"})
		return
	}
	h.proxyToWorkspace(c, h.dialect.QuestionReplyPath(requestID), false, "")
}

// QuestionReject proxies POST /question/:requestID/reject to the workspace pod.
func (h *ProxyHandler) QuestionReject(c *gin.Context) {
	if h.dialect == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "dialect not configured"})
		return
	}
	requestID := c.Param("requestID")
	if !questionIDPattern.MatchString(requestID) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid question request ID format"})
		return
	}
	h.proxyToWorkspace(c, h.dialect.QuestionRejectPath(requestID), false, "")
}

// ListPermissions proxies GET /permission to the workspace pod.
func (h *ProxyHandler) ListPermissions(c *gin.Context) {
	if h.dialect == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "dialect not configured"})
		return
	}
	h.proxyToWorkspace(c, h.dialect.PermissionListPath(), false, "")
}

// PermissionReply proxies POST /permission/:requestID/reply to the workspace pod.
func (h *ProxyHandler) PermissionReply(c *gin.Context) {
	if h.dialect == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "dialect not configured"})
		return
	}
	requestID := c.Param("requestID")
	if !permissionIDPattern.MatchString(requestID) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid permission request ID format"})
		return
	}
	h.proxyToWorkspace(c, h.dialect.PermissionReplyPath(requestID), false, "")
}
