// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package workspace

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"

	v1 "github.com/lenaxia/llmsafespaces/pkg/apis/llmsafespaces/v1"
)

// wsWithStorage builds a workspace fixture with explicit storage config so
// buildPVC tests do not depend on webhook defaulting.
func wsWithStorage(name, ns, size, sc, accessMode string) *v1.Workspace {
	ws := &v1.Workspace{
		ObjectMeta: metav1.ObjectMeta{
			Name: name, Namespace: ns,
			UID: types.UID("ws-uid-buildpvc"),
		},
		Spec: v1.WorkspaceSpec{
			Runtime: "python:3.11",
			Storage: v1.WorkspaceStorageConfig{
				Size:             size,
				StorageClassName: sc,
				AccessMode:       accessMode,
			},
		},
	}
	return ws
}

// TestBuildPVC_PropagatesStorageClass — when spec.storage.storageClassName
// is set, the built PVC must carry it so the PVC binds to the operator-chosen
// class. Value: without propagation the PVC falls back to the default class,
// which may be wrong (e.g. local-path instead of longhorn) → PVC stuck Pending.
// Failure mode: storageClassName dropped → PVC never binds. Expected: PVC.spec
// .storageClassName matches the workspace spec (non-nil).
func TestBuildPVC_PropagatesStorageClass(t *testing.T) {
	r := reconcilerFor(t)
	ws := wsWithStorage("ws-sc", "ns", "15Gi", "longhorn", "ReadWriteOnce")

	pvc := r.buildPVC(ws, "pvc-sc")
	require.NotNil(t, pvc)
	require.NotNil(t, pvc.Spec.StorageClassName, "storageClassName must be set, not nil")
	assert.Equal(t, "longhorn", *pvc.Spec.StorageClassName)
}

// TestBuildPVC_OmitsStorageClassWhenUnset — when the workspace does not name a
// storage class, buildPVC must leave storageClassName nil so Kubernetes applies
// the cluster default class. Value: forcing empty-string would create a PVC with
// no class binding. Failure mode: PVC bound to wrong/empty class. Expected:
// storageClassName is nil.
func TestBuildPVC_OmitsStorageClassWhenUnset(t *testing.T) {
	r := reconcilerFor(t)
	ws := wsWithStorage("ws-nosc", "ns", "5Gi", "", "ReadWriteOnce")

	pvc := r.buildPVC(ws, "pvc-nosc")
	require.NotNil(t, pvc)
	assert.Nil(t, pvc.Spec.StorageClassName,
		"unset storage class must stay nil so the cluster default applies")
}

// TestBuildPVC_SizeAndAccessMode — size and access mode from the workspace
// spec must reach the PVC resource requests. Value: a PVC with the wrong size
// silently caps user storage; wrong access mode breaks multi-attach semantics.
// Failure mode: size/access mode dropped or swapped. Expected: 15Gi request;
// ReadWriteMany → ReadWriteMany access mode.
func TestBuildPVC_SizeAndAccessMode(t *testing.T) {
	r := reconcilerFor(t)
	ws := wsWithStorage("ws-sz", "ns", "15Gi", "", "ReadWriteMany")

	pvc := r.buildPVC(ws, "pvc-sz")
	reqStorage := pvc.Spec.Resources.Requests[corev1.ResourceStorage]
	require.Equal(t, "15Gi", reqStorage.String())
	require.Len(t, pvc.Spec.AccessModes, 1)
	assert.Equal(t, corev1.ReadWriteMany, pvc.Spec.AccessModes[0],
		"ReadWriteMany workspace must produce a ReadWriteMany PVC")
}

// TestBuildPVC_DefaultsAccessModeToRWO — when access mode is empty (webhook
// defaulting bypassed), buildPVC defaults to ReadWriteOnce (single-writer),
// matching the CRD default. Value: a PVC with no access mode is invalid.
// Failure mode: empty access mode → invalid PVC. Expected: ReadWriteOnce.
func TestBuildPVC_DefaultsAccessModeToRWO(t *testing.T) {
	r := reconcilerFor(t)
	ws := wsWithStorage("ws-def", "ns", "5Gi", "", "")

	pvc := r.buildPVC(ws, "pvc-def")
	require.Len(t, pvc.Spec.AccessModes, 1)
	assert.Equal(t, corev1.ReadWriteOnce, pvc.Spec.AccessModes[0])
}

