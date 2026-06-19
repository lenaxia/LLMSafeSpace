// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package settings

// Canonical Kubernetes Quantity regex patterns shared by:
//
//   - The settings schema (`pkg/settings/schema.go`) — validates
//     admin-typed values at save time.
//   - The validating webhook (`controller/internal/webhooks/workspace_webhook.go`)
//     — validates spec.resources.* on Workspace CRs at admission time.
//   - The CRD kubebuilder annotation
//     (`pkg/apis/llmsafespaces/v1/workspace_types.go`) — validates
//     spec.resources.* on Workspace CRs at apiserver level.
//
// Sourcing all three from one constant prevents the schema-vs-webhook
// drift that caused the original "8gi" production bug. Magnitude is
// constrained: `[1-9][0-9]*` rather than `[0-9]+` so zero-magnitude
// values (which the webhook's parseMemoryMi/storageSizeGi reject as
// `n < 1`) fail the schema check too. Same failure class as "8gi":
// admin saves it, workspace creation breaks.
//
// IMPORTANT: when changing any of these constants, update the
// corresponding kubebuilder annotation in
// pkg/apis/llmsafespaces/v1/workspace_types.go AND the regex var in
// controller/internal/webhooks/workspace_webhook.go in lockstep.
// TestWebhookRegexAcceptsSameInputsAsSettingsPattern (in the
// webhooks package) verifies the schema↔webhook accept-set
// equivalence; TestInstanceSettings_ResourcePatternsUseCanonicalConstants
// (here) verifies the schema references these constants directly.

const (
	// MemoryQuantityPattern matches valid Kubernetes memory quantities
	// for spec.resources.memory: a positive integer with a Ki/Mi/Gi
	// suffix.
	MemoryQuantityPattern = `^[1-9][0-9]*(Ki|Mi|Gi)$`

	// StorageQuantityPattern matches valid storage quantities for
	// spec.storage.size: a positive integer with a Gi/Mi suffix.
	StorageQuantityPattern = `^[1-9][0-9]*(Gi|Mi)$`

	// CPUQuantityPattern matches valid CPU quantities for
	// spec.resources.cpu: positive millicores ("500m") or positive
	// fractional cores ("1.0", "0.5"). Zero magnitude is rejected:
	// "0m" and "0.0" are not useful for a workspace and would result
	// in unschedulable pods (kubelet rejects requests.cpu == 0). The
	// three alternations enumerate the positive-magnitude cases:
	//   - [1-9][0-9]*m            → "1m", "500m", "1000m"
	//   - [1-9][0-9]*\.[0-9]+     → "1.0", "1.5", "16.0"
	//   - 0\.[0-9]*[1-9][0-9]*    → "0.5", "0.001" (zero whole part,
	//                               but a non-zero digit somewhere
	//                               in the fractional part)
	CPUQuantityPattern = `^([1-9][0-9]*m|[1-9][0-9]*\.[0-9]+|0\.[0-9]*[1-9][0-9]*)$`
)
