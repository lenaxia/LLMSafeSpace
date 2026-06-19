// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package interfaces

import (
	"context"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lenaxia/llmsafespace/api/internal/services/msgqueue"
	k8sinterfaces "github.com/lenaxia/llmsafespace/pkg/interfaces"
	"github.com/lenaxia/llmsafespace/pkg/types"
)

type SessionManager interface {
	CreateSession(userID, workspaceID string, conn types.WSConnection) (*types.Session, error)
	GetSession(sessionID string) (*types.Session, error)
	CloseSession(sessionID string)
	SetCancellationFunc(sessionID, executionID string, cancel context.CancelFunc)
	CancelExecution(sessionID, executionID string) bool
	Start() error
	Stop() error
}

type AuthService interface {
	GetUserID(c *gin.Context) string
	CheckResourceAccess(userID, resourceType, resourceID, action string) bool
	GenerateToken(userID string) (string, error)
	ValidateToken(token string) (string, error)
	// RevokeToken adds the JWT token to the revocation cache so subsequent
	// ValidateToken calls reject it. Used by /auth/logout to invalidate
	// the active session (G18, Epic 17). Implementations must be safe to
	// call with an empty token (return nil) and with non-JWT inputs (the
	// caller filters out API-key-shaped tokens before calling).
	RevokeToken(token string) error
	AuthenticateAPIKey(ctx context.Context, apiKey string) (string, error)
	Register(ctx context.Context, req types.RegisterRequest) (*types.AuthResponse, error)
	Login(ctx context.Context, req types.LoginRequest) (*types.AuthResponse, error)
	CreateAPIKey(ctx context.Context, userID string, req types.CreateAPIKeyRequest, sessionID string) (*types.APIKey, error)
	ListAPIKeys(ctx context.Context, userID string) ([]*types.APIKey, error)
	DeleteAPIKey(ctx context.Context, userID, keyID string) error
	AuthMiddleware() gin.HandlerFunc
	// OptionalAuthMiddleware sets userID in context when a valid token is
	// present but never aborts — handlers must check userID themselves.
	OptionalAuthMiddleware() gin.HandlerFunc
	Start() error
	Stop() error
}

type DatabaseService interface {
	GetUser(ctx context.Context, userID string) (*types.User, error)
	GetUserByEmail(ctx context.Context, email string) (*types.User, error)
	CreateUser(ctx context.Context, user *types.User) error
	UpdateUser(ctx context.Context, userID string, updates types.UserUpdates) error
	DeleteUser(ctx context.Context, userID string) error
	CountUsers(ctx context.Context) (int, error)
	// SetUserStatus sets the authoritative operational status of a user
	// (D19): 'suspended' blocks across all contexts, 'active' restores.
	SetUserStatus(ctx context.Context, userID string, status types.UserStatus) error
	GetUserByAPIKey(ctx context.Context, apiKey string) (*types.User, error)
	CreateAPIKey(ctx context.Context, apiKey *types.APIKey) error
	ListAPIKeys(ctx context.Context, userID string) ([]*types.APIKey, error)
	GetAPIKey(ctx context.Context, userID, keyID string) (*types.APIKey, error)
	DeleteAPIKey(ctx context.Context, userID, keyID string) error
	GetAPIKeyRecordByHash(ctx context.Context, keyHash string) (*types.APIKey, error)
	UpdateAPIKeyDEK(ctx context.Context, keyID string, wrappedDEK, kekSalt []byte, synced bool) error
	ListAPIKeysWithDecrypt(ctx context.Context, userID string) ([]*types.APIKey, error)
	GetWorkspace(ctx context.Context, workspaceID string) (*types.WorkspaceMetadata, error)
	CreateWorkspace(ctx context.Context, workspace *types.WorkspaceMetadata) error
	UpdateWorkspace(ctx context.Context, workspaceID string, updates types.WorkspaceUpdates) error
	DeleteWorkspace(ctx context.Context, workspaceID string) error
	ListWorkspaces(ctx context.Context, userID string, limit, offset int) ([]*types.WorkspaceMetadata, *types.PaginationMetadata, error)
	CountWorkspacesByUserAndOrg(ctx context.Context, userID, orgID string) (int, error)
	CountActiveWorkspacesByUserAndOrg(ctx context.Context, userID, orgID string) (int, error)
	SyncWorkspaceVersionInfo(ctx context.Context, workspaceID, imageTag, agentVersion string)
	MarkWorkspaceDeleted(ctx context.Context, workspaceID string)
	CheckPermission(userID, resourceType, resourceID, action string) (bool, error)
	CheckResourceOwnership(userID, resourceType, resourceID string) (bool, error)
	ListSessionIndex(ctx context.Context, workspaceID string) ([]types.SessionListItem, error)
	DeleteSessionIndex(ctx context.Context, workspaceID string) error
	DeleteSessionTree(ctx context.Context, workspaceID, sessionID string) error
	UpsertSessionMessage(ctx context.Context, workspaceID, sessionID string, at time.Time) error
	UpsertSessionTitle(ctx context.Context, workspaceID, sessionID, title string) error
	UpsertSessionParent(ctx context.Context, workspaceID, sessionID, parentID string) error
	UpsertSessionContextUsed(ctx context.Context, workspaceID, sessionID string, contextUsed int64) error
	UpdateSessionLastSeen(ctx context.Context, workspaceID, sessionID string) error
	ListAllWorkspaceOwners(ctx context.Context) (map[string]string, error)
	Ping(ctx context.Context) error
	Start() error
	Stop() error
}

