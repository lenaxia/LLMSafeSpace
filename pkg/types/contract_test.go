// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package types

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/lenaxia/llmsafespace/pkg/agent"
)

// TestGenerateContractFixtures outputs JSON fixtures that the frontend
// contract test validates against. Run with:
//
//	go test -run TestGenerateContractFixtures ./pkg/types/ -v
//
// The output file is consumed by frontend/src/api/contract.test.ts
func TestGenerateContractFixtures(t *testing.T) {
	now := time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC)
	seenAt := time.Date(2026, 5, 24, 11, 0, 0, 0, time.UTC)
	contextUsed := int64(12500)

	fixtures := map[string]interface{}{
		"AuthConfig": AuthConfig{
			RegistrationEnabled: true,
			OIDCEnabled:         false,
			SSOProviders:        []string{"okta"},
		},
		"ActivateWorkspaceResponse": ActivateWorkspaceResponse{
			Resumed:   "ws-1",
			Suspended: "ws-old",
		},
		"SessionListItem": SessionListItem{
			ID:            "sess-1",
			Title:         "Chat about auth",
			ParentID:      "sess-root",
			LastMessageAt: &now,
			MessageCount:  12,
			Status:        "active",
			LastSeenAt:    &seenAt,
			HasUnread:     true,
			ContextUsed:   &contextUsed,
		},
		"ActiveSessionsResponse": ActiveSessionsResponse{
			Active:    []string{"sess-1", "sess-2"},
			MaxActive: 5,
		},
		"WorkspaceListItem": WorkspaceListItem{
			ID:                "ws-1",
			Name:              "alpha",
			UserID:            "u-123",
			Runtime:           "python:3.11",
			StorageSize:       "5Gi",
			Phase:             "Active",
			MaxActiveSessions: 5,
			CreatedAt:         now,
			UpdatedAt:         now,
		},
		"AuthResponse": AuthResponse{
			Token: "jwt-token",
			User: User{
				ID:       "u-123",
				Username: "alice",
				Email:    "alice@test.com",
				Role:     "user",
				Active:   true,
			},
		},
		// QuestionRequest / PermissionRequest live in pkg/agent because they
		// are agent-protocol payloads, not API response types. We include
		// them here because the frontend's contract.test.ts asserts the
		// JSON shape and needs a single fixtures.json to read from.
		"QuestionRequest": agent.QuestionRequest{
			ID:            "que_18b28260affeoxXrX1iwPH8wFg",
			SessionID:     "ses_18b28260affeoxXrX1iwPH8wFg",
			RootSessionID: "ses_18b28260affeoxXrX1iwPH8wFg",
			Questions: []agent.QuestionInfo{
				{
					Question: "What programming language do you want to use?",
					Header:   "Choose language",
					Options: []agent.QuestionOption{
						{Label: "Go", Description: "Fast compiled language"},
						{Label: "Python", Description: "Easy scripting"},
					},
					Multiple: false,
				},
			},
			Tool: &agent.ToolRef{MessageID: "msg_abc", CallID: "call_xyz"},
		},
		"PermissionRequest": agent.PermissionRequest{
			ID:            "per_18b28260affeoxXrX1iwPH8wFg",
			SessionID:     "ses_18b28260affeoxXrX1iwPH8wFg",
			RootSessionID: "ses_18b28260affeoxXrX1iwPH8wFg",
			Permission:    "shell",
			Patterns:      []string{"/workspace/src/main.go"},
			Metadata:      map[string]interface{}{"command": "go build"},
			Always:        []string{"/workspace/*"},
			Tool:          &agent.ToolRef{MessageID: "msg_abc", CallID: "call_xyz"},
		},
	}

	data, err := json.MarshalIndent(fixtures, "", "  ")
	if err != nil {
		t.Fatalf("failed to marshal fixtures: %v", err)
	}

	outPath := "../../frontend/src/api/contract-fixtures.json"
	if err := os.WriteFile(outPath, data, 0644); err != nil {
		// If frontend dir doesn't exist (CI without frontend checkout), skip
		t.Skipf("skipping fixture write (frontend not present): %v", err)
	}
	t.Logf("wrote contract fixtures to %s", outPath)
}
