// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package interfaces

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
)

// SyncWorkspacePhase used to live on DatabaseService when the workspace phase
// was cached in the DB. Migration 9 dropped the cache; the CRD is the only
// source of truth. This test guards against re-introducing the method by
// accident (e.g. via an auto-generated mock pulled in from an old branch).
func TestDatabaseService_NoSyncWorkspacePhase(t *testing.T) {
	typ := reflect.TypeOf((*DatabaseService)(nil)).Elem()
	for i := 0; i < typ.NumMethod(); i++ {
		assert.NotEqual(t, "SyncWorkspacePhase", typ.Method(i).Name,
			"DatabaseService must not declare SyncWorkspacePhase; phase is owned by the CRD")
	}
}
