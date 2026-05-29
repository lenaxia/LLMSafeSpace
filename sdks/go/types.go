package llmsafespace

import "time"

type Workspace struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	UserID      string            `json:"userId"`
	Runtime     string            `json:"runtime"`
	StorageSize string            `json:"storageSize"`
	Phase       string            `json:"phase"`
	PVCName     string            `json:"pvcName,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
	CreatedAt   time.Time         `json:"createdAt"`
	UpdatedAt   time.Time         `json:"updatedAt"`
}

type CreateWorkspaceRequest struct {
	Name        string `json:"name,omitempty"`
	Runtime     string `json:"runtime,omitempty"`
	StorageSize string `json:"storageSize,omitempty"`
}

type WorkspaceListResult struct {
	Items      []WorkspaceListItem `json:"items"`
	Pagination *PaginationMetadata `json:"pagination,omitempty"`
}

type WorkspaceListItem struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	UserID      string    `json:"userId"`
	Runtime     string    `json:"runtime"`
	StorageSize string    `json:"storageSize"`
	Phase       string    `json:"phase,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

type PaginationMetadata struct {
	Total  int `json:"total"`
	Limit  int `json:"limit"`
	Offset int `json:"offset"`
}

type EnsureSessionResponse struct {
	WorkspaceID    string `json:"workspaceId"`
	WorkspacePhase string `json:"workspacePhase"`
	SessionID      string `json:"sessionId"`
	Resumed        bool   `json:"resumed"`
}

type MessageResponse struct {
	Raw     any    `json:"-"`
	Content string `json:"-"`
}

type TerminalTicket struct {
	Ticket    string `json:"ticket"`
	ExpiresAt string `json:"expiresAt"`
}

type SecretResponse struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Type      string    `json:"type"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}
