// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package relay

import (
	"context"
	"time"

	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	v1 "github.com/lenaxia/llmsafespaces/pkg/apis/llmsafespaces/v1"
)

// OrphanDetector periodically lists every cloud VM tagged
// "managed-by=llmsafespaces-relay" and destroys any whose owner UID
// does not match any active InferenceRelay CR. Catches the case where
// the controller missed a deletion event entirely (e.g. crash during
// finalizer processing, or pre-fix-version VMs with the legacy tag
// schema). See worklog 0473/0474.
//
// This is belt-and-suspenders to the per-CR adopt + sweep paths in
// reconciler.go. Those paths cover the common case (Status.Update
// conflict during provisioning, deletion-time Status mismatch). The
// orphan detector covers everything else.
type OrphanDetector struct {
	Client   client.Client
	Drivers  map[string]ProviderDriver
	Interval time.Duration

	// Regions enumerates the regions the detector should sweep per
	// driver, in addition to whatever regions are referenced by
	// active InferenceRelay CRs at sweep time. Set explicitly only
	// if you need to sweep regions that no current CR uses (e.g.
	// clean up after a region was removed from the spec).
	Regions []string
}

// Start runs the detector loop until ctx is canceled. It satisfies
// manager.Runnable so the controller-runtime manager can schedule it.
func (d *OrphanDetector) Start(ctx context.Context) error {
	logger := log.FromContext(ctx).WithName("relay-orphan-detector")
	interval := d.Interval
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	logger.Info("starting orphan detector", "interval", interval.String())

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Run once at startup so leaked instances from a prior controller
	// generation are cleaned up promptly.
	d.sweep(ctx)

	for {
		select {
		case <-ctx.Done():
			logger.Info("orphan detector stopped")
			return nil
		case <-ticker.C:
			d.sweep(ctx)
		}
	}
}

// NeedLeaderElection returns true so only the leader runs the sweep.
// Without this, every controller replica would race to destroy the
// same orphans (and one of the destroy calls would no-op or error).
func (d *OrphanDetector) NeedLeaderElection() bool {
	return true
}

// sweep performs one full sweep cycle. To avoid a race with the per-CR
// reconciler, the detector uses a sandwich pattern:
//
//	(pass 1) list CRs to discover regions
//	(pass 2) list VMs in those regions
//	(pass 3) list CRs again to get the active-UID set used for the check
//
// This guarantees that any VM in the snapshot whose owner CR exists
// in Kubernetes at the time-of-VM-list will appear in the second CR
// list, because:
//
//   - For the VM to exist at time-of-VM-list, the per-CR reconciler must
//     have called Provision, which only fires AFTER the CR was created.
//   - The second CR list happens AFTER the VM list. If the CR existed
//     at time-of-Provision, it cannot have been deleted in the
//     intervening window without the per-CR finalizer running first
//     (which destroys the VM, removing it from the cloud).
//
// Therefore: any VM in the snapshot whose owner CR is missing from the
// second CR list is genuinely orphaned (its CR was deleted before
// finalizer cleanup, or never existed).
//
// Errors are logged and per-(provider, region) isolated — one failing
// driver does not block others.
func (d *OrphanDetector) sweep(ctx context.Context) {
	logger := log.FromContext(ctx).WithName("relay-orphan-detector")

	// Pass 1: list CRs to discover (provider, region) pairs.
	regionRelayList := &v1.InferenceRelayList{}
	if err := d.Client.List(ctx, regionRelayList); err != nil {
		logger.Error(err, "list InferenceRelay CRs (pass 1) failed; skipping sweep")
		return
	}
	regionsByProvider := make(map[string]map[string]bool)
	for i := range regionRelayList.Items {
		r := &regionRelayList.Items[i]
		for _, ps := range r.Spec.Providers {
			if regionsByProvider[ps.Provider] == nil {
				regionsByProvider[ps.Provider] = make(map[string]bool)
			}
			regionsByProvider[ps.Provider][ps.Region] = true
		}
	}
	// Operator-supplied region overrides expand the sweep set so we
	// can clean up regions no current CR mentions.
	if len(d.Regions) > 0 {
		for prov := range d.Drivers {
			if regionsByProvider[prov] == nil {
				regionsByProvider[prov] = make(map[string]bool)
			}
			for _, region := range d.Regions {
				regionsByProvider[prov][region] = true
			}
		}
	}

	// Pass 2: snapshot VMs across all relevant (provider, region) pairs.
	type taggedVM struct {
		vm       VMInstance
		provider string
		region   string
	}
	var snapshot []taggedVM
	for provider, driver := range d.Drivers {
		regions := regionsByProvider[provider]
		if len(regions) == 0 {
			continue
		}
		for region := range regions {
			listed, err := driver.ListInstances(ctx, region)
			if err != nil {
				logger.Error(err, "ListInstances failed", "provider", provider, "region", region)
				continue
			}
			for _, vm := range listed {
				snapshot = append(snapshot, taggedVM{vm: vm, provider: provider, region: region})
			}
		}
	}

	// Pass 3: list CRs AGAIN. Use this list (not pass 1's) for the
	// active-UID check. Any CR whose Reconcile triggered a Provision
	// before pass 2 must appear here — see method-level doc comment.
	activeRelayList := &v1.InferenceRelayList{}
	if err := d.Client.List(ctx, activeRelayList); err != nil {
		logger.Error(err, "list InferenceRelay CRs (pass 3) failed; skipping sweep")
		return
	}
	activeUIDs := make(map[string]bool, len(activeRelayList.Items))
	for i := range activeRelayList.Items {
		uid := string(activeRelayList.Items[i].UID)
		if uid != "" {
			activeUIDs[uid] = true
		}
	}

	// Pass 4: classify each VM and destroy orphans.
	for _, t := range snapshot {
		d.maybeDestroyOrphan(ctx, logger, t.provider, t.region, t.vm, activeUIDs)
	}
}

// maybeDestroyOrphan applies the safety rules to a single VM and
// destroys it if it's an orphan. Empty OwnerUID is intentionally
// preserved (legacy / pre-fix VMs require operator audit).
func (d *OrphanDetector) maybeDestroyOrphan(
	ctx context.Context,
	logger logr.Logger,
	provider, region string,
	vm VMInstance,
	activeUIDs map[string]bool,
) {
	if vm.OwnerUID == "" {
		logger.V(1).Info("skipping legacy untagged VM (manual cleanup required)",
			"provider", provider, "region", region, "instanceID", vm.InstanceID)
		return
	}
	if activeUIDs[vm.OwnerUID] {
		return
	}
	if vm.State != VMStateRunning && vm.State != VMStatePending {
		return
	}
	driver := d.Drivers[provider]
	if driver == nil {
		return
	}
	logger.Info("destroying orphan VM (no matching active CR)",
		"provider", provider, "region", region,
		"instanceID", vm.InstanceID, "orphanedUID", vm.OwnerUID)
	if err := driver.Destroy(ctx, vm.InstanceID, region); err != nil {
		logger.Error(err, "destroy orphan VM failed",
			"provider", provider, "region", region, "instanceID", vm.InstanceID)
	}
}

// Compile-time interface check.
var _ manager.Runnable = (*OrphanDetector)(nil)
var _ manager.LeaderElectionRunnable = (*OrphanDetector)(nil)
