// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package relay

import (
	"testing"

	"github.com/stretchr/testify/assert"

	v1 "github.com/lenaxia/llmsafespaces/pkg/apis/llmsafespaces/v1"
)

// TestTransitionInstanceState_AllPaths is a table-driven exhaustive test of
// the state transition helper. Pinned per the PR #331 reviewer's concern that
// the terminal-state preservation guarantee (the controller-driven drain wins
// path) was untested.
func TestTransitionInstanceState_AllPaths(t *testing.T) {
	cases := []struct {
		name    string
		current string
		healthy bool
		want    string
	}{
		// Routine axis: provisioning ↔ healthy ↔ unhealthy
		{"empty + healthy → healthy", "", true, string(v1.RelayStateHealthy)},
		{"empty + unhealthy → empty (boot grace)", "", false, ""},
		{"provisioning + healthy → healthy", string(v1.RelayStateProvisioning), true, string(v1.RelayStateHealthy)},
		{"provisioning + unhealthy → provisioning (boot grace)", string(v1.RelayStateProvisioning), false, string(v1.RelayStateProvisioning)},
		{"healthy + healthy → healthy (idempotent)", string(v1.RelayStateHealthy), true, string(v1.RelayStateHealthy)},
		{"healthy + unhealthy → unhealthy", string(v1.RelayStateHealthy), false, string(v1.RelayStateUnhealthy)},
		{"unhealthy + healthy → healthy (recovery)", string(v1.RelayStateUnhealthy), true, string(v1.RelayStateHealthy)},
		{"unhealthy + unhealthy → unhealthy (idempotent)", string(v1.RelayStateUnhealthy), false, string(v1.RelayStateUnhealthy)},

		// Terminal/explicit states — must be preserved regardless of router report
		{"draining + healthy → draining (controller drain wins)", string(v1.RelayStateDraining), true, string(v1.RelayStateDraining)},
		{"draining + unhealthy → draining", string(v1.RelayStateDraining), false, string(v1.RelayStateDraining)},
		{"terminated + healthy → terminated", string(v1.RelayStateTerminated), true, string(v1.RelayStateTerminated)},
		{"terminated + unhealthy → terminated", string(v1.RelayStateTerminated), false, string(v1.RelayStateTerminated)},
		{"quota-exhausted + healthy → quota-exhausted", string(v1.RelayStateQuotaExhausted), true, string(v1.RelayStateQuotaExhausted)},
		{"quota-exhausted + unhealthy → quota-exhausted", string(v1.RelayStateQuotaExhausted), false, string(v1.RelayStateQuotaExhausted)},
		{"provisioning-failed + healthy → provisioning-failed", string(v1.RelayStateProvisioningFailed), true, string(v1.RelayStateProvisioningFailed)},
		{"provisioning-failed + unhealthy → provisioning-failed", string(v1.RelayStateProvisioningFailed), false, string(v1.RelayStateProvisioningFailed)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := transitionInstanceState(tc.current, tc.healthy)
			assert.Equal(t, tc.want, got)
		})
	}
}