// TestBuildPVC_LabelsIdentifyWorkspace — the PVC labels (app/component/
// workspace) are how NetworkPolicy, garbage collection, and the stale-PVC
// detector select the PVC. Value: a missing label detaches the PVC from
// GC/selection logic → orphaned PVCs. Failure mode: label missing. Expected:
// all three identifying labels present and correct.
func TestBuildPVC_LabelsIdentifyWorkspace(t *testing.T) {
	r := reconcilerFor(t)
	ws := wsWithStorage("ws-lbl", "ns", "5Gi", "", "ReadWriteOnce")

	pvc := r.buildPVC(ws, "pvc-lbl")
	assert.Equal(t, AppName, pvc.Labels[LabelApp])
	assert.Equal(t, ComponentWorkspace, pvc.Labels[LabelComponent])
	assert.Equal(t, "ws-lbl", pvc.Labels[LabelWorkspace],
		"workspace label must match the workspace name for selection")
}

// TestPVCUsesWaitForFirstConsumer_True — a PVC whose storage class uses
// WaitForFirstConsumer binding must be recognized so the controller does NOT
// treat an unbound PVC as a failure (the binder delays binding until pod
// scheduling). Value: misclassifying WFC as a bind failure → workspace stuck
// Creating. Failure mode: WFC PVC flagged stale/failed. Expected: true.
func TestPVCUsesWaitForFirstConsumer_True(t *testing.T) {
	sc := &storagev1.StorageClass{
		ObjectMeta:        metav1.ObjectMeta{Name: "wfc-sc"},
		VolumeBindingMode: ptr.To(storagev1.VolumeBindingWaitForFirstConsumer),
	}
	r := reconcilerFor(t, sc)
	scName := "wfc-sc"
	pvc := &corev1.PersistentVolumeClaim{
		Spec: corev1.PersistentVolumeClaimSpec{StorageClassName: &scName},
	}

	assert.True(t, r.pvcUsesWaitForFirstConsumer(context.Background(), pvc),
		"WFC storage class must be detected")
}

// TestPVCUsesWaitForFirstConsumer_FalseForImmediateBinding — the mirror case:
// an Immediate-binding class must report false so the controller DOES expect
// prompt binding. Value: the controller would wait forever for a PVC that
// should already be bound. Failure mode: Immediate treated as WFC. Expected:
// false.
func TestPVCUsesWaitForFirstConsumer_FalseForImmediateBinding(t *testing.T) {
	sc := &storagev1.StorageClass{
		ObjectMeta:        metav1.ObjectMeta{Name: "imm-sc"},
		VolumeBindingMode: ptr.To(storagev1.VolumeBindingImmediate),
	}
	r := reconcilerFor(t, sc)
	scName := "imm-sc"
	pvc := &corev1.PersistentVolumeClaim{
		Spec: corev1.PersistentVolumeClaimSpec{StorageClassName: &scName},
	}

	assert.False(t, r.pvcUsesWaitForFirstConsumer(context.Background(), pvc),
		"Immediate-binding class must not be treated as WFC")
}

// TestPVCUsesWaitForFirstConsumer_FalseWhenNoStorageClass — a PVC with no
// storage class (cluster default) cannot be WFC-known; report false so the
// controller uses the normal bind expectation. Value: avoid a false WFC flag
// that would mask a genuine bind failure. Expected: false.
func TestPVCUsesWaitForFirstConsumer_FalseWhenNoStorageClass(t *testing.T) {
	r := reconcilerFor(t)
	pvc := &corev1.PersistentVolumeClaim{Spec: corev1.PersistentVolumeClaimSpec{}}

	assert.False(t, r.pvcUsesWaitForFirstConsumer(context.Background(), pvc))
}

// TestPVCUsesWaitForFirstConsumer_FalseWhenClassMissing — if the named storage
// class does not exist (Get fails), the method fails closed to false rather
// than panicking. Value: a missing SC must not crash the reconcile. Failure
// mode: nil-deref on missing storage class. Expected: false, no panic.
func TestPVCUsesWaitForFirstConsumer_FalseWhenClassMissing(t *testing.T) {
	r := reconcilerFor(t)
	scName := "does-not-exist"
	pvc := &corev1.PersistentVolumeClaim{
		Spec: corev1.PersistentVolumeClaimSpec{StorageClassName: &scName},
	}

	assert.False(t, r.pvcUsesWaitForFirstConsumer(context.Background(), pvc),
		"missing storage class must fail closed to false, not panic")
}
