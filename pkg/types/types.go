// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package types contains API DTOs (data transfer objects) used by the API
// service to receive requests and return responses to clients.
//
// These types are intentionally NOT Kubernetes CRD types. CRD types live in
// pkg/apis/llmsafespace/v1; this package converts to/from them at the
// service boundary. Types here use plain Go types (e.g. *time.Time, not
// *metav1.Time) so the JSON contract returned to clients is free of
// Kubernetes-isms (kind, apiVersion, metadata).
package types

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Common errors
var (
	ErrNotFound         = errors.New("resource not found")
	ErrPermissionDenied = errors.New("permission denied")
	ErrInvalidInput     = errors.New("invalid input")
	ErrAlreadyExists    = errors.New("resource already exists")
)

// contextKey is an unexported type for context keys defined in this package.
// Using a typed key avoids collisions with string keys from other packages.
type contextKey string

// ContextKeyUserID is the context key used to store the authenticated user ID.
// Both the auth middleware and service layer use this constant so the key is
// always in sync.
const ContextKeyUserID contextKey = "userID"

// ContextKeyUserRole is the context key used to store the authenticated user's role.
const ContextKeyUserRole contextKey = "userRole"

// Kubernetes object — there is no TypeMeta or ObjectMeta embedding. The
// service layer converts a v1.Workspace CRD into one of these for client
// responses.

// ResourceRequirements defines resource limits for a sandbox
type ResourceRequirements struct {
	// CPU resource limit
	CPU string `json:"cpu,omitempty"`

	// Memory resource limit
	Memory string `json:"memory,omitempty"`

	// GPU resource limit
	GPU string `json:"gpu,omitempty"`
}

// NetworkAccess defines network access configuration
type NetworkAccess struct {
	// Egress rules
	Egress []EgressRule `json:"egress,omitempty"`

	// Allow ingress traffic to sandbox
	Ingress bool `json:"ingress,omitempty"`
}

// EgressRule defines an egress rule
type EgressRule struct {
	// Domain name for egress filtering
	Domain string `json:"domain"`

	// Ports allowed for this domain
	Ports []PortRule `json:"ports,omitempty"`
}

// PortRule defines a port rule
type PortRule struct {
	// Port number
	Port int `json:"port"`

	// Protocol (TCP or UDP)
	Protocol string `json:"protocol,omitempty"`
}

// FilesystemConfig defines filesystem configuration
type FilesystemConfig struct {
	// Mount root filesystem as read-only
	ReadOnlyRoot bool `json:"readOnlyRoot,omitempty"`

	// Paths that should be writable
	WritablePaths []string `json:"writablePaths,omitempty"`
}

// StorageConfig defines storage configuration
type StorageConfig struct {
	// Enable persistent storage
	Persistent bool `json:"persistent,omitempty"`

	// Size of persistent volume
	VolumeSize string `json:"volumeSize,omitempty"`
}

// SecurityContext defines security context
type SecurityContext struct {
	// User ID to run container processes
	RunAsUser int64 `json:"runAsUser,omitempty"`

	// Group ID to run container processes
	RunAsGroup int64 `json:"runAsGroup,omitempty"`

	// Seccomp profile
	SeccompProfile string `json:"seccompProfile,omitempty"`

	// AppArmor profile
	AppArmorProfile string `json:"appArmorProfile,omitempty"`

	// Allow privilege escalation
	AllowPrivilegeEscalation bool `json:"allowPrivilegeEscalation,omitempty"`
}

// ProfileReference defines a reference to a RuntimeEnvironment
type ProfileReference struct {
	// Name of RuntimeEnvironment to use
	Name string `json:"name"`

	// Namespace of RuntimeEnvironment
	Namespace string `json:"namespace,omitempty"`
}

// ContainerStateValue represents the state of a container
type ContainerStateValue string

const (
	ContainerStateRunning    ContainerStateValue = "Running"
	ContainerStateTerminated ContainerStateValue = "Terminated"
	ContainerStateWaiting    ContainerStateValue = "Waiting"
	ContainerStateUnknown    ContainerStateValue = "Unknown"
)

