// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package controller

import (
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/lenaxia/llmsafespace/controller/internal/workspace"
	"github.com/lenaxia/llmsafespace/pkg/agent/opencode"
)

func init() {
	opencode.Register()
}

func SetupControllers(mgr ctrl.Manager, inferenceRelayURL string) error {
	logger := log.Log.WithName("controller")
	logger.Info("Setting up controllers")

	if err := (&workspace.WorkspaceReconciler{
		Client:            mgr.GetClient(),
		Scheme:            mgr.GetScheme(),
		InferenceRelayURL: inferenceRelayURL,
	}).SetupWithManager(mgr); err != nil {
		logger.Error(err, "unable to create Workspace controller")
		return err
	}

	return nil
}
