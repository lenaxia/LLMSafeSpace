// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package workspace

import "time"

const WorkspaceFinalizer = "workspace.llmsafespaces.dev/finalizer"

// Pod naming: {workspaceName}-{uid[:8]}
const podNameSuffix = 8

// Requeue intervals.
const (
	requeueCreating = 5 * time.Second
	requeueActive   = 15 * time.Second
)

// pendingPhaseTimeout is how long a workspace can stay in Pending before
// entering recovery backoff.
const pendingPhaseTimeout = 5 * time.Minute

// Labels applied to workspace pods.
const (
	LabelApp       = "app"
	LabelComponent = "component"
	LabelWorkspace = "llmsafespaces.dev/workspace"
	LabelRuntime   = "runtime"

	AppName            = "llmsafespaces"
	ComponentWorkspace = "workspace"
)

// Password secret naming.
func passwordSecretName(workspaceName string) string {
	return "workspace-pw-" + workspaceName
}

// Pod name from workspace name and UID.
func podName(workspaceName string, uid string) string {
	suffix := uid
	if len(suffix) > podNameSuffix {
		suffix = suffix[:podNameSuffix]
	}
	return workspaceName + "-" + suffix
}
