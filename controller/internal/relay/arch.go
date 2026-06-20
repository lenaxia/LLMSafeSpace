// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package relay

import "strings"

// archForShape resolves the CPU architecture for a given provider VM shape.
// The controller uses this to embed the correct per-arch relay-proxy binary
// name and SHA-256 checksum into cloud-init. Correctness matters: a mismatch
// (amd64 binary on arm64 hardware) would make the VM boot but fail to exec
// the binary, leaving the relay structurally provisioned but non-functional.
//
// Known ARM64 families: AWS Graviton (t4g/c7g/m6g/r6g/im4gn), OCI Ampere (A1/E1).
// Known AMD64 families: AWS Intel/AMD (t3/t2/m5/c5), GCP (all e2/n2/n2d/c3).
// Unknown shapes default to arm64 — the dominant relay architecture in the
// default fleet (AWS t4g.micro + OCI VM.Standard.A1.Flex).
func archForShape(shape, provider string) string {
	s := strings.ToLower(strings.TrimSpace(shape))
	if s == "" {
		return "arm64"
	}

	switch {
	case isARM64Shape(s, provider):
		return "arm64"
	case isAMD64Shape(s, provider):
		return "amd64"
	default:
		return "arm64"
	}
}

func isARM64Shape(shape, provider string) bool {
	if provider == "oci" {
		return strings.Contains(shape, "a1.") || strings.Contains(shape, "e1.")
	}
	armPrefixes := []string{"t4g", "c7g", "m6g", "r6g", "x2gd", "im4gn", "g5g"}
	for _, p := range armPrefixes {
		if strings.HasPrefix(shape, p) {
			return true
		}
	}
	return false
}

func isAMD64Shape(shape, provider string) bool {
	if provider == "gcp" {
		return true
	}
	amdPrefixes := []string{"t3", "t2", "t1", "m5", "m5a", "m5ad", "m5d", "m5dn", "c5", "c5a", "c5d", "c5n", "r5", "r5a", "r5b", "r5d", "r5n", "i3", "i3en", "i4i", "z1d"}
	for _, p := range amdPrefixes {
		if strings.HasPrefix(shape, p) {
			return true
		}
	}
	return false
}

// binaryNameForArch returns the relay-proxy artifact name for an architecture.
// Used to construct download URLs and for logging.
func binaryNameForArch(arch string) string {
	if arch == "amd64" {
		return "relay-proxy-amd64"
	}
	return "relay-proxy-arm64"
}
