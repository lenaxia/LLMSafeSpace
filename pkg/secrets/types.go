package secrets

import (
	"encoding/json"
	"time"
)

// SecretType defines the type of secret.
type SecretType string

const (
	SecretTypeLLMProvider   SecretType = "llm-provider"
	SecretTypeSSHKey        SecretType = "ssh-key"
	SecretTypeGitCredential SecretType = "git-credential"
	SecretTypeSecretFile    SecretType = "secret-file"
	SecretTypeEnvSecret     SecretType = "env-secret"
)

// ValidSecretTypes is the set of allowed secret types.
var ValidSecretTypes = map[SecretType]bool{
	SecretTypeLLMProvider:   true,
	SecretTypeSSHKey:        true,
	SecretTypeGitCredential: true,
	SecretTypeSecretFile:    true,
	SecretTypeEnvSecret:     true,
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
