// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package canary

import (
	"context"
	"fmt"
	"os"
	"time"

	llm "github.com/lenaxia/llmsafespaces/sdk/go"
)

// Config holds all environment-sourced canary configuration.
type Config struct {
	APIURL      string
	APIKey      string
	APIKeyUser2 string
	Email       string
	Password    string
	LLMProvider string
	LLMAPIKey   string
	LLMModel    string
	BadModel    string
}

// ConfigFromEnv reads canary configuration from environment variables.
func ConfigFromEnv() Config {
	apiURL := os.Getenv("LLMSAFESPACES_URL")
	if apiURL == "" {
		apiURL = "http://localhost:8080"
	}
	badModel := os.Getenv("LLMSAFESPACES_BAD_MODEL")
	if badModel == "" {
		badModel = "invalid-provider/no-such-model"
	}
	llmProvider := os.Getenv("LLMSAFESPACES_LLM_PROVIDER")
	if llmProvider == "" {
		llmProvider = "anthropic"
	}
	return Config{
		APIURL:      apiURL,
		APIKey:      os.Getenv("LLMSAFESPACES_API_KEY"),
		APIKeyUser2: os.Getenv("LLMSAFESPACES_API_KEY_USER2"),
		Email:       os.Getenv("LLMSAFESPACES_EMAIL"),
		Password:    os.Getenv("LLMSAFESPACES_PASSWORD"),
		LLMProvider: llmProvider,
		LLMAPIKey:   os.Getenv("LLMSAFESPACES_LLM_API_KEY"),
		LLMModel:    os.Getenv("LLMSAFESPACES_LLM_MODEL"),
		BadModel:    badModel,
	}
}

// NewClient creates a new SDK client with the given API key.
func (cfg Config) NewClient(apiKey string) *llm.Client {
	return llm.New(cfg.APIURL,
		llm.WithAPIKey(apiKey),
		llm.WithTimeout(60*time.Second),
	)
}

// JWTClient returns a client using email/password JWT authentication.
//
// Some endpoints (user-credential CRUD, secret CRUD with user-DEK
// encryption) require a session-scoped DEK that is only derived during
// JWT login. API-key auth alone returns 403 "encryption key not
// available; re-authenticate" on these paths. Scenarios that exercise
// such endpoints MUST use JWTClient instead of Client().
//
// Requires LLMSAFESPACES_EMAIL + LLMSAFESPACES_PASSWORD env vars.
func (cfg Config) JWTClient() *llm.Client {
	return llm.New(cfg.APIURL,
		llm.WithCredentials(cfg.Email, cfg.Password),
		llm.WithTimeout(60*time.Second),
	)
}

// Client returns a client using the primary API key.
func (cfg Config) Client() *llm.Client {
	return cfg.NewClient(cfg.APIKey)
}

// Client2 returns a client using the secondary API key (for ownership tests).
func (cfg Config) Client2() *llm.Client {
	return cfg.NewClient(cfg.APIKeyUser2)
}

// BadClient returns a client using a demonstrably invalid API key.
func (cfg Config) BadClient() *llm.Client {
	return cfg.NewClient("lsp_invalid_canary_key_000000000000")
}

// WaitPhase polls until the workspace reaches the target phase or deadline.
func WaitPhase(ctx context.Context, c *llm.Client, id, target string, limit time.Duration) string {
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		ws, err := c.Workspaces.Get(ctx, id)
		if err == nil && ws.Phase == target {
			return ws.Phase
		}
		select {
		case <-ctx.Done():
			return "ctx-canceled"
		case <-time.After(3 * time.Second):
		}
	}
	ws, _ := c.Workspaces.Get(ctx, id)
	if ws != nil {
		return ws.Phase
	}
	return "unknown"
}

// WaitPhaseNot polls until the workspace phase is NOT target.
func WaitPhaseNot(ctx context.Context, c *llm.Client, id, notTarget string, limit time.Duration) string {
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		ws, err := c.Workspaces.Get(ctx, id)
		if err == nil && ws.Phase != notTarget {
			return ws.Phase
		}
		select {
		case <-ctx.Done():
			return notTarget
		case <-time.After(3 * time.Second):
		}
	}
	ws, _ := c.Workspaces.Get(ctx, id)
	if ws != nil {
		return ws.Phase
	}
	return notTarget
}

// WaitActive polls until the workspace reaches Active phase.
func WaitActive(ctx context.Context, c *llm.Client, id string) string {
	return WaitPhase(ctx, c, id, "Active", 150*time.Second)
}

// EnsureSessionWithRetry retries ensure-session up to maxTries with a sleep between.
func EnsureSessionWithRetry(ctx context.Context, c *llm.Client, wsID string, maxTries int) (*llm.EnsureSessionResponse, error) {
	var lastErr error
	for i := 0; i < maxTries; i++ {
		sess, err := c.Sessions.Ensure(ctx, wsID)
		if err == nil && sess.SessionID != "" {
			return sess, nil
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
	return nil, fmt.Errorf("ensure session failed after %d tries: %w", maxTries, lastErr)
}
