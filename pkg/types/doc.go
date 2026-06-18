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
//
// Types are organised by domain (one file per area):
//
//   - errors.go      — cross-cutting sentinel errors
//   - context.go     — context-key types and the WorkspaceMetaFromCtx accessor
//   - auth.go        — user, registration/login, API keys, auth config
//   - workspace.go   — workspace API DTOs (create/list/status/metadata)
//   - container.go   — pod/resource/security/network config types
//   - session.go     — sessions, WebSocket connections, agent health
//   - pagination.go  — pagination metadata and list options
//   - event.go       — Kubernetes events, resource status, file info
//   - orgs.go        — organizations, members, invitations
//   - orgs_policy.go — org policies and audit log entries
//   - billing.go     — usage events, reports, quota
package types
