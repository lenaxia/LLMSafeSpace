package types

import (
	"encoding/json"
	"os"
	"testing"
	"time"
)

// TestGenerateContractFixtures outputs JSON fixtures that the frontend
// contract test validates against. Run with:
//
//	go test -run TestGenerateContractFixtures ./pkg/types/ -v
//
// The output file is consumed by frontend/src/api/contract.test.ts
func TestGenerateContractFixtures(t *testing.T) {
	now := time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC)

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
			LastMessageAt: &now,
			MessageCount:  12,
			Status:        "active",
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