// ContainerStatus represents the status of a container
type ContainerStatus struct {
	// Container name
	Name string `json:"name"`

	// Whether the container is ready
	Ready bool `json:"ready"`

	// Number of times the container has been restarted
	RestartCount int32 `json:"restartCount"`

	// Container state
	State ContainerStateValue `json:"state"`

	// Time when the container started
	StartedAt *time.Time `json:"startedAt,omitempty"`

	// Time when the container finished
	FinishedAt *time.Time `json:"finishedAt,omitempty"`

	// Exit code if terminated
	ExitCode int32 `json:"exitCode,omitempty"`

	// Reason for current state
	Reason string `json:"reason,omitempty"`

	// Message regarding current state
	Message string `json:"message,omitempty"`
}

// NetworkInfo represents network information for a sandbox
type NetworkInfo struct {
	// Pod IP address
	PodIP string `json:"podIP,omitempty"`

	// Host IP address
	HostIP string `json:"hostIP,omitempty"`

	// Whether ingress is allowed
	Ingress bool `json:"ingress"`

	// Allowed egress domains
	EgressDomains []string `json:"egressDomains,omitempty"`
}

// Event represents a Kubernetes event
type Event struct {
	// Event type (Normal, Warning)
	Type string `json:"type"`

	// Event reason
	Reason string `json:"reason"`

	// Event message
	Message string `json:"message"`

	// Event count
	Count int32 `json:"count"`

	// Event time
	Time *time.Time `json:"time,omitempty"`

	// Event source (Pod, Sandbox, etc.)
	Source string `json:"source,omitempty"`
}

// ResourceStatus defines resource usage
type ResourceStatus struct {
	// Current CPU usage
	CPUUsage string `json:"cpuUsage,omitempty"`

	// Current memory usage
	MemoryUsage string `json:"memoryUsage,omitempty"`
}

// PaginationMetadata represents pagination metadata
type PaginationMetadata struct {
	// Total number of items
	Total int `json:"total"`

	// Start index
	Start int `json:"start"`

	// End index
	End int `json:"end"`

	// Limit per page
	Limit int `json:"limit"`

	// Offset
	Offset int `json:"offset"`
}

// ExecutionResult represents the result of an execution
type ExecutionResult struct {
	// Stdout output
	Stdout string `json:"stdout"`

	// Stderr output
	Stderr string `json:"stderr"`

	// Exit code
	ExitCode int `json:"exitCode"`

	// Execution time in milliseconds
	ExecutionTime int64 `json:"executionTime"`

	// Error message if any
	Error string `json:"error,omitempty"`
}

// FileInfo represents information about a file
type FileInfo struct {
	// File name
	Name string `json:"name"`

	// File path
	Path string `json:"path"`

	// File size in bytes
	Size int64 `json:"size"`

	// File mode
	Mode string `json:"mode"`

	// Last modified time
	ModTime time.Time `json:"modTime"`

	// Whether it's a directory
	IsDir bool `json:"isDir"`
}

// WSConnection represents a WebSocket connection
type WSConnection interface {
	// ReadMessage reads a message from the connection
	ReadMessage() (messageType int, p []byte, err error)

	// WriteMessage writes a message to the connection
	WriteMessage(messageType int, data []byte) error

	// Close closes the connection
	Close() error
}

// Session represents a WebSocket session
type Session struct {
	// Session ID
	ID string

	// User ID
	UserID string

	// Workspace ID
	WorkspaceID string

	// WebSocket connection
	Conn WSConnection

	// Creation time
	CreatedAt time.Time
}

// User represents a user
type User struct {
	ID           string    `json:"id" db:"id"`
	Username     string    `json:"username" db:"username"`
	Email        string    `json:"email" db:"email"`
	PasswordHash string    `json:"-" db:"password_hash"`
	CreatedAt    time.Time `json:"createdAt" db:"created_at"`
	UpdatedAt    time.Time `json:"updatedAt" db:"updated_at"`
	Active       bool      `json:"active" db:"active"`
	Role         string    `json:"role" db:"role"`
}

// RegisterRequest is the request body for user registration.
type RegisterRequest struct {
	Username string `json:"username" binding:"required,min=3,max=64"`
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required,min=8,max=128"`
}

