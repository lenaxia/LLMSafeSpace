// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package types

import "context"

// contextKey is an unexported type for context keys defined in this package.
// Using a typed key avoids collisions with string keys from other packages.
type contextKey string

// ContextKeyUserID is the context key used to store the authenticated user ID.
// Both the auth middleware and service layer use this constant so the key is
// always in sync.
const ContextKeyUserID contextKey = "userID"

// ContextKeyUserRole is the context key used to store the authenticated user's role.
const ContextKeyUserRole contextKey = "userRole"

// ContextKeyWorkspaceMeta is the context key under which
// WorkspaceAccessMiddleware stores the resolved *WorkspaceMetadata for the
// current /:id workspace route. Service-layer methods (workspace.Service.
// verifyOwner, SecretService methods that previously re-checked ownership)
// read the metadata from here so they can short-circuit the redundant
// ResolveWorkspace + CheckOwnership round-trip the middleware has already
// performed. Lives in pkg/types — not api/internal/middleware — so the
// service layer can read it without importing the HTTP middleware package
// (which would invert the dependency direction).
const ContextKeyWorkspaceMeta contextKey = "workspaceMeta"

// WorkspaceMetaFromCtx returns the *WorkspaceMetadata stored in ctx by
// WorkspaceAccessMiddleware, or (nil, false) when the middleware did not run
// (e.g. the caller is a background job, a route outside idGroup, or a unit
// test). Callers that need to authorize MUST handle the (nil, false) case
// explicitly — a missing meta is NOT an implicit allow.
func WorkspaceMetaFromCtx(ctx context.Context) (*WorkspaceMetadata, bool) {
	if ctx == nil {
		return nil, false
	}
	v := ctx.Value(ContextKeyWorkspaceMeta)
	if v == nil {
		return nil, false
	}
	m, ok := v.(*WorkspaceMetadata)
	if !ok || m == nil {
		return nil, false
	}
	return m, true
}
