package controller

import (
	"fmt"

	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/lenaxia/llmsafespace/controller/internal/sandbox"
	"github.com/lenaxia/llmsafespace/controller/internal/warmpool"
	"github.com/lenaxia/llmsafespace/controller/internal/warmpod"
)

// InitializeControllers initializes all controllers with the manager
func InitializeControllers(mgr ctrl.Manager) error {
	// Set up sandbox controller
	if err := (&sandbox.SandboxReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		return fmt.Errorf("unable to create sandbox controller: %w", err)
	}
	
	// Set up warm pool controller
	if err := (&warmpool.WarmPoolReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		return fmt.Errorf("unable to create warm pool controller: %w", err)
	}
	
	// Set up warm pod controller
	if err := (&warmpod.WarmPodReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		return fmt.Errorf("unable to create warm pod controller: %w", err)
	}
	
	return nil
}
