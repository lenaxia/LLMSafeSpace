// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package types

import "time"

// OrgRole represents a user's role within an organization.
type OrgRole string

const (
	OrgRoleAdmin  OrgRole = "admin"
	OrgRoleMember OrgRole = "member"
)

// OrgStatus is the operational status of an organization. It gates access:
// only non-suspended orgs are usable via OrgMemberGuard/OrgAdminGuard. Both
// 'active' and 'pending_activation' allow access (the creator needs to reach
// the portal and Stripe checkout while pending); 'suspended' is fully locked.
type OrgStatus string

const (
	OrgStatusPendingActivation OrgStatus = "pending_activation"
	OrgStatusActive            OrgStatus = "active"
	OrgStatusSuspended         OrgStatus = "suspended"
)

// OrgPlan is the product plan identifier stored in organizations.plan_id and
// used locally for feature gating. The plan is set at org creation
// (enterprise for platform-admin orgs; the selected checkout plan on
// checkout.session.completed for self-service orgs). Per-event plan syncing
// from Stripe is planned for US-43.15.
type OrgPlan string

const (
	PlanFree       OrgPlan = "free"
	PlanTeam       OrgPlan = "team"
	PlanBusiness   OrgPlan = "business"
	PlanEnterprise OrgPlan = "enterprise"
)

// OrgSubscriptionStatus tracks the Stripe subscription lifecycle separately
// from OrgStatus. An org can be status='active' (members retain access) while
// subscription_status='past_due' (in the 7-day Smart Retries grace window).
type OrgSubscriptionStatus string

const (
	SubscriptionInactive OrgSubscriptionStatus = "inactive"
	SubscriptionActive   OrgSubscriptionStatus = "active"
	SubscriptionTrialing OrgSubscriptionStatus = "trialing"
	SubscriptionPastDue  OrgSubscriptionStatus = "past_due"
	SubscriptionCanceled OrgSubscriptionStatus = "canceled"
	SubscriptionUnpaid   OrgSubscriptionStatus = "unpaid"
)

// Organization is the API DTO for an organization.
type Organization struct {
	ID                 string                `json:"id"`
	Name               string                `json:"name"`
	Slug               string                `json:"slug"`
	CreatedBy          string                `json:"createdBy"`
	CreatedAt          time.Time             `json:"createdAt"`
	UpdatedAt          time.Time             `json:"updatedAt"`
	Status             OrgStatus             `json:"status"`
	PlanID             OrgPlan               `json:"planId"`
	SubscriptionStatus OrgSubscriptionStatus `json:"subscriptionStatus"`
}

// OrgMember is the API DTO for an organization membership.
type OrgMember struct {
	OrgID     string    `json:"orgId"`
	UserID    string    `json:"userId"`
	Username  string    `json:"username"`
	Email     string    `json:"email"`
	Role      OrgRole   `json:"role"`
	CreatedAt time.Time `json:"createdAt"`
}

// CreateOrgRequest is the request body for creating an organization. The slug is
// lowercased by the service before insert and uniqueness check.
//
// Per design 0031 D1, org creation is platform-admin only. The admin supplies
// the intended owner's email; the backend resolves it to a user ID. This is a
// single lookup, not a search/list endpoint (account-enumeration prevention).
type CreateOrgRequest struct {
	Name       string  `json:"name"       binding:"required,min=2,max=100"`
	Slug       string  `json:"slug"       binding:"required,min=2,max=50,alphanum"`
	OwnerEmail string  `json:"ownerEmail" binding:"required,email"`
	PlanID     OrgPlan `json:"planId"     binding:"omitempty"`
}

// UpdateOrgRequest is the request body for updating an organization.
type UpdateOrgRequest struct {
	Name string `json:"name" binding:"omitempty,min=2,max=100"`
	Slug string `json:"slug" binding:"omitempty,min=2,max=50,alphanum"`
}

// CreateOrgResponse is returned by POST /api/v1/orgs. Org creation is
// platform-admin only (design 0031 D1).
type CreateOrgResponse struct {
	OrgResponse
}

// OrgResponse extends Organization with the calling user's membership context.
// UserRole is omitempty so that an empty string (caller is not a member — e.g.
// platform admin creating an org for someone else) is omitted from JSON rather
// than appearing as `"userRole": ""`.
type OrgResponse struct {
	Organization
	UserRole    OrgRole `json:"userRole,omitempty"`
	MemberCount int     `json:"memberCount"`
}

// AddOrgMemberRequest is the request body for adding an org member.
type AddOrgMemberRequest struct {
	UserID string  `json:"userId" binding:"required"`
	Role   OrgRole `json:"role"   binding:"required"`
}

// ChangeOrgMemberRoleRequest is the request body for changing a member's role.
type ChangeOrgMemberRoleRequest struct {
	Role OrgRole `json:"role" binding:"required"`
}

// --- US-43.2: Org invitations ---

// OrgInvitation is the API DTO for an org invitation row.
type OrgInvitation struct {
	ID         string     `json:"id"`
	OrgID      string     `json:"orgId"`
	Email      string     `json:"email"`
	Role       OrgRole    `json:"role"`
	InvitedBy  string     `json:"invitedBy"`
	TokenHash  string     `json:"-"`
	ExpiresAt  time.Time  `json:"expiresAt"`
	AcceptedAt *time.Time `json:"acceptedAt,omitempty"`
	DeclinedAt *time.Time `json:"declinedAt,omitempty"`
	BounceType string     `json:"bounceType,omitempty"`
	BouncedAt  *time.Time `json:"bouncedAt,omitempty"`
	CreatedAt  time.Time  `json:"createdAt"`
}

// CreateInvitationsRequest is the body for POST /orgs/:id/invitations.
type CreateInvitationsRequest struct {
	Emails []string `json:"emails" binding:"required,min=1,max=100,dive,email"`
	Role   OrgRole  `json:"role"   binding:"required"`
}

// InvitationDetail is the public response for GET /invitations/:token. It does
// not expose the token hash or internal IDs beyond what the recipient needs to
// decide whether to accept.
type InvitationDetail struct {
	OrgName     string    `json:"orgName"`
	OrgSlug     string    `json:"orgSlug"`
	InviterName string    `json:"inviterName"`
	Role        OrgRole   `json:"role"`
	ExpiresAt   time.Time `json:"expiresAt"`
}
