// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package v1

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func assertFieldJSONTag(t *testing.T, obj interface{}, goField, expectedTag string) {
	t.Helper()
	typ := reflect.TypeOf(obj)
	field, ok := typ.FieldByName(goField)
	require.True(t, ok, "%T must have field %s", obj, goField)
	assert.Equal(t, expectedTag, field.Tag.Get("json"))
}

func TestInferenceRelayConditionType_Constants(t *testing.T) {
	cases := map[InferenceRelayConditionType]string{
		InferenceRelayConditionReady:              "Ready",
		InferenceRelayConditionDegraded:           "Degraded",
		InferenceRelayConditionProvisioningFailed: "ProvisioningFailed",
		InferenceRelayConditionRotating:           "Rotating",
		InferenceRelayConditionFallbackActive:     "FallbackActive",
	}
	for got, want := range cases {
		assert.Equal(t, want, string(got))
	}
}

func TestRelayInstanceState_Constants(t *testing.T) {
	cases := map[RelayInstanceState]string{
		RelayStateProvisioning:       "provisioning",
		RelayStateHealthy:            "healthy",
		RelayStateDraining:           "draining",
		RelayStateUnhealthy:          "unhealthy",
		RelayStateQuotaExhausted:     "quota-exhausted",
		RelayStateTerminated:         "terminated",
		RelayStateProvisioningFailed: "provisioning-failed",
	}
	for got, want := range cases {
		assert.Equal(t, want, string(got))
	}
}

func TestInferenceRelaySpec_FieldShape(t *testing.T) {
	tests := []struct {
		goField string
		jsonTag string
	}{
		{"UpstreamURL", "upstreamURL"},
		{"Providers", "providers"},
		{"WireGuard", "wireGuard,omitempty"},
		{"HealthCheck", "healthCheck,omitempty"},
		{"Rotation", "rotation,omitempty"},
		{"Fallback", "fallback,omitempty"},
	}
	for _, tt := range tests {
		t.Run(tt.goField, func(t *testing.T) {
			assertFieldJSONTag(t, InferenceRelaySpec{}, tt.goField, tt.jsonTag)
		})
	}
}

func TestRelayProviderSpec_FieldShape(t *testing.T) {
	tests := []struct {
		goField string
		jsonTag string
	}{
		{"Provider", "provider"},
		{"Region", "region"},
		{"CredentialsRef", "credentialsRef"},
		{"Shape", "shape,omitempty"},
	}
	for _, tt := range tests {
		t.Run(tt.goField, func(t *testing.T) {
			assertFieldJSONTag(t, RelayProviderSpec{}, tt.goField, tt.jsonTag)
		})
	}
}

func TestWireGuardConfig_FieldShape(t *testing.T) {
	tests := []struct {
		goField string
		jsonTag string
	}{
		{"RouterPrivateKeyRef", "routerPrivateKeyRef,omitempty"},
		{"CIDR", "cidr,omitempty"},
		{"Port", "port,omitempty"},
		{"RouterEndpoint", "routerEndpoint"},
	}
	for _, tt := range tests {
		t.Run(tt.goField, func(t *testing.T) {
			assertFieldJSONTag(t, WireGuardConfig{}, tt.goField, tt.jsonTag)
		})
	}
}

func TestFallbackConfig_FieldShape(t *testing.T) {
	tests := []struct {
		goField string
		jsonTag string
	}{
		{"Enabled", "enabled"},
		{"Rate", "rate,omitempty"},
		{"MaxConcurrent", "maxConcurrent,omitempty"},
	}
	for _, tt := range tests {
		t.Run(tt.goField, func(t *testing.T) {
			assertFieldJSONTag(t, FallbackConfig{}, tt.goField, tt.jsonTag)
		})
	}
}

