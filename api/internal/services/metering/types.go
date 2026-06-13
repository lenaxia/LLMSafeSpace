// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package metering

import (
	"encoding/json"
	"time"

	"github.com/lenaxia/llmsafespace/pkg/types"
)

type BillingOwner = types.BillingOwner
type OwnerType = types.OwnerType
type UsageEvent = types.UsageEvent
type UsageReport = types.UsageReport
type QuotaStatus = types.QuotaStatus

const (
	OwnerTypeUser = types.OwnerTypeUser
	OwnerTypeOrg  = types.OwnerTypeOrg
)

type DLQEntry struct {
	ID            int64
	Payload       json.RawMessage
	ErrorMessage  string
	RetryCount    int
	FirstFailedAt time.Time
	LastRetriedAt *time.Time
	ResolvedAt    *time.Time
	Resolution    *string
}

type WorkspaceLifecycleEvent struct {
	WorkspaceID  string
	OwnerID      string
	OwnerType    OwnerType
	FromPhase    string
	ToPhase      string
	ResourceTier string
	EventTime    time.Time
}
