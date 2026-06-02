// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package secrets

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// SecretType defines the type of secret.
type SecretType string

const (
	// SecretTypeAPIKey is for generic API-key secrets (legacy).
	// New code should use SecretTypeLLMProvider for structured provider
	// credentials. Kept for backward compatibility with existing secrets.
	SecretTypeAPIKey SecretType = "api-key"
	// SecretTypeLLMProvider is for structured LLM provider credentials
	// (Anthropic, OpenAI, etc.). Each secret holds one provider with
	// its API key, optional base URL, model visibility allowlist, and
	// default model selection. Multiple llm-provider secrets bound to
	// the same workspace are merged by the agent's FormatCredentials.
	SecretTypeLLMProvider   SecretType = "llm-provider"
	SecretTypeSSHKey        SecretType = "ssh-key"
	SecretTypeGitCredential SecretType = "git-credential"
	SecretTypeSecretFile    SecretType = "secret-file"
	SecretTypeEnvSecret     SecretType = "env-secret"
)

// ValidSecretTypes is the set of allowed secret types.
var ValidSecretTypes = map[SecretType]bool{
	SecretTypeAPIKey:        true,
	SecretTypeLLMProvider:   true,
	SecretTypeSSHKey:        true,
	SecretTypeGitCredential: true,
	SecretTypeSecretFile:    true,
	SecretTypeEnvSecret:     true,
}

// ValidSecretTypesList returns the canonical list of valid secret types,
// in stable order. Used to format the error message returned when a
// caller submits an invalid type, so the response is self-documenting.
func ValidSecretTypesList() []SecretType {
	return []SecretType{
		SecretTypeAPIKey,
		SecretTypeLLMProvider,
		SecretTypeSSHKey,
		SecretTypeGitCredential,
		SecretTypeSecretFile,
		SecretTypeEnvSecret,
	}
}

// MetadataRequirementsBySecretType is a self-documenting map of which
// metadata keys each secret type requires. Surfaced in error responses
// (Bug 7 in worklog 0085) so callers don't have to reverse-engineer the
// schema from 400s.
var MetadataRequirementsBySecretType = map[SecretType][]string{
	SecretTypeAPIKey:        {}, // optional: provider, model
	SecretTypeLLMProvider:   {}, // all config in value JSON, metadata optional
	SecretTypeSSHKey:        {"key_type"},
	SecretTypeGitCredential: {}, // optional: host
	SecretTypeSecretFile:    {"mount_path"},
	SecretTypeEnvSecret:     {"var_name"},
}

// formatSecretTypes joins SecretType values with commas for use in
// error messages (e.g. "api-key, ssh-key, git-credential, secret-file,
// env-secret"). Stable order.
func formatSecretTypes(types []SecretType) string {
	parts := make([]string, 0, len(types))
	for _, t := range types {
		parts = append(parts, string(t))
	}
	return strings.Join(parts, ", ")
}

// UserSecret represents an encrypted secret record.
type UserSecret struct {
	ID         string          `json:"id"`
	UserID     string          `json:"userId"`
	Name       string          `json:"name"`
	Type       SecretType      `json:"type"`
	Ciphertext []byte          `json:"-"` // never exposed via API
	KeyVersion int             `json:"keyVersion"`
	Metadata   json.RawMessage `json:"metadata"`
	CreatedAt  time.Time       `json:"createdAt"`
	UpdatedAt  time.Time       `json:"updatedAt"`
}

// SecretBinding represents a secret-to-workspace binding.
type SecretBinding struct {
	SecretID    string    `json:"secretId"`
	WorkspaceID string    `json:"workspaceId"`
	CreatedAt   time.Time `json:"createdAt"`
}

// AuditEntry represents a secret audit log entry.
type AuditEntry struct {
	ID          int64           `json:"id"`
	UserID      string          `json:"userId"`
	Action      string          `json:"action"`
	SecretID    *string         `json:"secretId,omitempty"`
	WorkspaceID *string         `json:"workspaceId,omitempty"`
	Metadata    json.RawMessage `json:"metadata,omitempty"`
	Timestamp   time.Time       `json:"timestamp"`
}

// CreateSecretRequest is the API request for creating a secret.
type CreateSecretRequest struct {
	Name     string          `json:"name" binding:"required,min=1,max=255"`
	Type     SecretType      `json:"type" binding:"required"`
	Value    string          `json:"value" binding:"required"` // plaintext, encrypted before storage
	Metadata json.RawMessage `json:"metadata"`
}

// UpdateSecretRequest is the API request for updating a secret value.
type UpdateSecretRequest struct {
	Value    string          `json:"value" binding:"required"`
	Metadata json.RawMessage `json:"metadata,omitempty"`
}

// SecretResponse is the API response for a secret (never includes value).
type SecretResponse struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Type      SecretType      `json:"type"`
	Metadata  json.RawMessage `json:"metadata"`
	CreatedAt time.Time       `json:"createdAt"`
	UpdatedAt time.Time       `json:"updatedAt"`
}

// SetBindingsRequest is the API request for setting workspace bindings.
type SetBindingsRequest struct {
	SecretIDs []string `json:"secretIds" binding:"required"`
}

// BindingsResponse is the API response for workspace bindings.
type BindingsResponse struct {
	Bindings []BoundSecret `json:"bindings"`
}

// BoundSecret is a secret reference in a binding response.
type BoundSecret struct {
	SecretID string     `json:"secretId"`
	Name     string     `json:"name"`
	Type     SecretType `json:"type"`
}

// AuditQuery defines filters for querying the audit log.
type AuditQuery struct {
	Action      string
	SecretID    string
	WorkspaceID string
	Since       *time.Time
	Until       *time.Time
	Limit       int
	Offset      int
}

// LLMModelConfig specifies a model identifier and optional display label
// for model visibility allowlisting.
type LLMModelConfig struct {
	ID    string `json:"id"`
	Label string `json:"label,omitempty"`
}

// LLMProviderData holds structured credentials for one LLM provider.
// The Plaintext value of an "llm-provider" secret is the JSON encoding of this struct.
//
// Provider is required (e.g. "anthropic", "openai", "google").
// APIKey is required.
// BaseURL is optional; when empty the provider's default endpoint is used.
// Models is an optional allowlist. When empty or nil all models from the
// provider are visible (no filtering). When non-empty only the listed
// models are shown.
// Default is the model ID to use when no per-session model is specified.
// SmallModel is the model ID used for lightweight/cheap operations
// (e.g. summarization).
type LLMProviderData struct {
	Provider   string           `json:"provider"`
	APIKey     string           `json:"apiKey"`
	BaseURL    string           `json:"baseURL,omitempty"`
	Models     []LLMModelConfig `json:"models,omitempty"`
	Default    string           `json:"default,omitempty"`
	SmallModel string           `json:"smallModel,omitempty"`
}

// Validate checks that required fields are set in LLMProviderData.
func (d LLMProviderData) Validate() error {
	if d.Provider == "" {
		return fmt.Errorf("%w: provider is required", ErrInvalidLLMProvider)
	}
	if d.APIKey == "" {
		return fmt.Errorf("%w: apiKey is required", ErrInvalidLLMProvider)
	}
	return nil
}
