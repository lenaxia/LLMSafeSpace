// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package relay

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	v1 "github.com/lenaxia/llmsafespace/pkg/apis/llmsafespace/v1"
	"github.com/lenaxia/llmsafespace/controller/internal/common"
)

// InferenceRelayReconciler reconciles InferenceRelay CRs to manage the
// full lifecycle of relay VMs across cloud providers.
type InferenceRelayReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	// Namespace is the controller's namespace (for ConfigMaps, Secrets).
	Namespace string

	// HealthChecker scrapes the relay-router /metrics endpoint.
	HealthChecker *HealthChecker

	// Drivers map provider name → driver implementation.
	Drivers map[string]ProviderDriver
}

// Reconcile handles the InferenceRelay CR lifecycle.
func (r *InferenceRelayReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("inferencerelay", req.NamespacedName)

	relay := &v1.InferenceRelay{}
	if err := r.Get(ctx, req.NamespacedName, relay); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Handle deletion
	if !relay.DeletionTimestamp.IsZero() {
		return r.handleDeletion(ctx, relay)
	}

	// Add finalizer if missing
	if common.AddFinalizer(relay, InferenceRelayFinalizer) {
		if err := r.Update(ctx, relay); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Check if paused
	if relay.Annotations[annotationPaused] == "true" {
		logger.Info("Relay fleet is paused — skipping provisioning")
		return ctrl.Result{RequeueAfter: requeuePaused}, nil
	}

	// Handle rotation annotation
	if rotateID := relay.Annotations[annotationRotate]; rotateID != "" {
		if err := r.handleRotation(ctx, relay, rotateID); err != nil {
			logger.Error(err, "rotation failed")
			return ctrl.Result{RequeueAfter: requeueError}, err
		}
		// Clear the rotation annotation
		delete(relay.Annotations, annotationRotate)
		if err := r.Update(ctx, relay); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: requeueProvisioning}, nil
	}

	// Main provisioning/health loop
	return r.reconcileFleet(ctx, relay)
}

