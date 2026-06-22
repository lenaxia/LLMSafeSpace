// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package relay

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/hex"
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

// transitionInstanceState computes the new state for an instance after the
// router reports its health. The transition rules (worklog 0467 fix):
//
//   - Terminal/explicit states (draining, terminated, quota-exhausted,
//     provisioning-failed) are preserved unchanged. Only the routine
//     provisioning ↔ healthy ↔ unhealthy axis transitions automatically.
//   - During initial boot, a not-yet-healthy provisioning instance stays
//     provisioning rather than flipping to unhealthy. This avoids alerting
//     on a relay that hasn't had a chance to come up yet.
//   - A previously-healthy instance that goes unhealthy flips to unhealthy.
//   - Any unhealthy or provisioning instance that becomes healthy flips
//     to healthy (recovery path).
func transitionInstanceState(currentState string, healthy bool) string {
	switch currentState {
	case "", string(v1.RelayStateProvisioning):
		if healthy {
			return string(v1.RelayStateHealthy)
		}
		return currentState // stay provisioning during boot grace
	case string(v1.RelayStateHealthy):
		if healthy {
			return currentState
		}
		return string(v1.RelayStateUnhealthy)
	case string(v1.RelayStateUnhealthy):
		if healthy {
			return string(v1.RelayStateHealthy)
		}
		return currentState
	}
	// All other states (draining, terminated, quota-exhausted,
	// provisioning-failed) are controller-driven and preserved as-is.
	return currentState
}

