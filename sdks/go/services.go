// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package llmsafespace

import (
	"context"
	"encoding/json"
	"fmt"
)

// WorkspacesService handles workspace operations.
type WorkspacesService struct{ c *Client }

func (s *WorkspacesService) List(ctx context.Context, limit, offset int) (*WorkspaceListResult, error) {
	var result WorkspaceListResult
	err := s.c.do(ctx, "GET", fmt.Sprintf("/workspaces?limit=%d&offset=%d", limit, offset), nil, &result)
	return &result, err
}

func (s *WorkspacesService) Create(ctx context.Context, req CreateWorkspaceRequest) (*Workspace, error) {
	var ws Workspace
	err := s.c.do(ctx, "POST", "/workspaces", req, &ws)
	return &ws, err
}

func (s *WorkspacesService) Get(ctx context.Context, id string) (*Workspace, error) {
	var ws Workspace
	err := s.c.do(ctx, "GET", "/workspaces/"+id, nil, &ws)
	return &ws, err
}

func (s *WorkspacesService) Delete(ctx context.Context, id string) error {
	return s.c.do(ctx, "DELETE", "/workspaces/"+id, nil, nil)
}

func (s *WorkspacesService) Suspend(ctx context.Context, id string) error {
	return s.c.do(ctx, "POST", "/workspaces/"+id+"/suspend", nil, nil)
}

func (s *WorkspacesService) Resume(ctx context.Context, id string) error {
	return s.c.do(ctx, "POST", "/workspaces/"+id+"/resume", nil, nil)
}

// SessionsService handles session operations.
type SessionsService struct{ c *Client }

func (s *SessionsService) Ensure(ctx context.Context, workspaceID string) (*EnsureSessionResponse, error) {
	var resp EnsureSessionResponse
	err := s.c.do(ctx, "POST", "/workspaces/"+workspaceID+"/sessions/new", nil, &resp)
	return &resp, err
}

func (s *SessionsService) SendMessage(ctx context.Context, workspaceID, sessionID, content string) (*MessageResponse, error) {
	body := map[string]any{
		"content": content,
		"parts":   []map[string]string{{"type": "text", "text": content}},
	}
	var raw json.RawMessage
	err := s.c.do(ctx, "POST", fmt.Sprintf("/workspaces/%s/sessions/%s/message", workspaceID, sessionID), body, &raw)
	if err != nil {
		return nil, err
	}
	text := extractText(raw)
	return &MessageResponse{Raw: raw, Content: text}, nil
}

func (s *SessionsService) GetHistory(ctx context.Context, workspaceID, sessionID string) ([]json.RawMessage, error) {
	var result []json.RawMessage
	err := s.c.do(ctx, "GET", fmt.Sprintf("/workspaces/%s/sessions/%s/message", workspaceID, sessionID), nil, &result)
	return result, err
}

func (s *SessionsService) Abort(ctx context.Context, workspaceID, sessionID string) error {
	return s.c.do(ctx, "POST", fmt.Sprintf("/workspaces/%s/sessions/%s/abort", workspaceID, sessionID), nil, nil)
}

// AuthService handles authentication operations.
type AuthService struct{ c *Client }

func (s *AuthService) Me(ctx context.Context) (map[string]any, error) {
	var result map[string]any
	err := s.c.do(ctx, "GET", "/auth/me", nil, &result)
	return result, err
}

// SecretsService handles secret operations.
type SecretsService struct{ c *Client }

func (s *SecretsService) Create(ctx context.Context, name, secretType, value string) (*SecretResponse, error) {
	body := map[string]string{"name": name, "type": secretType, "value": value}
	var resp SecretResponse
	err := s.c.do(ctx, "POST", "/secrets", body, &resp)
	return &resp, err
}

func (s *SecretsService) List(ctx context.Context) ([]SecretResponse, error) {
	var result []SecretResponse
	err := s.c.do(ctx, "GET", "/secrets", nil, &result)
	return result, err
}

func (s *SecretsService) Delete(ctx context.Context, id string) error {
	return s.c.do(ctx, "DELETE", "/secrets/"+id, nil, nil)
}

// TerminalService handles terminal operations.
type TerminalService struct{ c *Client }

func (s *TerminalService) GetTicket(ctx context.Context, workspaceID string) (*TerminalTicket, error) {
	var ticket TerminalTicket
	err := s.c.do(ctx, "POST", "/workspaces/"+workspaceID+"/terminal/ticket", nil, &ticket)
	return &ticket, err
}

func extractText(raw json.RawMessage) string {
	var obj struct {
		Parts []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"parts"`
	}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return ""
	}
	var sb string
	for _, p := range obj.Parts {
		if p.Type == "text" {
			sb += p.Text
		}
	}
	return sb
}
