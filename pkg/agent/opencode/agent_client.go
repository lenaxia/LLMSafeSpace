// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package opencode

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"go.uber.org/zap"

	"github.com/lenaxia/llmsafespace/pkg/agentd"
	"github.com/lenaxia/llmsafespace/pkg/secrets"
)

// PasswordResolver resolves the opencode Basic-auth password for a workspace.
// The API-side implementation reads from the K8s Secret cache (pwCache);
// agentd-side reads from /sandbox-cfg/password.
type PasswordResolver func(ctx context.Context, workspaceID string) (string, error)

// PodIPResolver resolves the pod IP for a workspace.
type PodIPResolver interface {
	GetWorkspacePodIP(ctx context.Context, userID, workspaceID string) (string, error)
}

// AgentClient abstracts all direct opencode HTTP communication at the
// workspace level (US-29.1). Each method resolves podIP and password
// internally from the injected resolvers, keeping callers clean of auth
// concerns. The interface is caller-shaped — consumers ask for what
// they need, not for a raw HTTP client.
//
// The interface is satisfied by WorkspaceClient, which wraps the
// low-level Client with workspace→pod resolution.
type AgentClient interface {
	ListModels(ctx context.Context, workspaceID string) ([]byte, error)
	PatchConfig(ctx context.Context, workspaceID string, config map[string]any) error
	DisposeInstance(ctx context.Context, workspaceID string) error
	GetSessionStatuses(ctx context.Context, workspaceID string) (map[string]string, error)
	StageCredentials(ctx context.Context, workspaceID string, providers []secrets.LLMProviderData) error
}

// WorkspaceClient implements AgentClient by resolving each call to a
// specific pod IP + password, then delegating to the low-level Client.
// It is constructed once and shared across handlers; each call resolves
// the workspace's current podIP + password fresh, so pod migrations and
// password rotations are transparent to callers.
type WorkspaceClient struct {
	passwordResolver PasswordResolver
	podIPResolver    PodIPResolver
	httpClient       *http.Client
	logger           *zap.Logger
}

// NewWorkspaceClient creates an AgentClient that resolves workspace →
// podIP + password on each call.
func NewWorkspaceClient(pw PasswordResolver, ip PodIPResolver, logger *zap.Logger) *WorkspaceClient {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &WorkspaceClient{
		passwordResolver: pw,
		podIPResolver:    ip,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		logger: logger,
	}
}

// resolve returns a low-level Client configured for the workspace's pod.
// podIP is resolved first; password is resolved on demand by the Client.
func (w *WorkspaceClient) resolve(ctx context.Context, workspaceID string) (*Client, error) {
	// PodIPResolver requires a userID in some implementations; pass empty
	// for workspace-scoped resolvers that don't need it.
	podIP, err := w.podIPResolver.GetWorkspacePodIP(ctx, "", workspaceID)
	if err != nil || podIP == "" {
		return nil, fmt.Errorf("resolve pod IP for workspace %s: %w", workspaceID, err)
	}
	password, err := w.passwordResolver(ctx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("resolve password for workspace %s: %w", workspaceID, err)
	}
	baseURL := fmt.Sprintf("http://%s:%d", podIP, agentd.AgentPort)
	return NewClient(baseURL, password, w.logger), nil
}

func (w *WorkspaceClient) ListModels(ctx context.Context, workspaceID string) ([]byte, error) {
	c, err := w.resolve(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	return c.ListModels(ctx)
}

func (w *WorkspaceClient) PatchConfig(ctx context.Context, workspaceID string, config map[string]any) error {
	c, err := w.resolve(ctx, workspaceID)
	if err != nil {
		return err
	}
	return c.PatchConfig(ctx, config)
}

func (w *WorkspaceClient) DisposeInstance(ctx context.Context, workspaceID string) error {
	c, err := w.resolve(ctx, workspaceID)
	if err != nil {
		return err
	}
	return c.DisposeInstance(ctx)
}

func (w *WorkspaceClient) GetSessionStatuses(ctx context.Context, workspaceID string) (map[string]string, error) {
	c, err := w.resolve(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	return c.GetSessionStatuses(ctx)
}

func (w *WorkspaceClient) StageCredentials(ctx context.Context, workspaceID string, providers []secrets.LLMProviderData) error {
	c, err := w.resolve(ctx, workspaceID)
	if err != nil {
		return err
	}
	return c.StageCredentials(ctx, providers)
}

// Compile-time check that WorkspaceClient satisfies AgentClient.
var _ AgentClient = (*WorkspaceClient)(nil)