// generateRelayToken returns a fresh per-VM shared-secret token (32 random
// bytes, hex-encoded → 64 chars). Used by provisionRelay when no existing token
// is persisted for the provider slot.
func generateRelayToken() (string, error) {
	b := make([]byte, relayTokenBytes)
	if _, err := cryptorand.Read(b); err != nil {
		return "", fmt.Errorf("read random bytes: %w", err)
	}
	return hex.EncodeToString(b), nil
}

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

	// ArtifactURLs are the base mirror URLs the controller embeds into each
	// relay VM's cloud-init so it can download the relay-proxy binary. Set
	// via the --relay-artifact-url controller flag (comma-separated), sourced
	// from the operator's Helm values. The cloud-init appends "/<binary>"
	// (arch-resolved) and tries each mirror in order.
	ArtifactURLs []string

	// ArtifactSHA256Arm64 is the hex SHA-256 of the arm64 relay-proxy binary.
	// Required when provisioning any arm64 shape (AWS t4g, OCI A1).
	ArtifactSHA256Arm64 string

	// ArtifactSHA256Amd64 is the hex SHA-256 of the amd64 relay-proxy binary.
	// Required when provisioning any amd64 shape (GCP e2, AWS t3).
	ArtifactSHA256Amd64 string
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

	// Adopt pre-pass: for any spec provider that doesn't have a status
	// entry, look for an existing cloud VM tagged with this CR's UID +
	// provider. If found, adopt it instead of provisioning a new one.
	//
	// This closes the worklog 0473/0474 leak: if a previous reconcile
	// successfully provisioned a VM but failed to persist Status (e.g.
	// optimistic-concurrency conflict on r.Status().Update), the VM is
	// alive in the cloud but unrecorded in the CR. Without adoption, the
	// next reconcile would call Provision again, creating a duplicate
	// and orphaning the original. With adoption, the original is found
	// by tag and re-attached.
	//
	// Also collect duplicates (tagged instances whose ID doesn't match
	// the adopted/existing one) for cleanup. These are previous orphans
	// that now-stranded.
	extraInstancesToDestroy := r.adoptOrphanedInstances(ctx, relay, existingByProvider)

	// Read relay per-VM tokens from persistent Secret
	relayTokens := r.readRelayTokens(ctx)

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
					existing.State = transitionInstanceState(existing.State, h.Healthy)
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
		result, token, err := r.provisionRelay(ctx, relay, providerSpec, relayTokens[provider])
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

		// Persist the per-VM token (so a controller restart uses the same token
		// the running VM was cloud-init'd with; a fresh token would 401 at the
		// router until the VM is destroyed + recreated).
		relayTokens[provider] = token
		if err := r.writeRelayTokens(ctx, relayTokens); err != nil {
			logger.Error(err, "failed to persist relay token")
		}

		instances = append(instances, v1.RelayInstanceStatus{
			ID:       result.InstanceID,
			Provider: provider,
			Region:   providerSpec.Region,
			PublicIP: result.PublicIP,
			State:    string(v1.RelayStateProvisioning),
			Healthy:  false,
		})
		needsRequeue = true
	}

	// Destroy any extra tagged-but-unaccounted instances surfaced by
	// the adopt pre-pass (worklog 0474). These are duplicates from
	// prior conflict-induced re-provisions.
	for _, dup := range extraInstancesToDestroy {
		logger.Info("destroying duplicate relay VM (tag-based orphan cleanup)",
			"provider", dup.Provider, "instanceID", dup.InstanceID, "region", dup.Region)
		if driver, ok := r.Drivers[dup.Provider]; ok {
			if err := driver.Destroy(ctx, dup.InstanceID, dup.Region); err != nil {
				logger.Error(err, "failed to destroy duplicate relay",
					"instanceID", dup.InstanceID, "provider", dup.Provider)
			}
		}
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

	// Build peer entries for ConfigMap (include per-VM tokens)
	peers := make([]PeerEntry, 0, len(instances))
	for _, inst := range instances {
		state := inst.State
		if state == "" {
			state = string(v1.RelayStateHealthy)
		}
		peers = append(peers, PeerEntry{
			ID:       inst.ID,
			Endpoint: endpointForInstance(inst),
			Provider: inst.Provider,
			State:    state,
			Token:    relayTokens[inst.Provider],
		})
	}

	// Sync the router peers ConfigMap
	if err := syncPeerConfigMap(ctx, r.Client, r.Namespace, peers); err != nil {
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

// orphanInstance describes a tagged cloud VM that does not belong to the
// current Status.Instances slot. The reconciler destroys these after the
// main provisioning loop. See worklog 0474.
type orphanInstance struct {
	Provider   string
	Region     string
	InstanceID string
}

// adoptOrphanedInstances lists cloud VMs tagged with this CR's UID and
// reconciles them against existingByProvider. For each spec provider that
// is missing from Status, if a tagged running/pending VM exists, adopt it
// (insert a synthesized RelayInstanceStatus into existingByProvider so
// the main loop sees it as already-provisioned). Any tagged VMs that
// don't match an active status entry are returned for destruction.
//
// This closes the worklog 0473/0474 leak: a Status update conflict during
// provisioning leaves an EC2 alive but unrecorded; without adoption, the
// next reconcile creates a duplicate and the original is forever orphaned.
//
// Failures here are logged but do not abort reconcile — adoption is
// best-effort. The next reconcile cycle will retry.
func (r *InferenceRelayReconciler) adoptOrphanedInstances(
	ctx context.Context,
	relay *v1.InferenceRelay,
	existingByProvider map[string]*v1.RelayInstanceStatus,
) []orphanInstance {
	logger := log.FromContext(ctx)
	uid := string(relay.UID)
	if uid == "" {
		// No UID means we can't safely identify ownership. Skip adoption.
		return nil
	}

	// Build set of instance IDs we already know about (and shouldn't try
	// to destroy as duplicates).
	knownIDs := make(map[string]bool)
	for _, inst := range relay.Status.Instances {
		if inst.ID != "" {
			knownIDs[inst.ID] = true
		}
	}

	var extras []orphanInstance

	// For each provider in the spec, list driver instances and look for
	// a matching tagged VM. Drivers list per region; we use the spec
	// region for adoption queries (same region we'd provision into).
	for _, providerSpec := range relay.Spec.Providers {
		driver, ok := r.Drivers[providerSpec.Provider]
		if !ok {
			continue
		}
		listed, err := driver.ListInstances(ctx, providerSpec.Region)
		if err != nil {
			logger.V(1).Info("adoption: ListInstances failed (continuing)",
				"provider", providerSpec.Provider, "region", providerSpec.Region, "error", err.Error())
			continue
		}

		// Find candidates: tagged with this UID + provider, in
		// running or pending state.
		var candidates []VMInstance
		for _, vm := range listed {
			if vm.OwnerUID != uid {
				continue
			}
			if vm.Provider != "" && vm.Provider != providerSpec.Provider {
				continue
			}
			if vm.State != VMStateRunning && vm.State != VMStatePending {
				continue
			}
			candidates = append(candidates, vm)
		}

		if existing, alreadyKnown := existingByProvider[providerSpec.Provider]; alreadyKnown {
			// We already have a Status entry for this provider. Don't
			// adopt new candidates — but DO refresh PublicIP from the
			// cloud listing if the Status entry is missing it. This
			// handles the case where an earlier adoption (or a fresh
			// Provision whose post-Provision Status update was lost)
			// captured an empty IP because the instance was still
			// pending. Without this refresh, the IP would stay empty
			// forever and the router could never reach the relay.
			if existing.PublicIP == "" {
				for _, vm := range candidates {
					if vm.InstanceID == existing.ID && vm.PublicIP != "" {
						existing.PublicIP = vm.PublicIP
						logger.Info("refreshed PublicIP for adopted relay (was empty in Status)",
							"provider", providerSpec.Provider, "instanceID", existing.ID, "publicIP", vm.PublicIP)
						break
					}
				}
			}
		} else if len(candidates) > 0 {
			// Adopt the first candidate — synthesize a RelayInstanceStatus
			// so the main loop treats it as already-provisioned. Health
			// state will catch up on the next router scrape.
			adopted := candidates[0]
			now := metav1.Now()
			synthetic := &v1.RelayInstanceStatus{
				ID:        adopted.InstanceID,
				Provider:  providerSpec.Provider,
				Region:    providerSpec.Region,
				PublicIP:  adopted.PublicIP,
				State:     string(v1.RelayStateProvisioning),
				Healthy:   false,
				LastCheck: &now,
			}
			existingByProvider[providerSpec.Provider] = synthetic
			knownIDs[adopted.InstanceID] = true
			logger.Info("adopted orphaned relay VM (Status update was lost previously)",
				"provider", providerSpec.Provider, "region", providerSpec.Region, "instanceID", adopted.InstanceID)
			candidates = candidates[1:]
		}

		// Any remaining candidates are duplicates — mark for destroy.
		for _, dup := range candidates {
			if knownIDs[dup.InstanceID] {
				continue
			}
			extras = append(extras, orphanInstance{
				Provider:   providerSpec.Provider,
				Region:     providerSpec.Region,
				InstanceID: dup.InstanceID,
			})
		}
	}
	return extras
}

// provisionRelay creates a new relay VM for the given provider.
// Returns the provision result, the per-VM token (read from existingToken or
// freshly generated), and error.
func (r *InferenceRelayReconciler) provisionRelay(ctx context.Context, relay *v1.InferenceRelay, providerSpec v1.RelayProviderSpec, existingToken string) (*ProvisionResult, string, error) {
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

	// Token: reuse the existing per-VM token if present (so a controller
	// restart doesn't desync from a running VM), otherwise generate a fresh
	// one for this new VM. Per-VM scope means a compromised VM's token cannot
	// be used against sibling relays.
	token := existingToken
	if token == "" {
		var err error
		token, err = generateRelayToken()
		if err != nil {
			return nil, "", fmt.Errorf("generate relay token: %w", err)
		}
	}

	// Render cloud-init
	shape := providerSpec.Shape
	if shape == "" {
		shape = defaultShapeForProvider(providerSpec.Provider)
	}
	arch := archForShape(shape, providerSpec.Provider)
	artifactSHA := r.ArtifactSHA256Arm64
	if arch == "amd64" {
		artifactSHA = r.ArtifactSHA256Amd64
	}
	if artifactSHA == "" {
		return nil, "", fmt.Errorf("%w: relay artifact SHA-256 for arch %s is not set on the controller — set --relay-artifact-sha256-%s (the cloud-init cannot verify the binary download without it)", ErrConfig, arch, arch)
	}

	cloudInit, err := RenderCloudInit(CloudInitConfig{
		UpstreamURL:    relay.Spec.UpstreamURL,
		Token:          token,
		ArtifactURLs:   r.ArtifactURLs,
		ArtifactSHA256: artifactSHA,
		BinaryName:     binaryNameForArch(arch),
	})
	if err != nil {
		return nil, "", fmt.Errorf("render cloud-init: %w", err)
	}

	// Call the provider driver. Pass OwnerUID + Provider so the driver
	// tags the VM. If the post-Provision Status update is lost, the next
	// reconcile pre-pass adopts the VM by tag instead of creating a
	// duplicate (worklog 0473/0474 leak fix).
	start := time.Now()
	result, err := driver.Provision(ctx, ProvisionRequest{
		Name:      fmt.Sprintf("relay-%s", providerSpec.Provider),
		Region:    providerSpec.Region,
		Shape:     shape,
		CloudInit: cloudInit,
		OwnerUID:  string(relay.UID),
		Provider:  providerSpec.Provider,
	})
	if err != nil {
		return nil, "", fmtError("provision", providerSpec.Provider, err)
	}

	observeProvisionDuration(providerSpec.Provider, time.Since(start).Seconds())
	return result, token, nil
}

// readRelayTokens reads the per-VM relay tokens from the persistent Secret.
// Keyed by provider name (one VM per provider slot).
func (r *InferenceRelayReconciler) readRelayTokens(ctx context.Context) map[string]string {
	tokens := make(map[string]string)
	secret := &corev1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{Name: relayTokensSecret, Namespace: r.Namespace}, secret); err != nil {
		return tokens
	}
	for provider, data := range secret.Data {
		if len(data) > 0 {
			tokens[provider] = string(data)
		}
	}
	return tokens
}

// writeRelayTokens persists per-VM relay tokens to a Secret.
func (r *InferenceRelayReconciler) writeRelayTokens(ctx context.Context, tokens map[string]string) error {
	secret := &corev1.Secret{}
	err := r.Get(ctx, types.NamespacedName{Name: relayTokensSecret, Namespace: r.Namespace}, secret)
	data := make(map[string][]byte, len(tokens))
	for provider, token := range tokens {
		data[provider] = []byte(token)
	}

	if err == nil {
		secret.Data = data
		return r.Update(ctx, secret)
	}
	secret.ObjectMeta = metav1.ObjectMeta{Name: relayTokensSecret, Namespace: r.Namespace}
	secret.Data = data
	return r.Create(ctx, secret)
}

// endpointForInstance returns the dialable endpoint (host:port) for a relay
// instance status. The router dials http://<endpoint><path>. The default port
// 8080 matches the relay-proxy's --listen default.
func endpointForInstance(inst v1.RelayInstanceStatus) string {
	if inst.PublicIP == "" {
		return ""
	}
	return inst.PublicIP + ":8080"
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

	// Tag-based sweep: list any cloud VMs tagged with this CR's UID
	// across the spec providers/regions and destroy them. Catches
	// orphans from prior reconciles whose Status update was lost
	// (worklog 0473/0474). Best-effort — failures don't block deletion.
	if uid := string(relay.UID); uid != "" {
		// Skip instance IDs that the Status.Instances loop above already
		// processed (regardless of destroy success/failure) — the cloud
		// may briefly still report them as running due to eventual
		// consistency, and we don't want to double-Destroy them.
		alreadyProcessed := make(map[string]bool, len(relay.Status.Instances))
		for _, inst := range relay.Status.Instances {
			if inst.ID != "" {
				alreadyProcessed[inst.ID] = true
			}
		}

		seenRegions := make(map[string]map[string]bool)
		for _, ps := range relay.Spec.Providers {
			driver, ok := r.Drivers[ps.Provider]
			if !ok {
				continue
			}
			if seenRegions[ps.Provider] == nil {
				seenRegions[ps.Provider] = make(map[string]bool)
			}
			if seenRegions[ps.Provider][ps.Region] {
				continue
			}
			seenRegions[ps.Provider][ps.Region] = true

			listed, err := driver.ListInstances(ctx, ps.Region)
			if err != nil {
				logger.V(1).Info("deletion sweep: ListInstances failed (continuing)",
					"provider", ps.Provider, "region", ps.Region, "error", err.Error())
				continue
			}
			for _, vm := range listed {
				if vm.OwnerUID != uid {
					continue
				}
				if alreadyProcessed[vm.InstanceID] {
					continue
				}
				if vm.State != VMStateRunning && vm.State != VMStatePending {
					continue
				}
				logger.Info("deletion sweep: destroying tagged VM not in Status",
					"provider", ps.Provider, "region", ps.Region, "instanceID", vm.InstanceID)
				if err := driver.Destroy(ctx, vm.InstanceID, ps.Region); err != nil {
					logger.Error(err, "deletion sweep: destroy failed",
						"instanceID", vm.InstanceID, "provider", ps.Provider)
					allDestroyed = false
				}
			}
		}
	}

	if !allDestroyed {
		return ctrl.Result{RequeueAfter: requeueError}, fmt.Errorf("some relay VMs could not be destroyed")
	}

	// Clear the peer ConfigMap so the relay-router observes the empty
	// fleet. The CM has no ownerReference (see syncPeerConfigMap doc
	// comment for why) so it persists across CR deletions; the empty-list
	// write here propagates cleanly through kubelet's volume-mount sync to
	// the router pod. See worklog 0468 for the discovery that motivated
	// removing the ownerRef.
	if err := syncPeerConfigMap(ctx, r.Client, r.Namespace, []PeerEntry{}); err != nil {
		logger.Error(err, "failed to clear peer ConfigMap during deletion — relay-router may retain orphans until restart")
		// Do not block deletion on this — terminating EC2 instances and
		// removing the finalizer is more important than the cosmetic
		// router-cache cleanup.
	}

	common.RemoveFinalizer(relay, InferenceRelayFinalizer)
	if err := r.Update(ctx, relay); err != nil {
		return ctrl.Result{}, err
	}

	logger.Info("InferenceRelay deleted — all relay VMs destroyed")
	return ctrl.Result{}, nil
}

// SetupWithManager registers the reconciler with the controller-runtime manager.
//
// Note: the relay-router-peers ConfigMap is intentionally NOT registered via
// Owns() because it has no ownerReference (see syncPeerConfigMap doc comment
// for why). The CM is managed by the controller's reconcile loop alone;
// external edits would not trigger a reconcile, which is acceptable since
// the controller is the only legitimate writer.
func (r *InferenceRelayReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1.InferenceRelay{}).
		Owns(&corev1.Secret{}).
		Complete(r)
}