// LoginRequest is the request body for user login.
type LoginRequest struct {
	Email      string `json:"email"      binding:"required,email"`
	Password   string `json:"password"   binding:"required"`
	RememberMe bool   `json:"rememberMe"`
}

// AuthResponse is returned after successful registration or login.
//
// RecoveryKey is populated only on registration (one-time display). It is
// the user's sole opportunity to retrieve it; the API does not store it
// anywhere recoverable. Login responses omit this field entirely.
//
// TokenTTL is the effective JWT lifetime used for this session. It is tagged
// json:"-" so it never appears in the HTTP response body — clients already
// receive the exp claim inside the JWT. This field carries the TTL from the
// auth service to the router handler for cookie Max-Age calculation without
// requiring an interface change.
type AuthResponse struct {
	Token       string        `json:"token"`
	User        User          `json:"user"`
	RecoveryKey string        `json:"recoveryKey,omitempty"`
	TokenTTL    time.Duration `json:"-"` // router-internal: not serialized
}

// CreateAPIKeyRequest is the request body for creating an API key.
type CreateAPIKeyRequest struct {
	Name          string   `json:"name" binding:"required,min=1,max=128"`
	DecryptAccess bool     `json:"decryptAccess"`
	AllowedCIDRs  []string `json:"allowedCidrs,omitempty"`
}

// APIKey represents an API key record returned in list responses.
type APIKey struct {
	ID        string     `json:"id"`
	UserID    string     `json:"-" db:"user_id"`
	Name      string     `json:"name"`
	Key       string     `json:"key,omitempty"`
	Prefix    string     `json:"prefix"`
	Active    bool       `json:"active"`
	CreatedAt time.Time  `json:"createdAt"`
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
	Legacy    bool       `json:"legacy,omitempty" db:"key_legacy"`

	DecryptAccess bool     `json:"decryptAccess"`
	DekSynced     bool     `json:"dekSynced"`
	AllowedCIDRs  []string `json:"allowedCidrs,omitempty"`
	KekSalt       []byte   `json:"-" db:"kek_salt"`
	WrappedDEK    []byte   `json:"-" db:"wrapped_dek"`
	KeyCiphertext []byte   `json:"-" db:"key_ciphertext"`
}

// live Kubernetes status with database metadata and pagination so callers never
// receive untyped maps.

// UserUpdates carries the fields that may be changed on a User record.
// All fields are pointers — nil means "do not update this field".
type UserUpdates struct {
	Username     *string `json:"username,omitempty"`
	Email        *string `json:"email,omitempty"`
	Active       *bool   `json:"active,omitempty"`
	Role         *string `json:"role,omitempty"`
	PasswordHash *string `json:"-"`
}

// All scalar fields are pointers — nil means "do not update this field".
// Labels nil means "do not touch labels"; non-nil replaces the label set entirely.

// CachedSession is the typed representation of a WebSocket session stored in
// the cache. It replaces the previous map[string]interface{} bag.
type CachedSession struct {
	SessionID   string `json:"sessionId"`
	UserID      string `json:"userId"`
	WorkspaceID string `json:"workspaceId"`
}

type Message struct {
	Type    string `json:"type"`
	Content string `json:"content"`
}

// Workspace is the API transfer object for a workspace resource.
type Workspace struct {
	ID                      string            `json:"id"`
	Name                    string            `json:"name"`
	UserID                  string            `json:"userId"`
	Runtime                 string            `json:"runtime"`
	StorageSize             string            `json:"storageSize"`
	Phase                   string            `json:"phase"`
	PVCName                 string            `json:"pvcName,omitempty"`
	Labels                  map[string]string `json:"labels,omitempty"`
	DefaultModel            string            `json:"defaultModel,omitempty"`
	CreatedAt               time.Time         `json:"createdAt"`
	UpdatedAt               time.Time         `json:"updatedAt"`
	AgentNeedsRefresh       bool              `json:"agentNeedsRefresh"`
	CredentialsPendingSince *time.Time        `json:"credentialsPendingSince,omitempty"`
}

