// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package workspace

import (
	"errors"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	promtest "github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// fakeConflictErr returns an error recognised by apierrors.IsConflict.
func fakeConflictErr() error {
	return apierrors.NewConflict(
		schema.GroupResource{Group: "llmsafespace.dev", Resource: "workspaces"},
		"ws-x",
		errors.New("the object has been modified"),
	)
}

// fakeNotFoundErr returns an error recognised by apierrors.IsNotFound.
func fakeNotFoundErr() error {
	return apierrors.NewNotFound(
		schema.GroupResource{Group: "llmsafespace.dev", Resource: "workspaces"},
		"ws-x",
	)
}

func TestRecordStatusUpdateConflict_IncrementsBySite(t *testing.T) {
	ctr := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "x"}, []string{"site"})

	recordStatusUpdateConflictInto(ctr, "phase_active")
	recordStatusUpdateConflictInto(ctr, "phase_active")
	recordStatusUpdateConflictInto(ctr, "phase_suspend")

	assert.Equal(t, 2.0, promtest.ToFloat64(ctr.WithLabelValues("phase_active")))
	assert.Equal(t, 1.0, promtest.ToFloat64(ctr.WithLabelValues("phase_suspend")))
	assert.Equal(t, 0.0, promtest.ToFloat64(ctr.WithLabelValues("never_called")))
}

func TestRecordStatusUpdateConflict_OnError_IncrementsOnlyOnConflict(t *testing.T) {
	ctr := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "x"}, []string{"site"})

	// Conflict path increments.
	recordStatusUpdateConflictOnErrorInto(ctr, "raw_site", fakeConflictErr())
	// Non-conflict paths do not.
	recordStatusUpdateConflictOnErrorInto(ctr, "raw_site", fakeNotFoundErr())
	recordStatusUpdateConflictOnErrorInto(ctr, "raw_site", errors.New("internal"))
	// Nil error does not.
	recordStatusUpdateConflictOnErrorInto(ctr, "raw_site", nil)

	assert.Equal(t, 1.0, promtest.ToFloat64(ctr.WithLabelValues("raw_site")),
		"exactly one conflict → exactly one increment")
}

// TestRecordStatusUpdateConflict_PanicsOnNilCounter verifies the helper is
// safe to call without test setup — important because production callers
// don't go through this code path; they use the package-level wrapper which
// binds metrics.WorkspaceStatusUpdateConflictsTotal at import time.
func TestRecordStatusUpdateConflict_NilCounterDoesNotPanic(t *testing.T) {
	assert.NotPanics(t, func() {
		recordStatusUpdateConflictInto(nil, "site")
		recordStatusUpdateConflictOnErrorInto(nil, "site", fakeConflictErr())
	})
}
