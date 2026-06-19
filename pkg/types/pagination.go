// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package types

// PaginationMetadata represents pagination metadata
type PaginationMetadata struct {
	// Total number of items
	Total int `json:"total"`

	// Start index
	Start int `json:"start"`

	// End index
	End int `json:"end"`

	// Limit per page
	Limit int `json:"limit"`

	// Offset
	Offset int `json:"offset"`
}

// ListOptions carries pagination and filtering parameters.
type ListOptions struct {
	Limit  int `json:"limit"`
	Offset int `json:"offset"`
}
