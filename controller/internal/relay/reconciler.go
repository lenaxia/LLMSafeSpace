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
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/lenaxia/llmsafespaces/controller/internal/common"
	"github.com/lenaxia/llmsafespaces/controller/internal/metrics"
	v1 "github.com/lenaxia/llmsafespaces/pkg/apis/llmsafespaces/v1"
)

// relayWGKeysSecret is where relay WG public keys are persisted across restarts.
const relayWGKeysSecret = "relay-wg-keys"

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

	// ExpectedCredentialSecrets maps provider name → the K8s Secret name
	// the driver reads credentials from. The reconciler validates that
	// spec.providers[].credentialsRef.Name matches this value.
	ExpectedCredentialSecrets map[string]string
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
		// Clear the rotation annotation — re-fetch to avoid resourceVersion conflict
		relay.Annotations[annotationRotate] = ""
		if err := r.Update(ctx, relay); err != nil {
			if apierrors.IsConflict(err) {
				return ctrl.Result{Requeue: true}, nil
			}
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: requeueProvisioning}, nil
	}

	// Main provisioning/health loop
	return r.reconcileFleet(ctx, relay)
}

// reconcileFleet provisions missing VMs, checks health, destroys orphaned
// instances, and updates status.
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

	// Build a set of spec providers for orphan detection
	specProviders := make(map[string]bool)
	for _, p := range relay.Spec.Providers {
		specProviders[p.Provider] = true
	}

	// Destroy orphaned instances (provider removed from spec but VM still running)
	for i := range relay.Status.Instances {
		inst := &relay.Status.Instances[i]
		if !specProviders[inst.Provider] {
			logger.Info("destroying orphaned relay", "provider", inst.Provider, "instanceID", inst.ID)
			if driver, ok := r.Drivers[inst.Provider]; ok {
				if err := driver.Destroy(ctx, inst.ID, inst.Region); err != nil && !apierrors.IsNotFound(err) {
					logger.Error(err, "failed to destroy orphaned relay", "instanceID", inst.ID)
				}
			}
		}
	}

	// Track existing instances by provider (only healthy/provisioning ones)
	// provisioning-failed instances are re-provisioned each cycle
	existingByProvider := make(map[string]*v1.RelayInstanceStatus)
	for i := range relay.Status.Instances {
		inst := &relay.Status.Instances[i]
		if specProviders[inst.Provider] && inst.State != string(v1.RelayStateProvisioningFailed) {
			existingByProvider[inst.Provider] = inst
		}
	}

	// Read relay WG public keys from persistent Secret
	relayPubKeys := r.readRelayWGKeys(ctx)

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
		result, pubKey, err := r.provisionRelay(ctx, relay, providerSpec)
		if err != nil {
			logger.Error(err, "provisioning failed", "provider", provider)
			if IsConfigError(err) {
				// Accumulate provisioning attempts from previous status
				attempts := 1
				for _, old := range relay.Status.Instances {
					if old.Provider == provider {
						attempts = old.ProvisioningAttempts + 1
						break
					}
				}
				failedState := string(v1.RelayStateProvisioningFailed)
				if attempts >= maxProvisioningAttempts {
					failedState = string(v1.RelayStateProvisioningFailed)
					common.SetCondition(&relay.Status.Conditions, string(v1.InferenceRelayConditionProvisioningFailed),
						metav1.ConditionTrue, "CircuitBreakerTripped", fmt.Sprintf("provider %s failed %d times: %s", provider, attempts, err.Error()))
				}
				instances = append(instances, v1.RelayInstanceStatus{
					ID:                   fmt.Sprintf("%s-provisioning", provider),
					Provider:             provider,
					Region:               providerSpec.Region,
					State:                failedState,
					Healthy:              false,
					ProvisioningAttempts: attempts,
					LastProvisionError:   err.Error(),
				})
			}
			needsRequeue = true
			continue
		}

		// Persist relay WG public key
		relayPubKeys[provider] = pubKey
		if err := r.writeRelayWGKeys(ctx, relayPubKeys); err != nil {
			logger.Error(err, "failed to persist relay WG key")
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

	metrics.RelayProvisioningFailed.Reset()
	metrics.RelayDraining.Reset()
	metrics.RelayQuotaExhausted.Reset()
	setRelayHealthyReplicas(healthyReplicas)
	for _, inst := range instances {
		setRelayProvisioningFailed(inst.Provider, inst.State == string(v1.RelayStateProvisioningFailed))
		setRelayDraining(inst.Provider, inst.State == string(v1.RelayStateDraining))
		setRelayQuotaExhausted(inst.Provider, inst.State == string(v1.RelayStateQuotaExhausted))
	}

	// Build peer entries for ConfigMap (include WG public keys)
	peers := make([]PeerEntry, 0, len(instances))
	for _, inst := range instances {
		state := inst.State
		if state == "" {
			state = string(v1.RelayStateHealthy)
		}
		peers = append(peers, PeerEntry{
			ID:        inst.ID,
			WgIP:      inst.WgIP,
			Provider:  inst.Provider,
			State:     state,
			PublicKey: relayPubKeys[inst.Provider],
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
	r.updateConditions(relay, healthReport, healthyReplicas, len(instances))

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

// updateConditions sets Ready, Degraded, and FallbackActive conditions.
func (r *InferenceRelayReconciler) updateConditions(relay *v1.InferenceRelay, healthReport *HealthReport, healthy, total int) {
	if total == 0 {
		common.SetCondition(&relay.Status.Conditions, string(v1.InferenceRelayConditionReady),
			metav1.ConditionFalse, "NoInstances", "No relay instances provisioned")
		common.SetCondition(&relay.Status.Conditions, string(v1.InferenceRelayConditionDegraded),
			metav1.ConditionTrue, "Empty", "No relay instances")
	} else if healthy == total {
		common.SetCondition(&relay.Status.Conditions, string(v1.InferenceRelayConditionReady),
			metav1.ConditionTrue, "AllRelaysHealthy", fmt.Sprintf("%d/%d relays healthy", healthy, total))
		common.SetCondition(&relay.Status.Conditions, string(v1.InferenceRelayConditionDegraded),
			metav1.ConditionFalse, "FleetHealthy", "")
	} else if healthy == 0 {
		common.SetCondition(&relay.Status.Conditions, string(v1.InferenceRelayConditionReady),
			metav1.ConditionFalse, "NoHealthyRelays", "0 relays healthy")
		common.SetCondition(&relay.Status.Conditions, string(v1.InferenceRelayConditionDegraded),
			metav1.ConditionTrue, "AllRelaysUnhealthy", "")
	} else {
		common.SetCondition(&relay.Status.Conditions, string(v1.InferenceRelayConditionReady),
			metav1.ConditionFalse, "PartialFleet", fmt.Sprintf("%d/%d relays healthy", healthy, total))
		common.SetCondition(&relay.Status.Conditions, string(v1.InferenceRelayConditionDegraded),
			metav1.ConditionTrue, "PartialOutage", "")
	}

	if healthReport != nil && healthReport.FallbackActive {
		common.SetCondition(&relay.Status.Conditions, string(v1.InferenceRelayConditionFallbackActive),
			metav1.ConditionTrue, "AllRelaysDown", "Router is in fallback mode")
	} else {
		common.SetCondition(&relay.Status.Conditions, string(v1.InferenceRelayConditionFallbackActive),
			metav1.ConditionFalse, "Normal", "")
	}
}

// provisionRelay creates a new relay VM for the given provider.
// Returns the provision result, WG public key, and error.
func (r *InferenceRelayReconciler) provisionRelay(ctx context.Context, relay *v1.InferenceRelay, providerSpec v1.RelayProviderSpec) (*ProvisionResult, string, error) {
	driver, ok := r.Drivers[providerSpec.Provider]
	if !ok {
		return nil, "", fmt.Errorf("%w: no driver for provider %s", ErrConfig, providerSpec.Provider)
	}

	// Validate that the CRD's credentialsRef.Name matches the driver's
	// expected credential secret. This prevents a schema mismatch where
	// the CRD allows arbitrary names but the driver only reads one.
	if expected, exists := r.ExpectedCredentialSecrets[providerSpec.Provider]; exists && expected != "" {
		if providerSpec.CredentialsRef.Name != expected {
			return nil, "", fmt.Errorf("%w: credentialsRef.Name %q does not match expected %q for provider %s",
				ErrConfig, providerSpec.CredentialsRef.Name, expected, providerSpec.Provider)
		}
	}

	// Generate WireGuard keypair for this relay VM
	kp, err := GenerateKeypair()
	if err != nil {
		return nil, "", fmt.Errorf("generate WG keypair: %w", err)
	}

	// Read or generate router's WG public key
	routerPubKey, err := r.ensureRouterWGKey(ctx, relay)
	if err != nil {
		return nil, "", fmt.Errorf("ensure router WG key: %w", err)
	}

	// Render WG config for the relay VM
	wgConf, err := RenderRelayConfig(RelayWGConfig{
		PrivateKey:      kp.PrivateKeyB64,
		WgIP:            wgIPForProvider(providerSpec.Provider),
		RouterPublicKey: routerPubKey,
		RouterEndpoint:  relay.Spec.WireGuard.RouterEndpoint,
	})
	if err != nil {
		return nil, "", fmt.Errorf("render WG config: %w", err)
	}

	// Render cloud-init
	cloudInit, err := RenderCloudInit(CloudInitConfig{
		WgConfig:       wgConf,
		UpstreamURL:    relay.Spec.UpstreamURL,
		RouterEndpoint: relay.Spec.WireGuard.RouterEndpoint,
	})
	if err != nil {
		return nil, "", fmt.Errorf("render cloud-init: %w", err)
	}

	shape := providerSpec.Shape
	if shape == "" {
		shape = defaultShapeForProvider(providerSpec.Provider)
	}

	// Call the provider driver
	start := time.Now()
	result, err := driver.Provision(ctx, ProvisionRequest{
		Name:        fmt.Sprintf("relay-%s", providerSpec.Provider),
		Region:      providerSpec.Region,
		Shape:       shape,
		CloudInit:   cloudInit,
		WireGuardIP: wgIPForProvider(providerSpec.Provider),
	})
	if err != nil {
		return nil, "", fmtError("provision", providerSpec.Provider, err)
	}

	observeProvisionDuration(providerSpec.Provider, time.Since(start).Seconds())
	return result, kp.PublicKeyB64, nil
}

// ensureRouterWGKey reads or generates the router's WG keypair from the
// secret referenced in spec.wireGuard.routerPrivateKeyRef (or default).
// Returns the public key and an error if the key could not be persisted.
func (r *InferenceRelayReconciler) ensureRouterWGKey(ctx context.Context, relay *v1.InferenceRelay) (string, error) {
	secretName := routerWGSecret
	if relay.Spec.WireGuard.RouterPrivateKeyRef != "" {
		secretName = relay.Spec.WireGuard.RouterPrivateKeyRef
	}
	routerSecret := &corev1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{Name: secretName, Namespace: r.Namespace}, routerSecret); err == nil {
		if pub := string(routerSecret.Data["publicKey"]); pub != "" {
			return pub, nil
		}
	}

	// Auto-generate if missing
	kp, err := GenerateKeypair()
	if err != nil {
		return "", fmt.Errorf("generate router WG keypair: %w", err)
	}

	routerSecret.ObjectMeta = metav1.ObjectMeta{Name: secretName, Namespace: r.Namespace}
	routerSecret.Data = map[string][]byte{
		"publicKey":  []byte(kp.PublicKeyB64),
		"privateKey": []byte(kp.PrivateKeyB64),
	}

	if err := r.Create(ctx, routerSecret); err != nil {
		if apierrors.IsAlreadyExists(err) {
			existing := &corev1.Secret{}
			if getErr := r.Get(ctx, types.NamespacedName{Name: secretName, Namespace: r.Namespace}, existing); getErr != nil {
				return "", fmt.Errorf("get existing router WG secret: %w", getErr)
			}
			existing.Data = routerSecret.Data
			if updateErr := r.Update(ctx, existing); updateErr != nil {
				return "", fmt.Errorf("update router WG secret: %w", updateErr)
			}
		} else {
			return "", fmt.Errorf("create router WG secret: %w", err)
		}
	}

	return kp.PublicKeyB64, nil
}

// readRelayWGKeys reads the relay WG public keys from the persistent Secret.
func (r *InferenceRelayReconciler) readRelayWGKeys(ctx context.Context) map[string]string {
	keys := make(map[string]string)
	secret := &corev1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{Name: relayWGKeysSecret, Namespace: r.Namespace}, secret); err != nil {
		return keys
	}
	for provider, data := range secret.Data {
		if len(data) > 0 {
			keys[provider] = string(data)
		}
	}
	return keys
}

// writeRelayWGKeys persists relay WG public keys to a Secret.
func (r *InferenceRelayReconciler) writeRelayWGKeys(ctx context.Context, keys map[string]string) error {
	secret := &corev1.Secret{}
	err := r.Get(ctx, types.NamespacedName{Name: relayWGKeysSecret, Namespace: r.Namespace}, secret)
	data := make(map[string][]byte, len(keys))
	for provider, key := range keys {
		data[provider] = []byte(key)
	}

	if err == nil {
		secret.Data = data
		return r.Update(ctx, secret)
	}
	secret.ObjectMeta = metav1.ObjectMeta{Name: relayWGKeysSecret, Namespace: r.Namespace}
	secret.Data = data
	return r.Create(ctx, secret)
}

// handleRotation destroys the specified relay VM and marks it for reprovisioning.
func (r *InferenceRelayReconciler) handleRotation(ctx context.Context, relay *v1.InferenceRelay, instanceID string) error {
	logger := log.FromContext(ctx)

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

	if err := driver.Destroy(ctx, target.ID, target.Region); err != nil {
		return fmtError("destroy during rotation", target.Provider, err)
	}

	recordRotation(target.Provider, "manual")

	// Remove the instance from status
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

	if !controllerutil.ContainsFinalizer(relay, InferenceRelayFinalizer) {
		return ctrl.Result{}, nil
	}

	// Destroy all instances, tracking which are already gone
	allDestroyed := true
	anyDestroyed := false
	for i := range relay.Status.Instances {
		inst := &relay.Status.Instances[i]
		if inst.State == string(v1.RelayStateTerminated) {
			continue
		}
		driver, ok := r.Drivers[inst.Provider]
		if !ok {
			logger.Error(fmt.Errorf("no driver for %s", inst.Provider),
				"cannot destroy relay VM — manual cleanup required",
				"instanceID", inst.ID, "provider", inst.Provider)
			allDestroyed = false
			continue
		}
		if err := driver.Destroy(ctx, inst.ID, inst.Region); err != nil {
			logger.Error(err, "failed to destroy relay during deletion", "instanceID", inst.ID, "provider", inst.Provider)
			allDestroyed = false
		} else {
			inst.State = string(v1.RelayStateTerminated)
			anyDestroyed = true
		}
	}

	// Persist partial destruction progress so retry knows which are gone
	if anyDestroyed {
		if err := r.Status().Update(ctx, relay); err != nil {
			return ctrl.Result{}, err
		}
	}

	if !allDestroyed {
		return ctrl.Result{RequeueAfter: requeueError}, fmt.Errorf("some relay VMs could not be destroyed")
	}

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
