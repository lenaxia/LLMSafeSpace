// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package relay

import "context"

// GCPDriver is a stub implementation of ProviderDriver for GCP.
// Provision/Destroy/GetStatus return ErrNotImplemented.
// GCP was dropped from the default fleet (Always Free tier eliminated).
// Operators can add GCP as a paid provider if they want IP diversity.
type GCPDriver struct{}

func (d *GCPDriver) Provision(_ context.Context, _ ProvisionRequest) (*ProvisionResult, error) {
	return nil, ErrNotImplemented
}

func (d *GCPDriver) Destroy(_ context.Context, _, _ string) error {
	return ErrNotImplemented
}

func (d *GCPDriver) GetStatus(_ context.Context, _, _ string) (*VMStatus, error) {
	return nil, ErrNotImplemented
}

func (d *GCPDriver) ListInstances(_ context.Context, _ string) ([]VMInstance, error) {
	return nil, ErrNotImplemented
}