// CreateWorkspaceRequest is the request body for creating a workspace.
type CreateWorkspaceRequest struct {
	Name         string            `json:"name"`
	Runtime      string            `json:"runtime"`
	StorageSize  string            `json:"storageSize"`
	StorageClass string            `json:"storageClass,omitempty"`
	Labels       map[string]string `json:"labels,omitempty"`
	OrgID        *string           `json:"orgId,omitempty"`
}

// ListOptions carries pagination and filtering parameters.
type ListOptions struct {
	Limit  int `json:"limit"`
	Offset int `json:"offset"`
}

// WorkspaceListResult bundles workspace list items with pagination.
type WorkspaceListResult struct {
	Items      []WorkspaceListItem `json:"items"`
	Pagination *PaginationMetadata `json:"pagination,omitempty"`
}

// WorkspaceListItem is a lightweight workspace representation for list responses.
type WorkspaceListItem struct {
	ID                      string     `json:"id"`
	Name                    string     `json:"name"`
	UserID                  string     `json:"userId"`
	Runtime                 string     `json:"runtime"`
	StorageSize             string     `json:"storageSize"`
	Phase                   string     `json:"phase,omitempty"`
	ImageTag                string     `json:"imageTag,omitempty"`
	AgentVersion            string     `json:"agentVersion,omitempty"`
	DefaultModel            string     `json:"defaultModel,omitempty"`
	MaxActiveSessions       int        `json:"maxActiveSessions,omitempty"`
	CreatedAt               time.Time  `json:"createdAt"`
	UpdatedAt               time.Time  `json:"updatedAt"`
	AgentNeedsRefresh       bool       `json:"agentNeedsRefresh"`
	CredentialsPendingSince *time.Time `json:"credentialsPendingSince,omitempty"`
}

// WorkspaceStatusResult carries the status fields read from the Workspace CRD.
type WorkspaceStatusResult struct {
	Phase            string                     `json:"phase"`
	PVCName          string                     `json:"pvcName,omitempty"`
	ActiveSessions   int                        `json:"activeSessions"`
	LastActivityAt   *time.Time                 `json:"lastActivityAt,omitempty"`
	Message          string                     `json:"message,omitempty"`
	Conditions       []WorkspaceConditionResult `json:"conditions,omitempty"`
	CredentialState  CredentialStateResult      `json:"credentialState"`
	AgentHealth      AgentHealthResult          `json:"agentHealth"`
	Sessions         []SessionStatusItem        `json:"sessions,omitempty"`
	ImageTag         string                     `json:"imageTag,omitempty"`
	DiskUsedBytes    int64                      `json:"diskUsedBytes,omitempty"`
	DiskTotalBytes   int64                      `json:"diskTotalBytes,omitempty"`
	MemoryUsedBytes  int64                      `json:"memoryUsedBytes,omitempty"`
	MemoryTotalBytes int64                      `json:"memoryTotalBytes,omitempty"`
	ContextUsed      int64                      `json:"contextUsed"`
	ContextTotal     int64                      `json:"contextTotal"`
}

// SessionStatusItem describes a session reported by the workspace agent.
type SessionStatusItem struct {
	ID          string `json:"id"`
	Title       string `json:"title,omitempty"`
	Status      string `json:"status"`
	ContextUsed int64  `json:"contextUsed"`
}

type CredentialStateResult struct {
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
	Message   string `json:"message,omitempty"`
}

type AgentHealthResult struct {
	Status              string   `json:"status"`
	ProvidersConfigured int      `json:"providersConfigured"`
	AgentVersion        string   `json:"agentVersion,omitempty"`
	Connected           []string `json:"connected,omitempty"`
	Message             string   `json:"message,omitempty"`
	LastCheckedAt       string   `json:"lastCheckedAt,omitempty"`
}

// WorkspaceConditionResult carries a single workspace condition.
type WorkspaceConditionResult struct {
	Type    string `json:"type"`
	Status  string `json:"status"`
	Reason  string `json:"reason,omitempty"`
	Message string `json:"message,omitempty"`
}

