package credentials

import "time"

// CredentialSet represents a credential set entity.
type CredentialSet struct {
	ID               string    `json:"id"`
	Name             string    `json:"name"`
	IsDefault        bool      `json:"isDefault"`
	Providers        []string  `json:"providers"`        // provider names only (keys never exposed)
	ModelAllowlist   []string  `json:"modelAllowlist"`
	AssignedTo       any       `json:"assignedTo"`       // "all" or []string of user IDs
	KeyVersion       int       `json:"keyVersion"`
	CreatedAt        time.Time `json:"createdAt"`
	UpdatedAt        time.Time `json:"updatedAt"`
}

// CreateCredentialSetRequest is the request to create a credential set.
type CreateCredentialSetRequest struct {
	Name           string         `json:"name" binding:"required"`
	Providers      ProviderConfig `json:"providers" binding:"required"`
	ModelAllowlist []string       `json:"modelAllowlist"`
	AssignedTo     any            `json:"assignedTo"` // "all" or []string
	IsDefault      bool           `json:"isDefault"`
}

// UpdateCredentialSetRequest is the request to update a credential set.
type UpdateCredentialSetRequest struct {
	Name           *string         `json:"name,omitempty"`
	Providers      *ProviderConfig `json:"providers,omitempty"`
	ModelAllowlist *[]string       `json:"modelAllowlist,omitempty"`
	AssignedTo     any             `json:"assignedTo,omitempty"`
	IsDefault      *bool           `json:"isDefault,omitempty"`
}

// RotateKeyResult is the response from a key rotation operation.
type RotateKeyResult struct {
	Rotated        int `json:"rotated"`
	AlreadyCurrent int `json:"alreadyCurrent"`
	Errors         int `json:"errors"`
}
