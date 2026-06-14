// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package handlers

// credential_probe.go — GET /:id/models handler for both admin and user credential flows.
//
// When the user configures a provider credential (admin or personal), the UI
// needs to show the full model list from that provider so the user can:
//   1. Select which models to allow (the allowlist)
//   2. Set a context limit per model (needed for contextTotal display)
//
// This endpoint decrypts the credential, calls the provider's /v1/models
// (OpenAI-compatible), and returns the list merged with any already-saved
// context limits so the UI can pre-populate the fields.
//
// The provider's /v1/models endpoint returns only {id, object, created,
// owned_by} — no context window data — for all standard OpenAI-compatible
// providers. Context limits must be user-entered; this endpoint just supplies
// the model IDs and any previously-saved limits.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/lenaxia/llmsafespace/pkg/secrets"
)

// ProbeModelEntry is one model returned by the probe endpoint.
type ProbeModelEntry struct {
	ID           string `json:"id"`
	ContextLimit int    `json:"contextLimit"` // 0 = unknown / not configured
}

// ProbeModelsResponse is the response body for GET /:id/models.
type ProbeModelsResponse struct {
	Models  []ProbeModelEntry `json:"models"`
	BaseURL string            `json:"baseURL,omitempty"`
	// Warning is set when the /v1/models call failed. The response still
	// succeeds (200) but Models is empty so the UI shows a friendly message.
	Warning string `json:"warning,omitempty"`
}

// ProbeModelsRequest is the body for POST /api/v1/probe-models — a
// credential-free probe for use before a credential is saved.
type ProbeModelsRequest struct {
	APIKey  string `json:"apiKey" binding:"required"`
	BaseURL string `json:"baseURL" binding:"required"`
}

// ProbeModelsAnon handles POST /api/v1/probe-models.
// No credential ID needed — caller passes apiKey + baseURL directly.
// Auth is still required so arbitrary API keys can't be proxied by unauthenticated users.
func ProbeModelsAnon(c *gin.Context) {
	var req ProbeModelsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "apiKey and baseURL are required"})
		return
	}

	pd := secrets.LLMProviderData{APIKey: req.APIKey, BaseURL: req.BaseURL}
	plaintext, err := json.Marshal(pd)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	result := probeCredentialModels(c.Request.Context(), plaintext, nil)
	c.JSON(http.StatusOK, result)
}

// plaintext bytes), calls GET {baseURL}/v1/models with the stored API key,
// merges saved context limits, and returns the probe response.
//
// plaintext must be the decrypted JSON-encoded LLMProviderData.
// savedLimits is the ModelContextLimits map from the credential row.
func probeCredentialModels(ctx context.Context, plaintext []byte, savedLimits map[string]int) ProbeModelsResponse {
	var pd secrets.LLMProviderData
	if err := json.Unmarshal(plaintext, &pd); err != nil {
		return ProbeModelsResponse{Warning: "credential data is unreadable"}
	}

	if pd.BaseURL == "" {
		// No custom BaseURL — this is a first-party provider (OpenAI, Anthropic, etc.)
		// whose model list is managed by opencode's internal catalog, not discoverable
		// via /v1/models without a provider-specific endpoint.
		return ProbeModelsResponse{
			BaseURL: "",
			Warning: "no baseURL configured — models for built-in providers cannot be discovered. Enter model IDs manually.",
		}
	}

	baseURL := pd.BaseURL
	if len(baseURL) > 0 && baseURL[len(baseURL)-1] == '/' {
		baseURL = baseURL[:len(baseURL)-1]
	}
	url := baseURL + "/models"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return ProbeModelsResponse{BaseURL: pd.BaseURL, Warning: fmt.Sprintf("failed to build request: %v", err)}
	}
	req.Header.Set("Authorization", "Bearer "+pd.APIKey)
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return ProbeModelsResponse{BaseURL: pd.BaseURL, Warning: fmt.Sprintf("failed to reach provider: %v", err)}
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return ProbeModelsResponse{
			BaseURL: pd.BaseURL,
			Warning: fmt.Sprintf("provider returned HTTP %d: %s", resp.StatusCode, string(body)),
		}
	}

	var mlr struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&mlr); err != nil {
		return ProbeModelsResponse{BaseURL: pd.BaseURL, Warning: fmt.Sprintf("failed to parse model list: %v", err)}
	}

	models := make([]ProbeModelEntry, 0, len(mlr.Data))
	for _, m := range mlr.Data {
		if m.ID == "" {
			continue
		}
		models = append(models, ProbeModelEntry{
			ID:           m.ID,
			ContextLimit: savedLimits[m.ID],
		})
	}

	return ProbeModelsResponse{
		BaseURL: pd.BaseURL,
		Models:  models,
	}
}

// ProbeModels handles GET /api/v1/admin/provider-credentials/:id/models.
// Admin variant — uses the platform KEK to decrypt.
func (h *AdminProviderCredentialsHandler) ProbeModels(c *gin.Context) {
	id := c.Param("id")
	row, err := h.store.GetAdminCredential(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get credential"})
		return
	}
	if row == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "credential not found"})
		return
	}

	kek := h.kek()
	if kek == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "master secret not configured"})
		return
	}
	plaintext, err := secrets.DecryptSecret(kek, row.Ciphertext)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to decrypt credential"})
		return
	}

	result := probeCredentialModels(c.Request.Context(), plaintext, row.ModelContextLimits)
	c.JSON(http.StatusOK, result)
}

// ProbeModels handles GET /api/v1/provider-credentials/:id/models.
// User variant — uses the session DEK to decrypt.
func (h *UserProviderCredentialsHandler) ProbeModels(c *gin.Context) {
	userID, sessionID := extractAuth(c)
	if userID == "" || sessionID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}

	row, err := h.store.GetUserCredential(c.Request.Context(), userID, c.Param("id"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get credential"})
		return
	}
	if row == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "credential not found"})
		return
	}

	dek, err := h.keys.GetDEK(c.Request.Context(), sessionID)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "encryption unavailable"})
		return
	}
	plaintext, err := secrets.DecryptSecret(dek, row.Ciphertext)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to decrypt credential"})
		return
	}

	result := probeCredentialModels(c.Request.Context(), plaintext, row.ModelContextLimits)
	c.JSON(http.StatusOK, result)
}