// WorkspaceMetadata is the database record for a workspace.
//
// Phase and pvc_state used to live here as a denormalised cache of the
// Workspace CRD's status fields. The cache was removed in migration 9
// because it was eventually-consistent at best (best-effort writes from
// `syncPhase`) and routinely diverged from the CRD shortly after creation,
// which caused the sidebar to render new workspaces with no phase. The CRD
// is now the only source of truth; phase is fetched directly from the
// kube-apiserver in `ListWorkspaces` and `enforceMaxActiveWorkspaces`.
type WorkspaceMetadata struct {
	ID           string    `json:"id" db:"id"`
	UserID       string    `json:"userId" db:"user_id"`
	Name         string    `json:"name" db:"name"`
	Runtime      string    `json:"runtime" db:"runtime"`
	StorageSize  string    `json:"storageSize" db:"storage_size"`
	ImageTag     string    `json:"imageTag" db:"image_tag"`
	AgentVersion string    `json:"agentVersion" db:"agent_version"`
	CreatedAt    time.Time `json:"createdAt" db:"created_at"`
	UpdatedAt    time.Time `json:"updatedAt" db:"updated_at"`
	// Model selection (migration 000013)
	DefaultModel string `json:"defaultModel,omitempty" db:"default_model"`
	// Epic 27a: agent credential state (LEFT JOIN workspace_agent_state)
	AgentNeedsRefresh       bool       `json:"agentNeedsRefresh" db:"agent_needs_refresh"`
	CredentialsPendingSince *time.Time `json:"credentialsPendingSince,omitempty" db:"credentials_pending_since"`
	// Epic 11: org attribution (nullable — personal workspaces have no org)
	OrgID *string `json:"orgId,omitempty" db:"org_id"`
}

// WorkspaceUpdates carries the fields that may be changed on a WorkspaceMetadata record.
type WorkspaceUpdates struct {
	Name         *string `json:"name,omitempty"`
	DefaultModel *string `json:"defaultModel,omitempty"`
}

// WorkspaceConfig is non-sensitive workspace metadata persisted to K8s Secret
// for pod boot. Read by agentd's applyWorkspaceConfig at startup.
type WorkspaceConfig struct {
	DefaultModel string `json:"defaultModel,omitempty"`
}

// WorkspaceNotFoundError is returned when a workspace cannot be found.
type WorkspaceNotFoundError struct {
	ID string
}

func (e *WorkspaceNotFoundError) Error() string {
	return fmt.Sprintf("workspace %s not found", e.ID)
}

// --- Frontend types (Phase A) ---

// AuthConfig is returned by GET /auth/config for feature-flag discovery.
type AuthConfig struct {
	RegistrationEnabled bool     `json:"registrationEnabled"`
	OIDCEnabled         bool     `json:"oidcEnabled"`
	SSOProviders        []string `json:"ssoProviders,omitempty"`
	InstanceName        string   `json:"instanceName"`
	MOTD                string   `json:"motd,omitempty"`
}

// ActivateWorkspaceResponse is returned by POST /workspaces/:id/activate.
type ActivateWorkspaceResponse struct {
	Resumed   string `json:"resumed"`
	Suspended string `json:"suspended,omitempty"`
}

// EnsureSessionResponse is returned by POST /workspaces/:id/sessions/new.
// It guarantees the workspace is active with a running pod, returning the
// workspace ID and session ID for immediate use.
type EnsureSessionResponse struct {
	WorkspaceID    string `json:"workspaceId"`
	WorkspacePhase string `json:"workspacePhase"`
	SessionID      string `json:"sessionId"`
	Resumed        bool   `json:"resumed"`
}

// SessionListItem is sidebar metadata for a session (NOT message bodies).
//
// ParentID, when non-empty, is the session_id of the user-visible parent
// session — typically populated for opencode subagent (subtask) sessions
// spawned via the `task` tool. The sidebar nests children under their
// parent for navigation. NULL/empty means the session is top-level.
//
// ContextUsed, when non-nil, is the prompt token count from the last
// session.next.step.ended SSE event persisted by the API proxy. nil means
// no LLM step has completed yet for this session (distinguishable from 0).
type SessionListItem struct {
	ID            string     `json:"id"`
	Title         string     `json:"title,omitempty"`
	ParentID      string     `json:"parentId,omitempty"`
	LastMessageAt *time.Time `json:"lastMessageAt,omitempty"`
	MessageCount  int        `json:"messageCount"`
	Status        string     `json:"status"` // "active" | "idle"
	LastSeenAt    *time.Time `json:"lastSeenAt,omitempty"`
	HasUnread     bool       `json:"hasUnread"`
	ContextUsed   *int64     `json:"contextUsed,omitempty"`
}

