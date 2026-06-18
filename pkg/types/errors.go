// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package types

import "errors"

// Common sentinel errors used across service-layer methods. Callers use
// errors.Is to branch on these without coupling to a specific service.
var (
	ErrNotFound         = errors.New("resource not found")
	ErrPermissionDenied = errors.New("permission denied")
	ErrInvalidInput     = errors.New("invalid input")
	ErrAlreadyExists    = errors.New("resource already exists")
)
