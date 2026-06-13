// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package database

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestSyncWorkspaceVersionInfo_BothFields(t *testing.T) {
	svc, mock, cleanup := setupMockDB(t)
	defer cleanup()

	mock.ExpectExec(`UPDATE workspaces SET image_tag = \$1, agent_version = \$2`).
		WithArgs("ts-1781332002", "1.15.12", "ws-1").
		WillReturnResult(sqlmock.NewResult(1, 1))

	svc.SyncWorkspaceVersionInfo(context.Background(), "ws-1", "ts-1781332002", "1.15.12")

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSyncWorkspaceVersionInfo_ImageTagOnly_DoesNotClobberAgentVersion(t *testing.T) {
	// When agentVersion is empty, only image_tag should be updated.
	// agent_version must NOT appear in the SET clause — passing "" would overwrite it.
	svc, mock, cleanup := setupMockDB(t)
	defer cleanup()

	mock.ExpectExec(`UPDATE workspaces SET image_tag = \$1`).
		WithArgs("ts-1781332002", "ws-1").
		WillReturnResult(sqlmock.NewResult(1, 1))

	svc.SyncWorkspaceVersionInfo(context.Background(), "ws-1", "ts-1781332002", "")

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSyncWorkspaceVersionInfo_AgentVersionOnly_DoesNotClobberImageTag(t *testing.T) {
	// When imageTag is empty, only agent_version should be updated.
	svc, mock, cleanup := setupMockDB(t)
	defer cleanup()

	mock.ExpectExec(`UPDATE workspaces SET agent_version = \$1`).
		WithArgs("1.15.12", "ws-1").
		WillReturnResult(sqlmock.NewResult(1, 1))

	svc.SyncWorkspaceVersionInfo(context.Background(), "ws-1", "", "1.15.12")

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSyncWorkspaceVersionInfo_BothEmpty_NoOp(t *testing.T) {
	// Both empty — must not execute any query.
	svc, mock, cleanup := setupMockDB(t)
	defer cleanup()

	// No expectations set: any query execution would fail the mock.
	svc.SyncWorkspaceVersionInfo(context.Background(), "ws-1", "", "")

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSyncWorkspaceVersionInfo_EmptyWorkspaceID_NoOp(t *testing.T) {
	svc, mock, cleanup := setupMockDB(t)
	defer cleanup()

	svc.SyncWorkspaceVersionInfo(context.Background(), "", "ts-1781332002", "1.15.12")

	require.NoError(t, mock.ExpectationsWereMet())
}