type CacheService interface {
	Get(ctx context.Context, key string) (string, error)
	Set(ctx context.Context, key string, value string, expiration time.Duration) error
	SetNX(ctx context.Context, key string, value string, expiration time.Duration) (bool, error)
	Delete(ctx context.Context, key string) error
	GetObject(ctx context.Context, key string, value interface{}) error
	SetObject(ctx context.Context, key string, value interface{}, expiration time.Duration) error
	GetSession(ctx context.Context, sessionID string) (*types.CachedSession, error)
	SetSession(ctx context.Context, sessionID string, session types.CachedSession, expiration time.Duration) error
	DeleteSession(ctx context.Context, sessionID string) error
	Ping(ctx context.Context) error
	Start() error
	Stop() error
}

type RateLimiterService interface {
	Increment(ctx context.Context, key string, value int64, expiration time.Duration) (int64, error)
	AddToWindow(ctx context.Context, key string, timestamp int64, member string, expiration time.Duration) error
	RemoveFromWindow(ctx context.Context, key string, cutoff int64) error
	CountInWindow(ctx context.Context, key string, min, max int64) (int, error)
	GetWindowEntries(ctx context.Context, key string, start, stop int) ([]string, error)
	GetTTL(ctx context.Context, key string) (time.Duration, error)
	Allow(key string, rate float64, burst int) bool
	Start() error
	Stop() error
}

type MetricsService interface {
	RecordRequest(method, path string, status int, duration time.Duration, size int)
	RecordError(errorType, endpoint, code string)
	IncrementActiveConnections(connType string, userID string)
	DecrementActiveConnections(connType string, userID string)
	RecordResourceUsage(workspaceID string, cpu float64, memoryBytes int64)
	Start() error
	Stop() error
}

type WorkspaceService interface {
	CreateWorkspace(ctx context.Context, userID string, req types.CreateWorkspaceRequest) (*types.Workspace, error)
	GetWorkspace(ctx context.Context, userID, workspaceID string) (*types.Workspace, error)
	ResolveWorkspace(ctx context.Context, workspaceID string) (*types.WorkspaceMetadata, error)
	CheckOwnership(ctx context.Context, userID string, meta *types.WorkspaceMetadata) error
	ListWorkspaces(ctx context.Context, userID string, opts types.ListOptions) (*types.WorkspaceListResult, error)
	DeleteWorkspace(ctx context.Context, userID, workspaceID string) error
	SuspendWorkspace(ctx context.Context, userID, workspaceID string) error
	RestartWorkspace(ctx context.Context, userID, workspaceID string) error
	GetWorkspaceStatus(ctx context.Context, userID, workspaceID string) (*types.WorkspaceStatusResult, error)
	ActivateWorkspace(ctx context.Context, userID, workspaceID string) (*types.ActivateWorkspaceResponse, error)
	EnsureSession(ctx context.Context, userID, workspaceID string) (*types.EnsureSessionResponse, error)
	ListWorkspaceSessions(ctx context.Context, userID, workspaceID string) ([]types.SessionListItem, error)
	RenameSession(ctx context.Context, userID, workspaceID, sessionID, title string) error
	MarkSessionSeen(ctx context.Context, userID, workspaceID, sessionID string) error
	RenameWorkspace(ctx context.Context, userID, workspaceID, name string) error
	Start() error
	Stop() error
}

