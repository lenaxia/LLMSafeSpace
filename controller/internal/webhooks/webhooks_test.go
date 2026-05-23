package webhooks

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	admissionv1 "k8s.io/api/admission/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	v1 "github.com/lenaxia/llmsafespace/pkg/apis/llmsafespace/v1"
)

// newScheme returns a runtime.Scheme with both clientgo and llmsafespace types.
func newScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(s))
	require.NoError(t, v1.AddToScheme(s))
	return s
}

func newAdmissionRequest(t *testing.T, obj runtime.Object) admission.Request {
	t.Helper()
	raw, err := json.Marshal(obj)
	require.NoError(t, err)
	return admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			Operation: admissionv1.Create,
			Namespace: "default",
			Object:    runtime.RawExtension{Raw: raw},
		},
	}
}

func TestSandboxValidator_Allowed(t *testing.T) {
	s := newScheme(t)
	dec := admission.NewDecoder(s)
	v := &SandboxValidator{Decoder: dec}

	sb := &v1.Sandbox{
		TypeMeta:   metav1.TypeMeta{APIVersion: "llmsafespace.dev/v1", Kind: "Sandbox"},
		ObjectMeta: metav1.ObjectMeta{Name: "ok", Namespace: "default"},
		Spec:       v1.SandboxSpec{Runtime: "python:3.11"},
	}

	resp := v.Handle(context.Background(), newAdmissionRequest(t, sb))
	assert.True(t, resp.Allowed, "expected allowed; got %+v", resp)
}

func TestSandboxValidator_DeniesEmptyRuntime(t *testing.T) {
	s := newScheme(t)
	v := &SandboxValidator{Decoder: admission.NewDecoder(s)}

	sb := &v1.Sandbox{
		TypeMeta:   metav1.TypeMeta{APIVersion: "llmsafespace.dev/v1", Kind: "Sandbox"},
		ObjectMeta: metav1.ObjectMeta{Name: "bad"},
		Spec:       v1.SandboxSpec{Runtime: ""},
	}
	resp := v.Handle(context.Background(), newAdmissionRequest(t, sb))
	assert.False(t, resp.Allowed)
	require.NotNil(t, resp.Result)
	assert.Contains(t, resp.Result.Message, "runtime is required")
}

func TestSandboxValidator_DeniesEmptyEgressDomain(t *testing.T) {
	s := newScheme(t)
	v := &SandboxValidator{Decoder: admission.NewDecoder(s)}

	sb := &v1.Sandbox{
		TypeMeta:   metav1.TypeMeta{APIVersion: "llmsafespace.dev/v1", Kind: "Sandbox"},
		ObjectMeta: metav1.ObjectMeta{Name: "bad"},
		Spec: v1.SandboxSpec{
			Runtime: "python:3.11",
			NetworkAccess: &v1.NetworkAccess{
				Egress: []v1.EgressRule{{Domain: ""}},
			},
		},
	}
	resp := v.Handle(context.Background(), newAdmissionRequest(t, sb))
	assert.False(t, resp.Allowed)
	assert.Contains(t, resp.Result.Message, "egress rule domain is required")
}

func TestSandboxValidator_DeniesMissingProfileRef(t *testing.T) {
	s := newScheme(t)
	cl := fake.NewClientBuilder().WithScheme(s).Build()
	v := &SandboxValidator{Client: cl, Decoder: admission.NewDecoder(s)}

	sb := &v1.Sandbox{
		TypeMeta:   metav1.TypeMeta{APIVersion: "llmsafespace.dev/v1", Kind: "Sandbox"},
		ObjectMeta: metav1.ObjectMeta{Name: "x", Namespace: "default"},
		Spec: v1.SandboxSpec{
			Runtime:    "python:3.11",
			ProfileRef: &v1.ProfileReference{Name: "missing-profile"},
		},
	}
	resp := v.Handle(context.Background(), newAdmissionRequest(t, sb))
	assert.False(t, resp.Allowed)
	assert.Contains(t, resp.Result.Message, "not found")
}

func TestSandboxValidator_AllowsExistingProfileRef(t *testing.T) {
	s := newScheme(t)
	existing := &v1.SandboxProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "p1", Namespace: "default"},
		Spec:       v1.SandboxProfileSpec{Language: "python"},
	}
	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(existing).Build()
	v := &SandboxValidator{Client: cl, Decoder: admission.NewDecoder(s)}

	sb := &v1.Sandbox{
		TypeMeta:   metav1.TypeMeta{APIVersion: "llmsafespace.dev/v1", Kind: "Sandbox"},
		ObjectMeta: metav1.ObjectMeta{Name: "x", Namespace: "default"},
		Spec: v1.SandboxSpec{
			Runtime:    "python:3.11",
			ProfileRef: &v1.ProfileReference{Name: "p1"},
		},
	}
	resp := v.Handle(context.Background(), newAdmissionRequest(t, sb))
	assert.True(t, resp.Allowed)
}

func TestSandboxValidator_BadJSONReturnsBadRequest(t *testing.T) {
	v := &SandboxValidator{Decoder: admission.NewDecoder(newScheme(t))}
	req := admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			Operation: admissionv1.Create,
			Object:    runtime.RawExtension{Raw: []byte("not json")},
		},
	}
	resp := v.Handle(context.Background(), req)
	assert.False(t, resp.Allowed)
	require.NotNil(t, resp.Result)
	assert.Equal(t, int32(http.StatusBadRequest), resp.Result.Code)
}

