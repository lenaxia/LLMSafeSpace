package controller

import (
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/lenaxia/llmsafespace/src/sandbox-controller/internal/sandbox"
	"github.com/lenaxia/llmsafespace/src/sandbox-controller/internal/warmpool"
	"github.com/lenaxia/llmsafespace/src/sandbox-controller/internal/warmpod"
)

// SetupControllers sets up all controllers with the Manager
func SetupControllers(mgr ctrl.Manager) error {
	logger := log.Log.WithName("controller")
	logger.Info("Setting up controllers")

	// Set up the Sandbox controller
	if err := (&sandbox.SandboxReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		logger.Error(err, "unable to create Sandbox controller")
		return err
	}

	// Set up the WarmPool controller
	if err := (&warmpool.WarmPoolReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		logger.Error(err, "unable to create WarmPool controller")
		return err
	}

	// Set up the WarmPod controller
	if err := (&warmpod.WarmPodReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		logger.Error(err, "unable to create WarmPod controller")
		return err
	}

	return nil
}
