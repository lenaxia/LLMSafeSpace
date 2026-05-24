package controller

import (
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/lenaxia/llmsafespace/controller/internal/workspace"
)

func SetupControllers(mgr ctrl.Manager) error {
	logger := log.Log.WithName("controller")
	logger.Info("Setting up controllers")

	if err := (&workspace.WorkspaceReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		logger.Error(err, "unable to create Workspace controller")
		return err
	}

	return nil
}
