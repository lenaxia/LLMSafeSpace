// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package relay

import (
	"context"
	"errors"
	"fmt"
)

// ProviderDriver abstracts cloud-provider VM lifecycle operations.
// Rotation is always destroy + provision (no IP rotation method by design).
type ProviderDriver interface {
	// Provision creates a relay VM with the given cloud-init userdata
	// and returns the instance ID and public IP.
	Provision(ctx context.Context, req ProvisionRequest) (*ProvisionResult, error)

	// Destroy terminates a relay VM.
	Destroy(ctx context.Context, instanceID, region string) error

	// GetStatus returns the current VM state and public IP.
	GetStatus(ctx context.Context, instanceID, region string) (*VMStatus, error)

	// ListInstances returns relay VMs managed by this driver.
	ListInstances(ctx context.Context, region string) ([]VMInstance, error)
}

// ProvisionRequest holds the parameters for provisioning a relay VM.
type ProvisionRequest struct {
	Name      string
	Region    string
	Shape     string
	CloudInit string

	// OwnerUID is the InferenceRelay CR's UID. Drivers MUST tag the
	// provisioned VM with this value so it can be adopted by the
	// reconciler if a Status update is lost mid-provisioning (the
	// classic K8s controller leak — provisioning side-effect committed
	// but Status persistence failed). Empty OwnerUID disables tagging
	// and the leak-protection guarantee. See worklog 0473/0474.
	OwnerUID string

	// Provider identifies which provider slot this VM serves
	// (e.g. "aws"). Drivers MUST tag the VM with this value so the
	// reconciler can adopt the right slot when multiple providers
	// share the same OwnerUID.
	Provider string
}

// ProvisionResult is returned by Provision on success.
type ProvisionResult struct {
	InstanceID string
	PublicIP   string
}

// VMStatus represents the observed state of a relay VM.
type VMStatus struct {
	InstanceID string
	State      VMState
	PublicIP   string
}

// VMState represents the lifecycle state of a VM as reported by the cloud provider.
type VMState string

const (
	VMStatePending    VMState = "pending"
	VMStateRunning    VMState = "running"
	VMStateStopping   VMState = "stopping"
	VMStateStopped    VMState = "stopped"
	VMStateTerminated VMState = "terminated"
	VMStateNotFound   VMState = "not-found"
)

// VMInstance is a lightweight summary used by ListInstances.
//
// OwnerUID and Provider are populated from the instance's tags when the
// driver provisioned with those tags set. Older VMs without tags will
// have empty values; the reconciler treats those as un-adoptable and
// the orphan detector destroys them after a grace period.
type VMInstance struct {
	InstanceID string
	PublicIP   string
	State      VMState
	OwnerUID   string
	Provider   string
}

// Tag keys used by all drivers when provisioning. Centralized here so
// any driver implementation uses the same wire contract.
const (
	// TagManagedBy identifies relay VMs (vs other VMs in the account).
	// Always set. Value is "llmsafespaces-relay".
	TagManagedBy = "managed-by"
	// TagOwnerUID is the InferenceRelay CR's UID; empty if Provision
	// was called without OwnerUID set (legacy / pre-fix VMs).
	TagOwnerUID = "inferencerelay-uid"
	// TagProvider identifies which provider slot the VM serves
	// (e.g. "aws", "oci", "gcp"). Empty for legacy VMs.
	TagProvider = "inferencerelay-provider"

	// TagManagedByValue is the canonical value of TagManagedBy.
	TagManagedByValue = "llmsafespaces-relay"
)

// Error classification for circuit breaker logic.
var (
	// ErrCapacity indicates the provider returned a capacity/availability error.
	// These should NOT count against the provisioning circuit breaker.
	ErrCapacity = errors.New("provider capacity exhausted")

	// ErrConfig indicates a configuration error (bad AMI, invalid shape, etc.).
	// These DO count against the circuit breaker.
	ErrConfig = errors.New("provider configuration error")

	// ErrTimeout indicates the provisioning operation timed out.
	ErrTimeout = errors.New("provisioning timed out")
)

// IsCapacityError returns true if the error is a provider capacity issue.
func IsCapacityError(err error) bool {
	return errors.Is(err, ErrCapacity)
}

// IsConfigError returns true if the error is a configuration issue.
func IsConfigError(err error) bool {
	return errors.Is(err, ErrConfig)
}

// fmtError wraps an error with context for logging.
func fmtError(op string, provider string, err error) error {
	return fmt.Errorf("%s %s relay: %w", op, provider, err)
}
