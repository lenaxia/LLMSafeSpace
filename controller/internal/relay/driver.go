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
	Name        string
	Region      string
	Shape       string
	CloudInit   string
	WireGuardIP string
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
type VMInstance struct {
	InstanceID string
	PublicIP   string
	State      VMState
}

// Error classification for circuit breaker logic.
var (
	// ErrCapacity indicates the provider returned a capacity/availability error.
	// These should NOT count against the provisioning circuit breaker.
	ErrCapacity = errors.New("provider capacity exhausted")

	// ErrConfig indicates a configuration error (bad AMI, invalid shape, etc.).
	// These DO count against the circuit breaker.
	ErrConfig = errors.New("provider configuration error")

	// ErrNotImplemented indicates the driver does not support this provider yet.
	ErrNotImplemented = errors.New("provider driver not implemented")

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
