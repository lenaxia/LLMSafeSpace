package profile

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	
	"github.com/lenaxia/llmsafespace/api/internal/handlers/auth"
	"github.com/lenaxia/llmsafespace/api/internal/handlers/common"
	"github.com/lenaxia/llmsafespace/api/internal/interfaces"
	"github.com/lenaxia/llmsafespace/api/internal/logger"
)

type Handler struct {
	*common.BaseHandler
}

func NewHandler(services interfaces.Services, logger *logger.Logger) *Handler {
	return &Handler{
		BaseHandler: common.NewBaseHandler(services, logger),
	}
}

func (h *Handler) RegisterRoutes(router *gin.RouterGroup) {
	profileGroup := router.Group("/profiles")
	profileGroup.Use(auth.AuthMiddleware(h.Services.GetAuth()))
	
	profileGroup.GET("", h.ListProfiles)
	profileGroup.GET("/:id", h.GetProfile)
}

func (h *Handler) ListProfiles(c *gin.Context) {
	// Parse pagination parameters
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	
	// Validate pagination parameters
	if limit < 1 || limit > 100 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	
	// Get profiles from Kubernetes
	profileList, err := h.Services.GetKubernetesClient().LlmsafespaceV1().SandboxProfiles("").List(c.Request.Context(), metav1.ListOptions{
		Limit: int64(limit),
	})
	
	if err != nil {
		h.Logger.Error("Failed to list profiles", err)
		h.HandleError(c, http.StatusInternalServerError, "list_failed", err.Error())
		return
	}
	
	// Convert to API response format
	profiles := make([]map[string]interface{}, 0, len(profileList.Items))
	for _, profile := range profileList.Items {
		profiles = append(profiles, map[string]interface{}{
			"id":            profile.Name,
			"name":          profile.Name,
			"language":      profile.Spec.Language,
			"securityLevel": profile.Spec.SecurityLevel,
			"seccompProfile": profile.Spec.SeccompProfile,
		})
	}
	
	c.JSON(http.StatusOK, gin.H{
		"profiles": profiles,
		"pagination": gin.H{
			"limit":  limit,
			"offset": offset,
			"total":  len(profiles),
		},
	})
}

func (h *Handler) GetProfile(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		h.HandleError(c, http.StatusBadRequest, "missing_id", "Profile ID is required")
		return
	}
	
	// Get profile from Kubernetes
	profile, err := h.Services.GetKubernetesClient().LlmsafespaceV1().SandboxProfiles("").Get(c.Request.Context(), id, metav1.GetOptions{})
	if err != nil {
		h.Logger.Error("Failed to get profile", err, "id", id)
		h.HandleError(c, http.StatusInternalServerError, "retrieval_failed", err.Error())
		return
	}
	
	// Convert to API response format
	response := map[string]interface{}{
		"id":            profile.Name,
		"name":          profile.Name,
		"language":      profile.Spec.Language,
		"securityLevel": profile.Spec.SecurityLevel,
		"seccompProfile": profile.Spec.SeccompProfile,
		"preInstalledPackages": profile.Spec.PreInstalledPackages,
	}
	
	if profile.Spec.NetworkPolicies != nil {
		response["networkPolicies"] = profile.Spec.NetworkPolicies
	}
	
	if profile.Spec.ResourceDefaults != nil {
		response["resourceDefaults"] = map[string]interface{}{
			"cpu":              profile.Spec.ResourceDefaults.CPU,
			"memory":           profile.Spec.ResourceDefaults.Memory,
			"ephemeralStorage": profile.Spec.ResourceDefaults.EphemeralStorage,
		}
	}
	
	if profile.Spec.FilesystemConfig != nil {
		response["filesystemConfig"] = map[string]interface{}{
			"readOnlyPaths": profile.Spec.FilesystemConfig.ReadOnlyPaths,
			"writablePaths": profile.Spec.FilesystemConfig.WritablePaths,
		}
	}
	
	c.JSON(http.StatusOK, response)
}
