// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package database

import (
	"github.com/lenaxia/llmsafespaces/pkg/settings"
)

// Compile-time interface satisfaction checks.
// These ensure the database Service implements the store interfaces
// required by the settings package.
var (
	_ settings.InstanceStore = (*Service)(nil)
	_ settings.UserStore     = (*Service)(nil)
	_ settings.SeedStore     = (*Service)(nil)
)
