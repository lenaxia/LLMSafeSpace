// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package common

import (
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	// WorkspaceesCreated tracks the number of workspacees created
	WorkspaceesCreated = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "llmsafespace_workspacees_created_total",
			Help: "Number of workspacees created",
		},
		[]string{"runtime", "security_level"},
	)

	// WorkspaceesDeleted tracks the number of workspacees deleted
	WorkspaceesDeleted = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "llmsafespace_workspacees_deleted_total",
			Help: "Number of workspacees deleted",
		},
		[]string{"runtime", "security_level"},
	)

	// WorkspaceesRunning tracks the number of workspacees currently running
	WorkspaceesRunning = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "llmsafespace_workspacees_running",
			Help: "Number of workspacees currently running",
		},
		[]string{"runtime", "security_level"},
	)

	// WorkspaceCreationDuration tracks the time taken to create a workspace
	WorkspaceCreationDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "llmsafespace_workspace_creation_duration_seconds",
			Help:    "Time taken to create a workspace",
			Buckets: prometheus.ExponentialBuckets(0.1, 2, 10),
		},
		[]string{"runtime", "security_level"},
	)
)

func init() {
	metrics.Registry.MustRegister(
		WorkspaceesCreated,
		WorkspaceesDeleted,
		WorkspaceesRunning,
		WorkspaceCreationDuration,
	)
}
