package controller

import (
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/llmsafespace/sandbox-controller/internal/sandbox"
	"github.com/llmsafespace/sandbox-controller/internal/warmpool"
	"github.com/llmsafespace/sandbox-controller/internal/warmpod"
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