func TestRuntimeEnvironmentValidator_Allowed(t *testing.T) {
	s := newScheme(t)
	v := &RuntimeEnvironmentValidator{Decoder: admission.NewDecoder(s)}
	re := &v1.RuntimeEnvironment{
		TypeMeta:   metav1.TypeMeta{APIVersion: "llmsafespace.dev/v1", Kind: "RuntimeEnvironment"},
		ObjectMeta: metav1.ObjectMeta{Name: "py311"},
		Spec: v1.RuntimeEnvironmentSpec{
			Image:    "python:3.11-slim",
			Language: "python",
		},
	}
	resp := v.Handle(context.Background(), newAdmissionRequest(t, re))
	assert.True(t, resp.Allowed)
}

func TestRuntimeEnvironmentValidator_DeniesEmptyImage(t *testing.T) {
	s := newScheme(t)
	v := &RuntimeEnvironmentValidator{Decoder: admission.NewDecoder(s)}
	re := &v1.RuntimeEnvironment{
		TypeMeta:   metav1.TypeMeta{APIVersion: "llmsafespace.dev/v1", Kind: "RuntimeEnvironment"},
		ObjectMeta: metav1.ObjectMeta{Name: "x"},
		Spec:       v1.RuntimeEnvironmentSpec{Image: "", Language: "python"},
	}
	resp := v.Handle(context.Background(), newAdmissionRequest(t, re))
	assert.False(t, resp.Allowed)
	assert.Contains(t, resp.Result.Message, "image is required")
}

func TestRuntimeEnvironmentValidator_DeniesEmptyLanguage(t *testing.T) {
	s := newScheme(t)
	v := &RuntimeEnvironmentValidator{Decoder: admission.NewDecoder(s)}
	re := &v1.RuntimeEnvironment{
		TypeMeta:   metav1.TypeMeta{APIVersion: "llmsafespace.dev/v1", Kind: "RuntimeEnvironment"},
		ObjectMeta: metav1.ObjectMeta{Name: "x"},
		Spec:       v1.RuntimeEnvironmentSpec{Image: "img", Language: ""},
	}
	resp := v.Handle(context.Background(), newAdmissionRequest(t, re))
	assert.False(t, resp.Allowed)
	assert.Contains(t, resp.Result.Message, "language is required")
}

func TestSandboxProfileValidator_Allowed(t *testing.T) {
	s := newScheme(t)
	v := &SandboxProfileValidator{Decoder: admission.NewDecoder(s)}
	sp := &v1.SandboxProfile{
		TypeMeta:   metav1.TypeMeta{APIVersion: "llmsafespace.dev/v1", Kind: "SandboxProfile"},
		ObjectMeta: metav1.ObjectMeta{Name: "p"},
		Spec: v1.SandboxProfileSpec{
			Language: "python",
			NetworkPolicies: []v1.NetworkPolicy{
				{Type: "egress"},
				{Type: "ingress"},
			},
		},
	}
	resp := v.Handle(context.Background(), newAdmissionRequest(t, sp))
	assert.True(t, resp.Allowed)
}

func TestSandboxProfileValidator_DeniesEmptyLanguage(t *testing.T) {
	s := newScheme(t)
	v := &SandboxProfileValidator{Decoder: admission.NewDecoder(s)}
	sp := &v1.SandboxProfile{
		TypeMeta:   metav1.TypeMeta{APIVersion: "llmsafespace.dev/v1", Kind: "SandboxProfile"},
		ObjectMeta: metav1.ObjectMeta{Name: "p"},
		Spec:       v1.SandboxProfileSpec{Language: ""},
	}
	resp := v.Handle(context.Background(), newAdmissionRequest(t, sp))
	assert.False(t, resp.Allowed)
	assert.Contains(t, resp.Result.Message, "language is required")
}

func TestSandboxProfileValidator_DeniesUnknownNetworkPolicyType(t *testing.T) {
	s := newScheme(t)
	v := &SandboxProfileValidator{Decoder: admission.NewDecoder(s)}
	sp := &v1.SandboxProfile{
		TypeMeta:   metav1.TypeMeta{APIVersion: "llmsafespace.dev/v1", Kind: "SandboxProfile"},
		ObjectMeta: metav1.ObjectMeta{Name: "p"},
		Spec: v1.SandboxProfileSpec{
			Language:        "python",
			NetworkPolicies: []v1.NetworkPolicy{{Type: "garbage"}},
		},
	}
	resp := v.Handle(context.Background(), newAdmissionRequest(t, sp))
	assert.False(t, resp.Allowed)
	assert.Contains(t, resp.Result.Message, "egress")
}

// TestInjectDecoder verifies the legacy InjectDecoder no-op still sets the
// Decoder field. Required for tests or callers still using the old DI path.
func TestInjectDecoder(t *testing.T) {
	s := newScheme(t)
	dec := admission.NewDecoder(s)

	t.Run("Sandbox", func(t *testing.T) {
		v := &SandboxValidator{}
		require.NoError(t, v.InjectDecoder(dec))
		assert.NotNil(t, v.Decoder)
	})
	t.Run("RuntimeEnvironment", func(t *testing.T) {
		v := &RuntimeEnvironmentValidator{}
		require.NoError(t, v.InjectDecoder(dec))
		assert.NotNil(t, v.Decoder)
	})
	t.Run("SandboxProfile", func(t *testing.T) {
		v := &SandboxProfileValidator{}
		require.NoError(t, v.InjectDecoder(dec))
		assert.NotNil(t, v.Decoder)
	})
}
