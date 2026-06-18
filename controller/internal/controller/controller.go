// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package controller

import (
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/lenaxia/llmsafespace/controller/internal/relay"
	"github.com/lenaxia/llmsafespace/controller/internal/workspace"
	"github.com/lenaxia/llmsafespace/pkg/agent/opencode"
)

func init() {
	opencode.Register()
}

func SetupControllers(mgr ctrl.Manager, inferenceRelayURL, inferenceRelaySecret string) error {
	logger := log.Log.WithName("controller")
	logger.Info("Setting up controllers")

	if err := (&workspace.WorkspaceReconciler{
		Client:               mgr.GetClient(),
		Scheme:               mgr.GetScheme(),
		InferenceRelayURL:    inferenceRelayURL,
		InferenceRelaySecret: inferenceRelaySecret,
	}).SetupWithManager(mgr); err != nil {
		logger.Error(err, "unable to create Workspace controller")
		return err
	}

	return nil
}

// SetupRelayController registers the InferenceRelay reconciler.
// It is feature-gated and only activated when enableRelay is true.
func SetupRelayController(mgr ctrl.Manager, namespace, routerURL string, enableRelay bool) error {
	if !enableRelay {
		return nil
	}

	logger := log.Log.WithName("controller")
	logger.Info("Setting up InferenceRelay controller")

	relayReconciler := &relay.InferenceRelayReconciler{
		Client:        mgr.GetClient(),
		Scheme:        mgr.GetScheme(),
		Namespace:     namespace,
		HealthChecker: relay.NewHealthChecker(routerURL),
		Drivers: map[string]relay.ProviderDriver{
			"aws": relay.NewAWSDriver(mgr.GetClient(), namespace, "aws-relay-irwa"),
			"oci": relay.NewOCIDriver(mgr.GetClient(), namespace, "oci-credentials"),
		},
		ExpectedCredentialSecrets: map[string]string{
			"aws": "aws-relay-irwa",
			"oci": "oci-credentials",
		},
	}

	if err := relayReconciler.SetupWithManager(mgr); err != nil {
		logger.Error(err, "unable to create InferenceRelay controller")
		return err
	}

	return nil
}