func TestRelayInstanceStatus_FieldShape(t *testing.T) {
	tests := []struct {
		goField string
		jsonTag string
	}{
		{"ID", "id"},
		{"Provider", "provider"},
		{"Region", "region"},
		{"WgIP", "wgIP"},
		{"PublicIP", "publicIP"},
		{"State", "state"},
		{"Healthy", "healthy"},
		{"EgressBytes", "egressBytes,omitempty"},
		{"ProvisioningAttempts", "provisioningAttempts,omitempty"},
		{"LastProvisionError", "lastProvisionError,omitempty"},
	}
	for _, tt := range tests {
		t.Run(tt.goField, func(t *testing.T) {
			assertFieldJSONTag(t, RelayInstanceStatus{}, tt.goField, tt.jsonTag)
		})
	}
}

func TestInferenceRelay_JSONRoundTrip(t *testing.T) {
	now := metav1.NewTime(time.Now().Truncate(time.Second))
	original := &InferenceRelay{
		TypeMeta:   metav1.TypeMeta{APIVersion: "llmsafespaces.dev/v1", Kind: "InferenceRelay"},
		ObjectMeta: metav1.ObjectMeta{Name: "relay-fleet"},
		Spec: InferenceRelaySpec{
			UpstreamURL: "https://opencode.ai/zen/v1",
			Providers: []RelayProviderSpec{
				{
					Provider: "oci",
					Region:   "us-ashburn-1",
					CredentialsRef: corev1.LocalObjectReference{
						Name: "oci-credentials",
					},
				},
				{
					Provider: "gcp",
					Region:   "us-central1-a",
					CredentialsRef: corev1.LocalObjectReference{
						Name: "gcp-credentials",
					},
				},
			},
			WireGuard: WireGuardConfig{
				CIDR:           "10.42.42.0/24",
				Port:           51820,
				RouterEndpoint: "relay-gw.safespaces.dev:51820",
			},
			HealthCheck: HealthCheckConfig{
				Interval:           metav1.Duration{Duration: 15 * time.Second},
				Timeout:            metav1.Duration{Duration: 5 * time.Second},
				UnhealthyThreshold: 3,
				ReplacementTimeout: metav1.Duration{Duration: 15 * time.Minute},
			},
			Rotation: RotationConfig{
				Enabled:         true,
				Max429Rate:      0.5,
				DetectionWindow: metav1.Duration{Duration: 5 * time.Minute},
				Cooldown:        metav1.Duration{Duration: 30 * time.Minute},
			},
			Fallback: FallbackConfig{
				Enabled:       true,
				Rate:          0.5,
				MaxConcurrent: 1,
			},
		},
		Status: InferenceRelayStatus{
			HealthyReplicas: 2,
			Instances: []RelayInstanceStatus{
				{
					ID:        "oci-1",
					Provider:  "oci",
					Region:    "us-ashburn-1",
					WgIP:      "10.42.42.2",
					PublicIP:  "203.0.113.1",
					State:     string(RelayStateHealthy),
					Healthy:   true,
					LastCheck: &now,
				},
			},
			Conditions: []metav1.Condition{
				{
					Type:               string(InferenceRelayConditionReady),
					Status:             metav1.ConditionTrue,
					LastTransitionTime: now,
					Reason:             "AllRelaysHealthy",
				},
			},
			LastRotation: &now,
		},
	}

	bytes, err := json.Marshal(original)
	require.NoError(t, err)

	var roundTrip InferenceRelay
	require.NoError(t, json.Unmarshal(bytes, &roundTrip))

	assert.Equal(t, original.Spec.UpstreamURL, roundTrip.Spec.UpstreamURL)
	require.Len(t, roundTrip.Spec.Providers, 2)
	assert.Equal(t, "oci", roundTrip.Spec.Providers[0].Provider)
	assert.Equal(t, "gcp", roundTrip.Spec.Providers[1].Provider)
	assert.Equal(t, "oci-credentials", roundTrip.Spec.Providers[0].CredentialsRef.Name)
	assert.Equal(t, "10.42.42.0/24", roundTrip.Spec.WireGuard.CIDR)
	assert.Equal(t, 51820, roundTrip.Spec.WireGuard.Port)
	assert.Equal(t, 15*time.Second, roundTrip.Spec.HealthCheck.Interval.Duration)
	assert.Equal(t, true, roundTrip.Spec.Rotation.Enabled)
	assert.Equal(t, 0.5, roundTrip.Spec.Fallback.Rate)
	assert.Equal(t, 2, roundTrip.Status.HealthyReplicas)
	require.Len(t, roundTrip.Status.Instances, 1)
	assert.Equal(t, "10.42.42.2", roundTrip.Status.Instances[0].WgIP)
	assert.Equal(t, "healthy", roundTrip.Status.Instances[0].State)
	require.Len(t, roundTrip.Status.Conditions, 1)
	assert.Equal(t, "Ready", roundTrip.Status.Conditions[0].Type)
}

