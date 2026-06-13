// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package billing

import "context"

type BillingProvider interface {
	ReportUsage(ctx context.Context, events []UsageExportEvent) (reportedIDs []int64, err error)
	CreateCustomer(ctx context.Context, ownerID, ownerType, email string) (externalID string, err error)
	SuspendCustomer(ctx context.Context, externalID string) error
}

type UsageExportEvent struct {
	ExternalCustomerID string
	EventType          string
	Quantity           int64
	Timestamp          string
	IdempotencyKey     string
	Properties         map[string]string
}

type NoopBillingProvider struct{}

func (n *NoopBillingProvider) ReportUsage(_ context.Context, events []UsageExportEvent) ([]int64, error) {
	ids := make([]int64, len(events))
	for i := range events {
		ids[i] = int64(i + 1)
	}
	return ids, nil
}

func (n *NoopBillingProvider) CreateCustomer(_ context.Context, ownerID, _, _ string) (string, error) {
	return ownerID, nil
}

func (n *NoopBillingProvider) SuspendCustomer(_ context.Context, _ string) error {
	return nil
}
