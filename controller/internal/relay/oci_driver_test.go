// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package relay

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestOCIProvisionTags pins the FreeformTags shape produced for OCI
// instances. Always includes managed-by; conditionally includes UID
// + provider so the reconciler's adopt pre-pass can find the VM if a
// post-Provision Status update was lost. See worklog 0473/0474.
func TestOCIProvisionTags(t *testing.T) {
	cases := []struct {
		name string
		req  ProvisionRequest
		want map[string]string
	}{
		{
			name: "full set",
			req:  ProvisionRequest{OwnerUID: "uid-abc", Provider: "oci"},
			want: map[string]string{
				TagManagedBy: TagManagedByValue,
				TagOwnerUID:  "uid-abc",
				TagProvider:  "oci",
			},
		},
		{
			name: "missing UID — managed-by + provider only",
			req:  ProvisionRequest{Provider: "oci"},
			want: map[string]string{
				TagManagedBy: TagManagedByValue,
				TagProvider:  "oci",
			},
		},
		{
			name: "missing provider — managed-by + UID only",
			req:  ProvisionRequest{OwnerUID: "uid-abc"},
			want: map[string]string{
				TagManagedBy: TagManagedByValue,
				TagOwnerUID:  "uid-abc",
			},
		},
		{
			name: "legacy/empty — managed-by only",
			req:  ProvisionRequest{},
			want: map[string]string{
				TagManagedBy: TagManagedByValue,
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ociProvisionTags(tc.req)
			assert.Equal(t, tc.want, got)
		})
	}
}