func TestInferenceRelay_DeepCopy(t *testing.T) {
	now := metav1.NewTime(time.Now().Truncate(time.Second))
	original := &InferenceRelay{
		ObjectMeta: metav1.ObjectMeta{Name: "relay-fleet"},
		Spec: InferenceRelaySpec{
			UpstreamURL: "https://opencode.ai/zen/v1",
			Providers: []RelayProviderSpec{
				{Provider: "oci", Region: "us-ashburn-1"},
				{Provider: "gcp", Region: "us-central1-a"},
			},
		},
		Status: InferenceRelayStatus{
			HealthyReplicas: 1,
			Instances: []RelayInstanceStatus{
				{ID: "oci-1", State: "healthy", LastCheck: &now},
			},
			Conditions: []metav1.Condition{
				{Type: "Ready", Status: metav1.ConditionTrue},
			},
			LastRotation: &now,
		},
	}

	copy := original.DeepCopy()
	require.NotNil(t, copy)

	copy.Spec.Providers[0].Provider = "modified"
	assert.Equal(t, "oci", original.Spec.Providers[0].Provider)

	copy.Status.Instances[0].ID = "modified"
	assert.Equal(t, "oci-1", original.Status.Instances[0].ID)

	copy.Status.Conditions[0].Type = "modified"
	assert.Equal(t, "Ready", original.Status.Conditions[0].Type)

	require.NotNil(t, copy.Status.Instances[0].LastCheck)
	copy.Status.Instances[0].LastCheck = nil
	require.NotNil(t, original.Status.Instances[0].LastCheck)

	require.NotNil(t, copy.Status.LastRotation)
	copy.Status.LastRotation = nil
	require.NotNil(t, original.Status.LastRotation)
}

func TestInferenceRelayList_DeepCopy(t *testing.T) {
	l := &InferenceRelayList{
		Items: []InferenceRelay{
			{ObjectMeta: metav1.ObjectMeta{Name: "r1"}},
			{ObjectMeta: metav1.ObjectMeta{Name: "r2"}},
		},
	}
	c := l.DeepCopy()
	c.Items[0].Name = "modified"
	assert.Equal(t, "r1", l.Items[0].Name)
	assert.Equal(t, "r2", l.Items[1].Name)
}

func TestInferenceRelay_NilDeepCopy(t *testing.T) {
	var nilRelay *InferenceRelay
	assert.Nil(t, nilRelay.DeepCopy())

	var nilList *InferenceRelayList
	assert.Nil(t, nilList.DeepCopy())

	var nilSpec *InferenceRelaySpec
	assert.Nil(t, nilSpec.DeepCopy())

	var nilStatus *InferenceRelayStatus
	assert.Nil(t, nilStatus.DeepCopy())
}

func TestInferenceRelay_DeepCopyObject(t *testing.T) {
	original := &InferenceRelay{
		ObjectMeta: metav1.ObjectMeta{Name: "fleet"},
		Spec:       InferenceRelaySpec{UpstreamURL: "https://example.com"},
	}

	obj := original.DeepCopyObject()
	require.NotNil(t, obj)

	copied, ok := obj.(*InferenceRelay)
	require.True(t, ok)
	assert.Equal(t, "fleet", copied.Name)
	assert.Equal(t, "https://example.com", copied.Spec.UpstreamURL)

	copied.Spec.UpstreamURL = "modified"
	assert.Equal(t, "https://example.com", original.Spec.UpstreamURL)
}
