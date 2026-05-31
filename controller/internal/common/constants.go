// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package common

// Controller-related constants
const (
	ControllerName = "controller"

	// Annotation keys
	AnnotationCreatedBy  = "llmsafespace.dev/created-by"
	AnnotationRuntimeEnv = "llmsafespace.dev/runtime-env"

	// Label keys
	LabelApp       = "app"
	LabelComponent = "component"
	LabelRuntime   = "runtime"
	LabelStatus    = "status"
	LabelWorkspace = "llmsafespace.dev/workspace"

	// Condition types
	ConditionReady      = "Ready"
	ConditionPodCreated = "PodCreated"
	ConditionPodRunning = "PodRunning"

	// Condition reasons
	ReasonPodCreated        = "PodCreated"
	ReasonPodCreationFailed = "PodCreationFailed"
	ReasonPodRunning        = "PodRunning"
	ReasonPodNotRunning     = "PodNotRunning"
)