// reconcileFleet provisions missing VMs, checks health, and updates status.
func (r *InferenceRelayReconciler) reconcileFleet(ctx context.Context, relay *v1.InferenceRelay) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	needsRequeue := false

	// Scrape router health if available
	var healthReport *HealthReport
	if r.HealthChecker != nil {
		report, err := r.HealthChecker.Scrape(ctx)
		if err != nil {
			logger.V(1).Info("failed to scrape router health", "error", err.Error())
		} else {
			healthReport = report
		}
	}

	// Track existing instances by provider
	existingByProvider := make(map[string]*v1.RelayInstanceStatus)
	for i := range relay.Status.Instances {
		inst := &relay.Status.Instances[i]
		existingByProvider[inst.Provider] = inst
	}

	// Provision missing VMs for each provider in spec
	instances := make([]v1.RelayInstanceStatus, 0, len(relay.Spec.Providers))
	for _, providerSpec := range relay.Spec.Providers {
		provider := providerSpec.Provider

		if existing, ok := existingByProvider[provider]; ok {
			// Update health from router metrics
			if healthReport != nil {
				if h, found := healthReport.Relays[existing.ID]; found {
					existing.Healthy = h.Healthy
					existing.Requests429 = int(h.Requests429)
					existing.TotalRequests = int(h.Requests)
					existing.EgressBytes = h.EgressBytes
				}
			}
			if existing.LastCheck == nil {
				now := metav1.Now()
				existing.LastCheck = &now
			}
			instances = append(instances, *existing)
			continue
		}

		// Need to provision a new VM
		result, err := r.provisionRelay(ctx, relay, providerSpec)
		if err != nil {
			logger.Error(err, "provisioning failed", "provider", provider)
			if IsConfigError(err) {
				instances = append(instances, v1.RelayInstanceStatus{
					ID:                   fmt.Sprintf("%s-provisioning", provider),
					Provider:             provider,
					Region:               providerSpec.Region,
					State:                string(v1.RelayStateProvisioningFailed),
					Healthy:              false,
					ProvisioningAttempts: 1,
					LastProvisionError:   err.Error(),
				})
			}
			needsRequeue = true
			continue
		}

		instances = append(instances, v1.RelayInstanceStatus{
			ID:       result.InstanceID,
			Provider: provider,
			Region:   providerSpec.Region,
			WgIP:     wgIPForProvider(provider),
			PublicIP: result.PublicIP,
			State:    string(v1.RelayStateProvisioning),
			Healthy:  false,
		})
		needsRequeue = true
	}

	// Count healthy replicas
	healthyReplicas := 0
	for _, inst := range instances {
		if inst.Healthy {
			healthyReplicas++
		}
	}

	// Build peer entries for ConfigMap
	peers := make([]PeerEntry, 0, len(instances))
	for _, inst := range instances {
		state := inst.State
		if state == "" {
			state = string(v1.RelayStateHealthy)
		}
		peers = append(peers, PeerEntry{
			ID:       inst.ID,
			WgIP:     inst.WgIP,
			Provider: inst.Provider,
			State:    state,
		})
	}

	// Sync the router peers ConfigMap
	if err := syncPeerConfigMap(ctx, r.Client, r.Namespace, relay, peers); err != nil {
		logger.Error(err, "failed to sync peers ConfigMap")
	}

	// Update status
	relay.Status.Instances = instances
	relay.Status.HealthyReplicas = healthyReplicas

	// Set conditions based on fleet health
	if healthyReplicas == len(instances) && len(instances) > 0 {
		common.SetCondition(&relay.Status.Conditions, string(v1.InferenceRelayConditionReady),
			metav1.ConditionTrue, "AllRelaysHealthy", fmt.Sprintf("%d/%d relays healthy", healthyReplicas, len(instances)))
		common.SetCondition(&relay.Status.Conditions, string(v1.InferenceRelayConditionDegraded),
			metav1.ConditionFalse, "FleetHealthy", "")
	} else if healthyReplicas == 0 && len(instances) > 0 {
		common.SetCondition(&relay.Status.Conditions, string(v1.InferenceRelayConditionReady),
			metav1.ConditionFalse, "NoHealthyRelays", "0 relays healthy")
		common.SetCondition(&relay.Status.Conditions, string(v1.InferenceRelayConditionDegraded),
			metav1.ConditionTrue, "AllRelaysUnhealthy", "")
	} else {
		common.SetCondition(&relay.Status.Conditions, string(v1.InferenceRelayConditionReady),
			metav1.ConditionFalse, "PartialFleet", fmt.Sprintf("%d/%d relays healthy", healthyReplicas, len(instances)))
		common.SetCondition(&relay.Status.Conditions, string(v1.InferenceRelayConditionDegraded),
			metav1.ConditionTrue, "PartialOutage", "")
	}

	// Fallback condition
	if healthReport != nil && healthReport.FallbackActive {
		common.SetCondition(&relay.Status.Conditions, string(v1.InferenceRelayConditionFallbackActive),
			metav1.ConditionTrue, "AllRelaysDown", "Router is in fallback mode")
	} else {
		common.SetCondition(&relay.Status.Conditions, string(v1.InferenceRelayConditionFallbackActive),
			metav1.ConditionFalse, "Normal", "")
	}

	if err := r.Status().Update(ctx, relay); err != nil {
		return ctrl.Result{}, err
	}

	// Determine requeue interval
	if needsRequeue {
		return ctrl.Result{RequeueAfter: requeueProvisioning}, nil
	}
	if healthyReplicas < len(instances) {
		return ctrl.Result{RequeueAfter: requeueDegraded}, nil
	}
	return ctrl.Result{RequeueAfter: requeueHealthy}, nil
}

// provisionRelay creates a new relay VM for the given provider.
func (r *InferenceRelayReconciler) provisionRelay(ctx context.Context, relay *v1.InferenceRelay, providerSpec v1.RelayProviderSpec) (*ProvisionResult, error) {
	driver, ok := r.Drivers[providerSpec.Provider]
	if !ok {
		return nil, fmt.Errorf("%w: no driver for provider %s", ErrConfig, providerSpec.Provider)
	}

	// Read provider credentials from Secret
	secret := &corev1.Secret{}
	secretName := types.NamespacedName{Name: providerSpec.CredentialsRef.Name, Namespace: r.Namespace}
	if err := r.Get(ctx, secretName, secret); err != nil {
		return nil, fmt.Errorf("get credentials secret %s: %w", providerSpec.CredentialsRef.Name, err)
	}

	// Generate WireGuard keypair for this relay VM
	kp, err := GenerateKeypair()
	if err != nil {
		return nil, fmt.Errorf("generate WG keypair: %w", err)
	}

	// Read router's WG public key from the router WG secret
	routerSecret := &corev1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{Name: routerWGSecret, Namespace: r.Namespace}, routerSecret); err != nil {
		return nil, fmt.Errorf("get router WG secret: %w", err)
	}
	routerPubKey := string(routerSecret.Data["publicKey"])
	if routerPubKey == "" {
		// Auto-generate if missing (first relay)
		routerKP, err := GenerateKeypair()
		if err != nil {
			return nil, fmt.Errorf("generate router keypair: %w", err)
		}
		routerPubKey = routerKP.PublicKeyB64
		routerSecret.Data = map[string][]byte{
			"publicKey":  []byte(routerPubKey),
			"privateKey": []byte(routerKP.PrivateKeyB64),
		}
		if err := r.Update(ctx, routerSecret); err != nil {
			return nil, fmt.Errorf("save router keypair: %w", err)
		}
	}

	// Render WG config for the relay VM
	wgConf, err := RenderRelayConfig(RelayWGConfig{
		PrivateKey:      kp.PrivateKeyB64,
		WgIP:            wgIPForProvider(providerSpec.Provider),
		RouterPublicKey: routerPubKey,
		RouterEndpoint:  relay.Spec.WireGuard.RouterEndpoint,
	})
	if err != nil {
		return nil, fmt.Errorf("render WG config: %w", err)
	}

	// Render cloud-init
	cloudInit, err := RenderCloudInit(CloudInitConfig{
		WgConfig:      wgConf,
		UpstreamURL:   relay.Spec.UpstreamURL,
		RouterEndpoint: relay.Spec.WireGuard.RouterEndpoint,
	})
	if err != nil {
		return nil, fmt.Errorf("render cloud-init: %w", err)
	}

	shape := providerSpec.Shape
	if shape == "" {
		shape = defaultShapeForProvider(providerSpec.Provider)
	}

	// Call the provider driver
	result, err := driver.Provision(ctx, ProvisionRequest{
		Name:      fmt.Sprintf("relay-%s", providerSpec.Provider),
		Region:    providerSpec.Region,
		Shape:     shape,
		CloudInit: cloudInit,
		WireGuardIP: wgIPForProvider(providerSpec.Provider),
	})
	if err != nil {
		return nil, fmtError("provision", providerSpec.Provider, err)
	}

	return result, nil
}

