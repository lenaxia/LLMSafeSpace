// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build tools
// +build tools

// This package contains imports of tools used during the build.
package tools

import (
	_ "k8s.io/code-generator"
	_ "k8s.io/code-generator/cmd/deepcopy-gen"
)
