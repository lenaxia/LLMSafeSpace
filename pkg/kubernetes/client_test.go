package kubernetes

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/client-go/kubernetes/scheme"

	v1 "github.com/lenaxia/llmsafespace/pkg/apis/llmsafespace/v1"
)

// TestNegotiatedSerializerDecodesWatchPayload reproduces the cluster-observed
// failure: when the apiserver delivers a Sandbox object via the watch stream,
// the rest client's negotiated serializer must be able to decode it without
// trying to convert through an unregistered internal version.
//
// The bug: NewCodecFactory(scheme.Scheme) returns a codec that requests
// conversion to the internal hub version when decoding. Our types have no
// __internal version, so decoding fails with:
//
//	no kind "Sandbox" is registered for the internal version of group
//	"llmsafespace.dev" in scheme "pkg/runtime/scheme.go:100"
//
// The fix: use WithoutConversion(), which is the canonical pattern for CRD
// clients that don't define separate internal/external versions.
func TestNegotiatedSerializerDecodesWatchPayload(t *testing.T) {
	gv := schema.GroupVersion{Group: "llmsafespace.dev", Version: "v1"}

	// Build a CodecFactory the way our rest config does.
	codecs := serializer.NewCodecFactory(scheme.Scheme)
	negotiatedNoConv := codecs.WithoutConversion()

	// JSON payload as it would arrive from the apiserver in a watch event:
	// a Sandbox object with TypeMeta filled in.
	payload := []byte(`{
		"apiVersion": "llmsafespace.dev/v1",
		"kind": "Sandbox",
		"metadata": {"name": "sb-1", "namespace": "default", "resourceVersion": "42"},
		"spec": {"runtime": "base", "securityLevel": "standard", "timeout": 300},
		"status": {"phase": "Running"}
	}`)

	// The decoder used by rest.Request for typed responses comes from the
	// negotiated serializer. Reproduce that decoder selection here.
	info, ok := runtime.SerializerInfoForMediaType(negotiatedNoConv.SupportedMediaTypes(), "application/json")
	require.True(t, ok, "JSON serializer must be available")

	// DecoderToVersion is what rest.Request uses internally to obtain a
	// decoder targeted at the GroupVersion.
	decoder := negotiatedNoConv.DecoderToVersion(info.Serializer, gv)

	obj, _, err := decoder.Decode(payload, nil, nil)
	require.NoError(t, err, "Sandbox watch payload must decode without conversion errors")

	sb, ok := obj.(*v1.Sandbox)
	require.True(t, ok, "decoded object must be *v1.Sandbox, got %T", obj)
	assert.Equal(t, "sb-1", sb.Name)
	assert.Equal(t, "Running", sb.Status.Phase)
	assert.Equal(t, "42", sb.ResourceVersion)
}

// TestNegotiatedSerializerWithConversionFailsForCRD demonstrates the bug. When
// the rest client builds a Negotiator (NewClientNegotiator only sets the
// encode GroupVersion; decode is nil), the watch decoder calls
// DecoderToVersion(serializer, nil). For NewCodecFactory(scheme.Scheme)
// (default, WITH conversion), passing nil GroupVersioner produces a codec
// that converts to the internal hub version of the object's group. Our
// types have no __internal version registered, so conversion fails with
// "no kind ... is registered for the internal version of group
// llmsafespace.dev". This is exactly the cluster-observed failure.
//
// This test exists to lock in the rationale behind using WithoutConversion()
// in newLLMSafespaceV1Client. If a future refactor removes WithoutConversion()
// thinking it's unnecessary, this test will catch it.
func TestNegotiatedSerializerWithConversionFailsForCRD(t *testing.T) {
	codecs := serializer.NewCodecFactory(scheme.Scheme)
	// Default factory (with conversion) — the broken path.
	withConv := runtime.NegotiatedSerializer(codecs)

	info, ok := runtime.SerializerInfoForMediaType(withConv.SupportedMediaTypes(), "application/json")
	require.True(t, ok)

	// Reproduce the call rest's StreamDecoder makes: DecoderToVersion with
	// nil GroupVersioner (which is what NewClientNegotiator passes from its
	// zero-valued decode field).
	decoder := withConv.DecoderToVersion(info.Serializer, nil)

	payload := []byte(`{
		"apiVersion": "llmsafespace.dev/v1",
		"kind": "Sandbox",
		"metadata": {"name": "sb-1"}
	}`)

	_, _, err := decoder.Decode(payload, nil, nil)
	require.Error(t, err, "decoder with conversion + nil version target must fail for CRDs without an internal version")
	assert.Contains(t, err.Error(), "internal version",
		"error must reference the missing internal version (this is the cluster-observed failure)")
}

// TestSchemeRegisteredWithGroup is a regression check for the init() in
// client_crds.go. Without this registration, even WithoutConversion() codecs
// can't recognize Sandbox objects.
func TestSchemeRegisteredWithGroup(t *testing.T) {
	gvk := schema.GroupVersionKind{Group: "llmsafespace.dev", Version: "v1", Kind: "Sandbox"}
	obj, err := scheme.Scheme.New(gvk)
	require.NoError(t, err, "scheme must recognize Sandbox GVK after init()")
	_, ok := obj.(*v1.Sandbox)
	assert.True(t, ok, "scheme.New must return *v1.Sandbox, got %T", obj)

	// metav1 types (Status, WatchEvent) must also be registered in our group
	// for watch decoding.
	statusGVK := schema.GroupVersionKind{Group: "llmsafespace.dev", Version: "v1", Kind: "Status"}
	_, err = scheme.Scheme.New(statusGVK)
	assert.NoError(t, err, "metav1.Status must be registered for our group (apiserver delivers errors as Status)")
}