type SessionIndexService interface {
	RecordMessage(workspaceID, sessionID, title string, at time.Time)
	ListByWorkspace(ctx context.Context, workspaceID string) ([]types.SessionListItem, error)
	DeleteByWorkspace(ctx context.Context, workspaceID string) error
	DeleteSession(ctx context.Context, workspaceID, sessionID string) error
	UpsertTitle(ctx context.Context, workspaceID, sessionID, title string) error
	UpsertParent(ctx context.Context, workspaceID, sessionID, parentID string) error
	UpsertContextUsed(ctx context.Context, workspaceID, sessionID string, contextUsed int64) error
	UpdateLastSeen(ctx context.Context, workspaceID, sessionID string) error
	Start() error
	Stop() error
}

type MessageQueueService interface {
	Enqueue(ctx context.Context, workspaceID, sessionID, text string) (string, error)
	Dequeue(ctx context.Context, workspaceID, sessionID string) (*msgqueue.QueuedMessage, error)
	Requeue(ctx context.Context, workspaceID, sessionID string, msg msgqueue.QueuedMessage) error
	PeekAll(ctx context.Context, workspaceID, sessionID string) ([]msgqueue.QueuedMessage, error)
	Len(ctx context.Context, workspaceID, sessionID string) (int64, error)
	Remove(ctx context.Context, workspaceID, sessionID, messageID string) error
	Clear(ctx context.Context, workspaceID, sessionID string) error
	ClearWorkspace(ctx context.Context, workspaceID string) error
	PeekAllWorkspace(ctx context.Context, workspaceID string) ([]msgqueue.QueuedMessage, error)
}

type MeteringService interface {
	Record(event types.UsageEvent)
	RecordLifecycleEvent(ctx context.Context, workspaceID, ownerID string, ownerType types.OwnerType, fromPhase, toPhase, resourceTier string, eventTime time.Time) error
	GetUsage(ctx context.Context, owner types.BillingOwner, from, to time.Time) (*types.UsageReport, error)
	GetUsageByWorkspace(ctx context.Context, owner types.BillingOwner, workspaceID string, from, to time.Time) (*types.UsageReport, error)
	GetQuotaStatus(ctx context.Context, owner types.BillingOwner) ([]types.QuotaStatus, error)
	CheckQuota(ctx context.Context, owner types.BillingOwner, eventType string) (allowed bool, remaining int64, err error)
	ExportUsage(ctx context.Context) (int, error)
	Start() error
	Stop() error
}

// MeteringRecorder is the subset of MeteringService that record-only
// consumers depend on (middleware, proxy event handlers, inference-token
// trackers). ISP-extracted (US-46.7) so these consumers don't depend on
// query/export methods they never call.
type MeteringRecorder interface {
	Record(event types.UsageEvent)
	RecordLifecycleEvent(ctx context.Context, workspaceID, ownerID string, ownerType types.OwnerType, fromPhase, toPhase, resourceTier string, eventTime time.Time) error
}

// SettingsReader is the read-only subset of settings.InstanceService.
// 6 consumers (router, workspace_service, auth, rate_limit, max_active)
// only call Get* methods — they never Set. ISP-extracted (US-46.7).
type SettingsReader interface {
	GetBool(ctx context.Context, key string) (bool, error)
	GetInt(ctx context.Context, key string) (int, error)
	GetString(ctx context.Context, key string) (string, error)
	GetStrings(ctx context.Context, key string) ([]string, error)
}

// WorkspaceMetadataStore is the workspace-CRUD subset of DatabaseService.
// Handlers that only need to read/update workspace metadata depend on
// this narrow interface instead of the full DatabaseService. ISP-extracted
// (US-46.7).
type WorkspaceMetadataStore interface {
	GetWorkspace(ctx context.Context, workspaceID string) (*types.WorkspaceMetadata, error)
	UpdateWorkspace(ctx context.Context, workspaceID string, updates types.WorkspaceUpdates) error
}

type Services interface {
	GetAuth() AuthService
	GetDatabase() DatabaseService
	GetCache() CacheService
	GetMetrics() MetricsService
	GetWorkspace() WorkspaceService
	GetRateLimiter() RateLimiterService
	GetMetering() MeteringService
}

type KubernetesClient = k8sinterfaces.KubernetesClient
type LLMSafespaceV1Interface = k8sinterfaces.LLMSafespaceV1Interface
type RuntimeEnvironmentInterface = k8sinterfaces.RuntimeEnvironmentInterface
type WorkspaceInterface = k8sinterfaces.WorkspaceInterface
