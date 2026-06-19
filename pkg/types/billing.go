// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package types

import "time"

type OwnerType string

const (
	OwnerTypeUser OwnerType = "user"
	OwnerTypeOrg  OwnerType = "org"
)

type BillingOwner struct {
	ID   string    `json:"id"`
	Type OwnerType `json:"type"`
}

type UsageEvent struct {
	IdempotencyKey string         `json:"idempotencyKey,omitempty"`
	Owner          BillingOwner   `json:"owner"`
	ActorID        string         `json:"actorId"`
	WorkspaceID    string         `json:"workspaceId,omitempty"`
	EventType      string         `json:"eventType"`
	EventSubtype   string         `json:"eventSubtype,omitempty"`
	Quantity       int64          `json:"quantity"`
	ResourceTier   string         `json:"resourceTier,omitempty"`
	Region         string         `json:"region,omitempty"`
	Metadata       map[string]any `json:"metadata,omitempty"`
	RequestContext map[string]any `json:"requestContext,omitempty"`
	Source         string         `json:"source"`
	EventTime      time.Time      `json:"eventTime"`
}

type UsageReport struct {
	OwnerID     string                      `json:"ownerId"`
	OwnerType   OwnerType                   `json:"ownerType"`
	PeriodFrom  time.Time                   `json:"periodFrom"`
	PeriodTo    time.Time                   `json:"periodTo"`
	Totals      map[string]int64            `json:"totals"`
	ByWorkspace map[string]map[string]int64 `json:"byWorkspace,omitempty"`
	ByDay       map[string]map[string]int64 `json:"byDay,omitempty"`
}

type QuotaStatus struct {
	EventType  string    `json:"eventType"`
	PeriodType string    `json:"periodType"`
	Limit      int64     `json:"limit"`
	Used       int64     `json:"used"`
	Remaining  int64     `json:"remaining"`
	ResetsAt   time.Time `json:"resetsAt"`
}