// ActiveSessionsResponse is returned by GET /workspaces/:id/sessions/active.
type ActiveSessionsResponse struct {
	Active    []string `json:"active"`
	MaxActive int      `json:"maxActive"`
}

// OrgRole represents a user's role within an organization.
type OrgRole string

const (
	OrgRoleAdmin  OrgRole = "admin"
	OrgRoleMember OrgRole = "member"
)

// OrgStatus is the operational status of an organization. It gates access:
// only non-suspended orgs are usable via OrgMemberGuard/OrgAdminGuard. Both
// 'active' and 'pending_activation' allow access (the creator needs to reach
// the portal and Stripe checkout while pending); 'suspended' is fully locked.
type OrgStatus string

const (
	OrgStatusPendingActivation OrgStatus = "pending_activation"
	OrgStatusActive            OrgStatus = "active"
	OrgStatusSuspended         OrgStatus = "suspended"
)

// OrgPlan is the product plan identifier stored in organizations.plan_id and
// used locally for feature gating. The plan is set at org creation
// (enterprise for platform-admin orgs; the selected checkout plan on
// checkout.session.completed for self-service orgs). Per-event plan syncing
// from Stripe is planned for US-43.15.
type OrgPlan string

const (
	PlanFree       OrgPlan = "free"
	PlanTeam       OrgPlan = "team"
	PlanBusiness   OrgPlan = "business"
	PlanEnterprise OrgPlan = "enterprise"
)

// OrgSubscriptionStatus tracks the Stripe subscription lifecycle separately
// from OrgStatus. An org can be status='active' (members retain access) while
// subscription_status='past_due' (in the 7-day Smart Retries grace window).
type OrgSubscriptionStatus string

const (
	SubscriptionInactive OrgSubscriptionStatus = "inactive"
	SubscriptionActive   OrgSubscriptionStatus = "active"
	SubscriptionTrialing OrgSubscriptionStatus = "trialing"
	SubscriptionPastDue  OrgSubscriptionStatus = "past_due"
	SubscriptionCanceled OrgSubscriptionStatus = "canceled"
	SubscriptionUnpaid   OrgSubscriptionStatus = "unpaid"
)

// Organization is the API DTO for an organization.
type Organization struct {
	ID                 string                `json:"id"`
	Name               string                `json:"name"`
	Slug               string                `json:"slug"`
	CreatedBy          string                `json:"createdBy"`
	CreatedAt          time.Time             `json:"createdAt"`
	UpdatedAt          time.Time             `json:"updatedAt"`
	Status             OrgStatus             `json:"status"`
	PlanID             OrgPlan               `json:"planId"`
	SubscriptionStatus OrgSubscriptionStatus `json:"subscriptionStatus"`
}

// OrgMember is the API DTO for an organization membership.
type OrgMember struct {
	OrgID     string    `json:"orgId"`
	UserID    string    `json:"userId"`
	Username  string    `json:"username"`
	Email     string    `json:"email"`
	Role      OrgRole   `json:"role"`
	CreatedAt time.Time `json:"createdAt"`
}

// CreateOrgRequest is the request body for creating an organization. The slug is
// lowercased by the service before insert and uniqueness check.
//
// Per design 0031 D1, org creation is platform-admin only. The admin supplies
// the intended owner's email; the backend resolves it to a user ID. This is a
// single lookup, not a search/list endpoint (account-enumeration prevention).
type CreateOrgRequest struct {
	Name       string  `json:"name"       binding:"required,min=2,max=100"`
	Slug       string  `json:"slug"       binding:"required,min=2,max=50,alphanum"`
	OwnerEmail string  `json:"ownerEmail" binding:"required,email"`
	PlanID     OrgPlan `json:"planId"     binding:"omitempty"`
}

