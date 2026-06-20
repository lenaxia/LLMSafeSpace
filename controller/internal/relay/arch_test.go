// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package relay

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestArchForShape_ARM64(t *testing.T) {
	tests := []struct {
		provider string
		shape    string
	}{
		{"aws", "t4g.micro"},
		{"aws", "t4g.small"},
		{"aws", "t4g.medium"},
		{"aws", "c7g.large"},
		{"aws", "m6g.medium"},
		{"aws", "im4gn.large"},
		{"oci", "VM.Standard.A1.Flex"},
		{"oci", "VM.Standard.E1.Flex"},
	}
	for _, tt := range tests {
		t.Run(tt.provider+"_"+tt.shape, func(t *testing.T) {
			assert.Equal(t, "arm64", archForShape(tt.shape, tt.provider),
				"%s on %s is an ARM instance — must resolve to arm64 so the arm64 binary is downloaded", tt.shape, tt.provider)
		})
	}
}

func TestArchForShape_AMD64(t *testing.T) {
	tests := []struct {
		provider string
		shape    string
	}{
		{"aws", "t3.micro"},
		{"aws", "t3.small"},
		{"aws", "t2.micro"},
		{"aws", "m5.large"},
		{"aws", "c5.large"},
		{"gcp", "e2-micro"},
		{"gcp", "e2-small"},
		{"gcp", "n2-standard-2"},
	}
	for _, tt := range tests {
		t.Run(tt.provider+"_"+tt.shape, func(t *testing.T) {
			assert.Equal(t, "amd64", archForShape(tt.shape, tt.provider),
				"%s on %s is an x86 instance — must resolve to amd64 so the amd64 binary is downloaded", tt.shape, tt.provider)
		})
	}
}

func TestArchForShape_UnknownDefaultsToARM64(t *testing.T) {
	assert.Equal(t, "arm64", archForShape("unknown-shape", "aws"),
		"unknown shapes default to arm64 — the dominant relay architecture (AWS t4g / OCI A1), avoiding an amd64 misdownload on ARM hardware")
}

func TestArchForShape_EmptyShapeDefaultsToARM64(t *testing.T) {
	assert.Equal(t, "arm64", archForShape("", "aws"),
		"empty shape (pre-defaulting) defaults to arm64 — the dominant relay architecture")
}

func TestBinaryNameForArch(t *testing.T) {
	assert.Equal(t, "relay-proxy-arm64", binaryNameForArch("arm64"))
	assert.Equal(t, "relay-proxy-amd64", binaryNameForArch("amd64"))
}
