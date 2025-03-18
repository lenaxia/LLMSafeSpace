//go:build tools
// +build tools

// This package contains imports of tools used during the build.
package tools

import (
	_ "k8s.io/code-generator"
	_ "k8s.io/code-generator/cmd/deepcopy-gen"
)