// UpdateOrgRequest is the request body for updating an organization.
type UpdateOrgRequest struct {
	Name string `json:"name" binding:"omitempty,min=2,max=100"`
	Slug string `json:"slug" binding:"omitempty,min=2,max=50,alphanum"`
}

// CreateOrgResponse is returned by POST /api/v1/orgs. Org creation is
// platform-admin only (design 0031 D1).
type CreateOrgResponse struct {
	OrgResponse
}

// OrgResponse extends Organization with the calling user's membership context.
// UserRole is omitempty so that an empty string (caller is not a member — e.g.
// platform admin creating an org for someone else) is omitted from JSON rather
// than appearing as `"userRole": ""`.
type OrgResponse struct {
	Organization
	UserRole    OrgRole `json:"userRole,omitempty"`
	MemberCount int     `json:"memberCount"`
}

// AddOrgMemberRequest is the request body for adding an org member.
type AddOrgMemberRequest struct {
	UserID string  `json:"userId" binding:"required"`
	Role   OrgRole `json:"role"   binding:"required"`
}

// ChangeOrgMemberRoleRequest is the request body for changing a member's role.
type ChangeOrgMemberRoleRequest struct {
	Role OrgRole `json:"role" binding:"required"`
}

type OwnerType string

const (
	OwnerTypeUser OwnerType = "user"
	OwnerTypeOrg  OwnerType = "org"
)

type BillingOwner struct {
	ID   string    `json:"id"`
	Type OwnerType `json:"type"`
}

type UsageEvent struct {
	IdempotencyKey string         `json:"idempotencyKey,omitempty"`
	Owner          BillingOwner   `json:"owner"`
	ActorID        string         `json:"actorId"`
	WorkspaceID    string         `json:"workspaceId,omitempty"`
	EventType      string         `json:"eventType"`
	EventSubtype   string         `json:"eventSubtype,omitempty"`
	Quantity       int64          `json:"quantity"`
	ResourceTier   string         `json:"resourceTier,omitempty"`
	Region         string         `json:"region,omitempty"`
	Metadata       map[string]any `json:"metadata,omitempty"`
	RequestContext map[string]any `json:"requestContext,omitempty"`
	Source         string         `json:"source"`
	EventTime      time.Time      `json:"eventTime"`
}

type UsageReport struct {
	OwnerID     string                      `json:"ownerId"`
	OwnerType   OwnerType                   `json:"ownerType"`
	PeriodFrom  time.Time                   `json:"periodFrom"`
	PeriodTo    time.Time                   `json:"periodTo"`
	Totals      map[string]int64            `json:"totals"`
	ByWorkspace map[string]map[string]int64 `json:"byWorkspace,omitempty"`
	ByDay       map[string]map[string]int64 `json:"byDay,omitempty"`
}

type QuotaStatus struct {
	EventType  string    `json:"eventType"`
	PeriodType string    `json:"periodType"`
	Limit      int64     `json:"limit"`
	Used       int64     `json:"used"`
	Remaining  int64     `json:"remaining"`
	ResetsAt   time.Time `json:"resetsAt"`
}

// --- US-43.2: Org invitations ---

// OrgInvitation is the API DTO for an org invitation row.
type OrgInvitation struct {
	ID         string     `json:"id"`
	OrgID      string     `json:"orgId"`
	Email      string     `json:"email"`
	Role       OrgRole    `json:"role"`
	InvitedBy  string     `json:"invitedBy"`
	TokenHash  string     `json:"-"`
	ExpiresAt  time.Time  `json:"expiresAt"`
	AcceptedAt *time.Time `json:"acceptedAt,omitempty"`
	DeclinedAt *time.Time `json:"declinedAt,omitempty"`
	BounceType string     `json:"bounceType,omitempty"`
	BouncedAt  *time.Time `json:"bouncedAt,omitempty"`
	CreatedAt  time.Time  `json:"createdAt"`
}

