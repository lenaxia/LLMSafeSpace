// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package kubernetes

import (
	"github.com/lenaxia/llmsafespaces/api/internal/config"
	"github.com/lenaxia/llmsafespaces/pkg/interfaces"
	k8s "github.com/lenaxia/llmsafespaces/pkg/kubernetes"
	"github.com/lenaxia/llmsafespaces/pkg/logger"
)

// NewClient creates a new Kubernetes client from the API config
func NewClient(cfg *config.Config, log *logger.Logger) (interfaces.KubernetesClient, error) {
	// Pass just the Kubernetes config to the client
	return k8s.New(&cfg.Kubernetes, log)
}
