package kubernetes

import (
	"github.com/lenaxia/llmsafespace/api/internal/config"
	"github.com/lenaxia/llmsafespace/pkg/interfaces"
	k8s "github.com/lenaxia/llmsafespace/pkg/kubernetes"
	"github.com/lenaxia/llmsafespace/pkg/logger"
)

// NewClient creates a new Kubernetes client from the API config
func NewClient(cfg *config.Config, log *logger.Logger) (interfaces.KubernetesClient, error) {
	// Pass just the Kubernetes config to the client
	return k8s.New(&cfg.Kubernetes, log)
}
