// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package opencode

import (
	"context"
	"fmt"

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
// userID is required for workspace ownership verification (the PodIPResolver
// enforces that the caller owns the workspace before returning the IP).
type AgentClient interface {
	ListModels(ctx context.Context, userID, workspaceID string) ([]byte, error)
	PatchConfig(ctx context.Context, userID, workspaceID string, config map[string]any) error
	DisposeInstance(ctx context.Context, userID, workspaceID string) error
	GetSessionStatuses(ctx context.Context, userID, workspaceID string) (map[string]string, error)
	StageCredentials(ctx context.Context, userID, workspaceID string, providers []secrets.LLMProviderData) error
}

// WorkspaceClient implements AgentClient by resolving each call to a
// specific pod IP + password, then delegating to the low-level Client.
// It is constructed once and shared across handlers; each call resolves
// the workspace's current podIP + password fresh, so pod migrations and
// password rotations are transparent to callers.
type WorkspaceClient struct {
	passwordResolver PasswordResolver
	podIPResolver    PodIPResolver
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
		logger:           logger,
	}
}

// agentPort is the opencode HTTP port. Package-level so tests can
// override it to point at a test server.
var agentPort = agentd.AgentPort

// resolve returns a low-level Client configured for the workspace's pod.
func (w *WorkspaceClient) resolve(ctx context.Context, userID, workspaceID string) (*Client, error) {
	podIP, err := w.podIPResolver.GetWorkspacePodIP(ctx, userID, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("resolve pod IP for workspace %s: %w", workspaceID, err)
	}
	if podIP == "" {
		return nil, fmt.Errorf("no running pod for workspace %s", workspaceID)
	}
	password, err := w.passwordResolver(ctx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("resolve password for workspace %s: %w", workspaceID, err)
	}
	baseURL := fmt.Sprintf("http://%s:%d", podIP, agentPort)
	return NewClient(baseURL, password, w.logger), nil
}

func (w *WorkspaceClient) ListModels(ctx context.Context, userID, workspaceID string) ([]byte, error) {
	c, err := w.resolve(ctx, userID, workspaceID)
	if err != nil {
		return nil, err
	}
	return c.ListModels(ctx)
}

func (w *WorkspaceClient) PatchConfig(ctx context.Context, userID, workspaceID string, config map[string]any) error {
	c, err := w.resolve(ctx, userID, workspaceID)
	if err != nil {
		return err
	}
	return c.PatchConfig(ctx, config)
}

func (w *WorkspaceClient) DisposeInstance(ctx context.Context, userID, workspaceID string) error {
	c, err := w.resolve(ctx, userID, workspaceID)
	if err != nil {
		return err
	}
	return c.DisposeInstance(ctx)
}

func (w *WorkspaceClient) GetSessionStatuses(ctx context.Context, userID, workspaceID string) (map[string]string, error) {
	c, err := w.resolve(ctx, userID, workspaceID)
	if err != nil {
		return nil, err
	}
	return c.GetSessionStatuses(ctx)
}

func (w *WorkspaceClient) StageCredentials(ctx context.Context, userID, workspaceID string, providers []secrets.LLMProviderData) error {
	c, err := w.resolve(ctx, userID, workspaceID)
	if err != nil {
		return err
	}
	return c.StageCredentials(ctx, providers)
}

// Compile-time check that WorkspaceClient satisfies AgentClient.
var _ AgentClient = (*WorkspaceClient)(nil)
