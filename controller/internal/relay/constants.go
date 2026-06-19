// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package relay

import "time"

const (
	// InferenceRelayFinalizer is the finalizer name for cleanup on CR deletion.
	InferenceRelayFinalizer = "inferencerelay.llmsafespaces.dev/finalizer"

	// Requeue intervals for different reconcile outcomes.
	requeueProvisioning = 10 * time.Second // waiting for VM to come up
	requeueHealthy      = 30 * time.Second // steady-state health check
	requeueDegraded     = 15 * time.Second // unhealthy relay, faster checking
	requeuePaused       = 5 * time.Minute  // paused fleet, occasional check
	requeueError        = 30 * time.Second // after error, retry soon

	// maxProvisioningAttempts is the max consecutive config-error provisioning
	// attempts before setting ProvisioningFailed condition and stopping retries.
	maxProvisioningAttempts = 3

	// WG IP allocation map within the 10.42.42.0/24 mesh.
	// Router is always .1. Relays are assigned by provider.
	wgRouterIP = "10.42.42.1"
	wgAWSRelay = "10.42.42.4"
	wgOCIRelay = "10.42.42.2"
	wgGCPRelay = "10.42.42.3"

	// Default shapes per provider.
	defaultShapeAWS = "t4g.micro"
	defaultShapeOCI = "VM.Standard.A1.Flex"
	defaultShapeGCP = "e2-micro"

	// Default regions per provider.
	defaultRegionAWS = "us-east-1"
	defaultRegionOCI = "us-ashburn-1"
	defaultRegionGCP = "us-west1"

	// ConfigMap names managed by the reconciler.
	routerPeersConfigMap = "relay-router-peers"
	routerWGSecret       = "relay-router-wg"

	// Annotation keys read by the reconciler.
	annotationRotate = "relay.llmsafespaces.dev/rotate"
	annotationPaused = "relay.llmsafespaces.dev/paused"
)

// wgIPForProvider returns the WireGuard mesh IP for a given provider.
func wgIPForProvider(provider string) string {
	switch provider {
	case "aws":
		return wgAWSRelay
	case "oci":
		return wgOCIRelay
	case "gcp":
		return wgGCPRelay
	default:
		return ""
	}
}

// defaultShapeForProvider returns the default VM shape for a provider.
func defaultShapeForProvider(provider string) string {
	switch provider {
	case "aws":
		return defaultShapeAWS
	case "oci":
		return defaultShapeOCI
	case "gcp":
		return defaultShapeGCP
	default:
		return ""
	}
}

// defaultRegionForProvider returns the default region for a provider.
func defaultRegionForProvider(provider string) string {
	switch provider {
	case "aws":
		return defaultRegionAWS
	case "oci":
		return defaultRegionOCI
	case "gcp":
		return defaultRegionGCP
	default:
		return ""
	}
}