// CreateInvitationsRequest is the body for POST /orgs/:id/invitations.
type CreateInvitationsRequest struct {
	Emails []string `json:"emails" binding:"required,min=1,max=100,dive,email"`
	Role   OrgRole  `json:"role"   binding:"required"`
}

// InvitationDetail is the public response for GET /invitations/:token. It does
// not expose the token hash or internal IDs beyond what the recipient needs to
// decide whether to accept.
type InvitationDetail struct {
	OrgName     string    `json:"orgName"`
	OrgSlug     string    `json:"orgSlug"`
	InviterName string    `json:"inviterName"`
	Role        OrgRole   `json:"role"`
	ExpiresAt   time.Time `json:"expiresAt"`
}

// --- US-43.7: Org policies ---

// OrgPolicyKey identifies a single org-scoped policy. Per D15, Phase 2 ships
// exactly these four; the migration CHECK constraint enforces the same set.
type OrgPolicyKey string

const (
	PolicyAllowedModels             OrgPolicyKey = "allowed_models"
	PolicyAllowedProviders          OrgPolicyKey = "allowed_providers"
	PolicyMaxWorkspacesPerMember    OrgPolicyKey = "max_workspaces_per_member"
	PolicyMaxActiveWorkspacesPerMem OrgPolicyKey = "max_active_workspaces_per_member"
)

// OrgPolicy is one row of org_policies. The Value is the raw JSONB payload; the
// interpretation depends on the Key (see OrgPolicyValues).
type OrgPolicy struct {
	OrgID     string          `json:"-"`
	Key       OrgPolicyKey    `json:"key"`
	Value     json.RawMessage `json:"value"`
	UpdatedBy string          `json:"-"`
	UpdatedAt time.Time       `json:"updatedAt"`
}

// OrgPolicyValues is the typed view of all four Phase 2 policies for one org.
// Fields are pointers so nil means "not set / unrestricted"; the zero value of
// the dereferenced type is never confused with "unset".
type OrgPolicyValues struct {
	AllowedModels             *[]string `json:"allowedModels,omitempty"`
	AllowedProviders          *[]string `json:"allowedProviders,omitempty"`
	MaxWorkspacesPerMember    *int      `json:"maxWorkspacesPerMember,omitempty"`
	MaxActiveWorkspacesPerMem *int      `json:"maxActiveWorkspacesPerMember,omitempty"`
}

// IsModelAllowed reports whether modelID is permitted under the allowed-models
// policy. Returns true when no policy is set (unrestricted).
func (p *OrgPolicyValues) IsModelAllowed(modelID string) bool {
	if p == nil || p.AllowedModels == nil || len(*p.AllowedModels) == 0 {
		return true
	}
	for _, m := range *p.AllowedModels {
		if m == modelID {
			return true
		}
	}
	return false
}

// IsProviderAllowed reports whether providerID is permitted.
func (p *OrgPolicyValues) IsProviderAllowed(providerID string) bool {
	if p == nil || p.AllowedProviders == nil || len(*p.AllowedProviders) == 0 {
		return true
	}
	for _, id := range *p.AllowedProviders {
		if id == providerID {
			return true
		}
	}
	return false
}

// MaxWorkspaces returns the per-member workspace creation limit, or -1 (unlimited) when unset.
func (p *OrgPolicyValues) MaxWorkspaces() int {
	if p == nil || p.MaxWorkspacesPerMember == nil {
		return -1
	}
	return *p.MaxWorkspacesPerMember
}

// MaxActive returns the per-member concurrent active workspace limit, or -1.
func (p *OrgPolicyValues) MaxActive() int {
	if p == nil || p.MaxActiveWorkspacesPerMem == nil {
		return -1
	}
	return *p.MaxActiveWorkspacesPerMem
}

// --- US-43.13: Org-scoped audit log ---

// AuditEntry is one row of the audit_log, scoped to an org when OrgID is non-empty.
type AuditEntry struct {
	ID        int64          `json:"id"`
	ActorID   string         `json:"actorId"`
	Domain    string         `json:"domain"`
	Action    string         `json:"action"`
	TargetID  string         `json:"targetId,omitempty"`
	OrgID     string         `json:"orgId,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
	CreatedAt time.Time      `json:"createdAt"`
}