// handleRotation destroys the specified relay VM and marks it for reprovisioning.
func (r *InferenceRelayReconciler) handleRotation(ctx context.Context, relay *v1.InferenceRelay, instanceID string) error {
	logger := log.FromContext(ctx)

	// Find the instance
	var target *v1.RelayInstanceStatus
	for i := range relay.Status.Instances {
		if relay.Status.Instances[i].ID == instanceID {
			target = &relay.Status.Instances[i]
			break
		}
	}
	if target == nil {
		logger.Info("rotation target not found in fleet", "instanceID", instanceID)
		return nil
	}

	driver, ok := r.Drivers[target.Provider]
	if !ok {
		return fmt.Errorf("no driver for provider %s", target.Provider)
	}

	// Destroy the VM
	if err := driver.Destroy(ctx, target.ID, target.Region); err != nil {
		return fmtError("destroy during rotation", target.Provider, err)
	}

	// Remove the instance from status — next reconcile will provision a replacement
	updated := relay.Status.Instances[:0]
	for _, inst := range relay.Status.Instances {
		if inst.ID != instanceID {
			updated = append(updated, inst)
		}
	}
	relay.Status.Instances = updated
	relay.Status.HealthyReplicas = 0
	for _, inst := range updated {
		if inst.Healthy {
			relay.Status.HealthyReplicas++
		}
	}
	now := metav1.NewTime(time.Now())
	relay.Status.LastRotation = &now

	common.SetCondition(&relay.Status.Conditions, string(v1.InferenceRelayConditionRotating),
		metav1.ConditionTrue, "ManualRotation", fmt.Sprintf("Rotating %s", instanceID))

	return r.Status().Update(ctx, relay)
}

// handleDeletion destroys all relay VMs before removing the finalizer.
func (r *InferenceRelayReconciler) handleDeletion(ctx context.Context, relay *v1.InferenceRelay) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	if !controllerContainsFinalizer(relay, InferenceRelayFinalizer) {
		return ctrl.Result{}, nil
	}

	// Destroy all instances
	for _, inst := range relay.Status.Instances {
		driver, ok := r.Drivers[inst.Provider]
		if !ok {
			continue
		}
		if err := driver.Destroy(ctx, inst.ID, inst.Region); err != nil {
			logger.Error(err, "failed to destroy relay during deletion", "instanceID", inst.ID, "provider", inst.Provider)
			return ctrl.Result{RequeueAfter: requeueError}, err
		}
	}

	// Remove finalizer
	common.RemoveFinalizer(relay, InferenceRelayFinalizer)
	if err := r.Update(ctx, relay); err != nil {
		return ctrl.Result{}, err
	}

	logger.Info("InferenceRelay deleted — all relay VMs destroyed")
	return ctrl.Result{}, nil
}

// SetupWithManager registers the reconciler with the controller-runtime manager.
func (r *InferenceRelayReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1.InferenceRelay{}).
		Owns(&corev1.ConfigMap{}).
		Owns(&corev1.Secret{}).
		Complete(r)
}

// controllerContainsFinalizer is a helper to avoid importing controllerutil directly.
func controllerContainsFinalizer(obj client.Object, finalizer string) bool {
	annotations := obj.GetAnnotations()
	_ = annotations
	f := obj.GetFinalizers()
	for _, f2 := range f {
		if f2 == finalizer {
			return true
		}
	}
	return false
}
