// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package relay

import "context"

// AWSDriver is a stub implementation of ProviderDriver for AWS.
// Provision/Destroy/GetStatus return ErrNotImplemented.
// This will be replaced with a real AWS EC2 driver (US-42.13).
type AWSDriver struct{}

func (d *AWSDriver) Provision(_ context.Context, _ ProvisionRequest) (*ProvisionResult, error) {
	return nil, ErrNotImplemented
}

func (d *AWSDriver) Destroy(_ context.Context, _, _ string) error {
	return ErrNotImplemented
}

func (d *AWSDriver) GetStatus(_ context.Context, _, _ string) (*VMStatus, error) {
	return nil, ErrNotImplemented
}

func (d *AWSDriver) ListInstances(_ context.Context, _ string) ([]VMInstance, error) {
	return nil, ErrNotImplemented
}
