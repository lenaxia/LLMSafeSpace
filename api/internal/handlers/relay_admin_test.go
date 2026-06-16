// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	k8smocks "github.com/lenaxia/llmsafespace/mocks/kubernetes"
	v1 "github.com/lenaxia/llmsafespace/pkg/apis/llmsafespace/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"
)

// ─── Test helpers ───────────────────────────────────────────────────────────

const testNamespace = "llmsafespace"

func setupRelayRouter(t *testing.T, clientset *fake.Clientset) (*gin.Engine, *RelayAdminHandler, *k8smocks.MockInferenceRelayInterface) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	llmMock := k8smocks.NewMockLLMSafespaceV1Interface()
	relayMock := k8smocks.NewMockInferenceRelayInterface()
	llmMock.On("InferenceRelays").Return(relayMock).Maybe()
	relayMock.On("List", mock.Anything, mock.Anything).Return(&v1.InferenceRelayList{}, nil).Maybe()

	h := NewRelayAdminHandler(clientset, llmMock, testNamespace, testNamespace, "http://relay-router.test:8080")

	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("userID", "admin-1")
		c.Set("userRole", "admin")
		c.Next()
	})

	g := r.Group("/api/v1/admin/relay")
	g.GET("/setup", h.GetSetup)
	g.GET("/status", h.GetStatus)
	g.POST("/oci-creds", h.SaveOCICreds)
	g.POST("/gcp-creds", h.SaveGCPCreds)
	g.POST("/aws-creds", h.SaveAWSCreds)
	g.POST("/deploy", h.Deploy)
	g.POST("/rotate/:id", h.Rotate)
	g.POST("/pause", h.Pause)
	g.POST("/resume", h.Resume)

	return r, h, relayMock
}

func makeRelayCR(name string, instances []v1.RelayInstanceStatus, healthy int) *v1.InferenceRelay {
	return &v1.InferenceRelay{
		TypeMeta:   metav1.TypeMeta{APIVersion: "llmsafespace.dev/v1", Kind: "InferenceRelay"},
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: v1.InferenceRelaySpec{
			UpstreamURL: "https://opencode.ai/zen/v1",
			Providers: []v1.RelayProviderSpec{
				{Provider: "oci", Region: "us-ashburn-1", Shape: "VM.Standard.A1.Flex"},
				{Provider: "gcp", Region: "us-west1", Shape: "e2-micro"},
			},
			WireGuard: v1.WireGuardConfig{RouterEndpoint: "relay-gw.example.com:51820"},
		},
		Status: v1.InferenceRelayStatus{
			Instances:       instances,
			HealthyReplicas: healthy,
		},
	}
}

func makeRelayCRWithConditions(name string, conditions []metav1.Condition) *v1.InferenceRelay {
	return &v1.InferenceRelay{
		TypeMeta:   metav1.TypeMeta{APIVersion: "llmsafespace.dev/v1", Kind: "InferenceRelay"},
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status: v1.InferenceRelayStatus{
			Conditions: conditions,
		},
	}
}

func doRelayRequest(r *gin.Engine, method, path string, body ...string) *httptest.ResponseRecorder {
	var buf *bytes.Buffer
	if len(body) > 0 {
		buf = bytes.NewBufferString(body[0])
	} else {
		buf = &bytes.Buffer{}
	}
	req, _ := http.NewRequest(method, path, buf)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func overrideList(relayMock *k8smocks.MockInferenceRelayInterface, list *v1.InferenceRelayList, err error) {
	var filtered []*mock.Call
	for _, c := range relayMock.ExpectedCalls {
		if c.Method != "List" {
			filtered = append(filtered, c)
		}
	}
	relayMock.ExpectedCalls = filtered
	relayMock.On("List", mock.Anything, mock.Anything).Return(list, err).Maybe()
}

type simpleError struct{ msg string }

func (e simpleError) Error() string { return e.msg }

func testError(msg string) error { return simpleError{msg: msg} }

func notFoundError() error {
	return apierrors.NewNotFound(schema.GroupResource{Group: "llmsafespace.dev", Resource: "inferencerelays"}, "relay-fleet")
}

// ─── US-43.2: GetSetup tests ────────────────────────────────────────────────

func TestRelaySetup_NoSecrets_NotConfigured(t *testing.T) {
	r, _, _ := setupRelayRouter(t, fake.NewSimpleClientset())

	w := doRelayRequest(r, "GET", "/api/v1/admin/relay/setup")

	require.Equal(t, http.StatusOK, w.Code)
	var resp setupResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.False(t, resp.OCIConfigured)
	assert.False(t, resp.GCPConfigured)
	assert.False(t, resp.Deployed)
	assert.False(t, resp.RouterDeployed)
}

func TestRelaySetup_OCISecretExists_Configured(t *testing.T) {
	clientset := fake.NewSimpleClientset(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "oci-credentials", Namespace: testNamespace},
	})
	r, _, _ := setupRelayRouter(t, clientset)

	w := doRelayRequest(r, "GET", "/api/v1/admin/relay/setup")

	require.Equal(t, http.StatusOK, w.Code)
	var resp setupResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.True(t, resp.OCIConfigured)
}

func TestRelaySetup_GCPSecretExists_Configured(t *testing.T) {
	clientset := fake.NewSimpleClientset(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "gcp-credentials", Namespace: testNamespace},
	})
	r, _, _ := setupRelayRouter(t, clientset)

	w := doRelayRequest(r, "GET", "/api/v1/admin/relay/setup")

	require.Equal(t, http.StatusOK, w.Code)
	var resp setupResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.True(t, resp.GCPConfigured)
}

func TestRelaySetup_RouterDeploymentExists_Deployed(t *testing.T) {
	clientset := fake.NewSimpleClientset(&appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "relay-router", Namespace: testNamespace},
	})
	r, _, _ := setupRelayRouter(t, clientset)

	w := doRelayRequest(r, "GET", "/api/v1/admin/relay/setup")

	require.Equal(t, http.StatusOK, w.Code)
	var resp setupResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.True(t, resp.RouterDeployed)
}

func TestRelaySetup_RouterNamespaceQueriesCorrectNS(t *testing.T) {
	// checkRouter must use routerNamespace (not h.namespace/workspace ns).
	// This test seeds the relay-router in "router-ns" and verifies the
	// handler finds it when routerNamespace="router-ns", while ignoring
	// deployments in the workspace namespace ("ws-ns").
	gin.SetMode(gin.TestMode)

	clientset := fake.NewSimpleClientset(&appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "relay-router", Namespace: "router-ns"},
	})
	llmMock := k8smocks.NewMockLLMSafespaceV1Interface()
	relayMock := k8smocks.NewMockInferenceRelayInterface()
	llmMock.On("InferenceRelays").Return(relayMock).Maybe()
	relayMock.On("List", mock.Anything, mock.Anything).Return(&v1.InferenceRelayList{}, nil).Maybe()

	// workspace ns != router ns
	h := NewRelayAdminHandler(clientset, llmMock, "ws-ns", "router-ns", "http://relay-router.test:8080")

	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("userID", "admin-1"); c.Set("userRole", "admin"); c.Next() })
	r.GET("/setup", h.GetSetup)

	w := doRelayRequest(r, "GET", "/setup")

	require.Equal(t, http.StatusOK, w.Code)
	var resp setupResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.True(t, resp.RouterDeployed, "deployment in router-ns should be found")
}

func TestRelaySetup_RouterNamespaceIgnoresWorkspaceNS(t *testing.T) {
	// Companion test: deployment in workspace namespace must NOT be found
	// when routerNamespace points elsewhere.
	gin.SetMode(gin.TestMode)

	clientset := fake.NewSimpleClientset(&appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "relay-router", Namespace: "ws-ns"},
	})
	llmMock := k8smocks.NewMockLLMSafespaceV1Interface()
	relayMock := k8smocks.NewMockInferenceRelayInterface()
	llmMock.On("InferenceRelays").Return(relayMock).Maybe()
	relayMock.On("List", mock.Anything, mock.Anything).Return(&v1.InferenceRelayList{}, nil).Maybe()

	h := NewRelayAdminHandler(clientset, llmMock, "ws-ns", "router-ns", "http://relay-router.test:8080")

	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("userID", "admin-1"); c.Set("userRole", "admin"); c.Next() })
	r.GET("/setup", h.GetSetup)

	w := doRelayRequest(r, "GET", "/setup")

	require.Equal(t, http.StatusOK, w.Code)
	var resp setupResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.False(t, resp.RouterDeployed, "deployment in ws-ns must not be found when routerNS is router-ns")
}

func TestRelaySetup_FleetDeployed_WireGuardEndpoint(t *testing.T) {
	r, _, relayMock := setupRelayRouter(t, fake.NewSimpleClientset())
	overrideList(relayMock, &v1.InferenceRelayList{Items: []v1.InferenceRelay{*makeRelayCR("relay-fleet", nil, 0)}}, nil)

	w := doRelayRequest(r, "GET", "/api/v1/admin/relay/setup")

	require.Equal(t, http.StatusOK, w.Code)
	var resp setupResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.True(t, resp.Deployed)
	assert.Equal(t, "relay-gw.example.com:51820", resp.WireGuardEndpoint)
}

// ─── US-43.1: GetStatus tests ───────────────────────────────────────────────

func TestRelayStatus_NotDeployed(t *testing.T) {
	r, _, _ := setupRelayRouter(t, fake.NewSimpleClientset())

	w := doRelayRequest(r, "GET", "/api/v1/admin/relay/status")

	require.Equal(t, http.StatusOK, w.Code)
	var resp statusResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.False(t, resp.Deployed)
}

func TestRelayStatus_HealthyFleet(t *testing.T) {
	r, _, relayMock := setupRelayRouter(t, fake.NewSimpleClientset())
	instances := []v1.RelayInstanceStatus{
		{ID: "oci-1", Provider: "oci", Region: "us-ashburn-1", State: "healthy", Healthy: true, WgIP: "10.42.42.2", PublicIP: "1.2.3.4"},
		{ID: "gcp-1", Provider: "gcp", Region: "us-west1", State: "healthy", Healthy: true, WgIP: "10.42.42.3", PublicIP: "5.6.7.8"},
	}
	overrideList(relayMock, &v1.InferenceRelayList{Items: []v1.InferenceRelay{*makeRelayCR("relay-fleet", instances, 2)}}, nil)

	w := doRelayRequest(r, "GET", "/api/v1/admin/relay/status")

	require.Equal(t, http.StatusOK, w.Code)
	var resp statusResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.True(t, resp.Deployed)
	assert.Equal(t, "healthy", resp.Overall)
	assert.Equal(t, 2, resp.HealthyReplicas)
	assert.Equal(t, 2, resp.TotalReplicas)
	require.Len(t, resp.Instances, 2)
	assert.Equal(t, "oci-1", resp.Instances[0].ID)
	assert.Equal(t, "gcp-1", resp.Instances[1].ID)
}

func TestRelayStatus_IncludesShapeFromSpec(t *testing.T) {
	r, _, relayMock := setupRelayRouter(t, fake.NewSimpleClientset())
	instances := []v1.RelayInstanceStatus{
		{ID: "oci-1", Provider: "oci", Region: "us-ashburn-1", State: "healthy", Healthy: true},
	}
	overrideList(relayMock, &v1.InferenceRelayList{Items: []v1.InferenceRelay{*makeRelayCR("relay-fleet", instances, 1)}}, nil)

	w := doRelayRequest(r, "GET", "/api/v1/admin/relay/status")

	require.Equal(t, http.StatusOK, w.Code)
	var resp statusResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Instances, 1)
	assert.Equal(t, "VM.Standard.A1.Flex", resp.Instances[0].Shape)
}

func TestRelayStatus_DegradedFleet(t *testing.T) {
	r, _, relayMock := setupRelayRouter(t, fake.NewSimpleClientset())
	instances := []v1.RelayInstanceStatus{
		{ID: "oci-1", Provider: "oci", State: "healthy", Healthy: true},
		{ID: "gcp-1", Provider: "gcp", State: "unhealthy", Healthy: false},
	}
	overrideList(relayMock, &v1.InferenceRelayList{Items: []v1.InferenceRelay{*makeRelayCR("relay-fleet", instances, 1)}}, nil)

	w := doRelayRequest(r, "GET", "/api/v1/admin/relay/status")

	require.Equal(t, http.StatusOK, w.Code)
	var resp statusResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "degraded", resp.Overall)
	assert.Equal(t, 1, resp.HealthyReplicas)
	assert.Equal(t, 2, resp.TotalReplicas)
}

func TestRelayStatus_AllUnhealthy(t *testing.T) {
	r, _, relayMock := setupRelayRouter(t, fake.NewSimpleClientset())
	instances := []v1.RelayInstanceStatus{
		{ID: "oci-1", Provider: "oci", State: "unhealthy", Healthy: false},
		{ID: "gcp-1", Provider: "gcp", State: "unhealthy", Healthy: false},
	}
	overrideList(relayMock, &v1.InferenceRelayList{Items: []v1.InferenceRelay{*makeRelayCR("relay-fleet", instances, 0)}}, nil)

	w := doRelayRequest(r, "GET", "/api/v1/admin/relay/status")

	require.Equal(t, http.StatusOK, w.Code)
	var resp statusResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "unhealthy", resp.Overall)
}

func TestRelayStatus_FallbackCondition(t *testing.T) {
	r, _, relayMock := setupRelayRouter(t, fake.NewSimpleClientset())
	relay := makeRelayCRWithConditions("relay-fleet", []metav1.Condition{
		{Type: "FallbackActive", Status: metav1.ConditionTrue, Reason: "AllRelaysDown"},
	})
	overrideList(relayMock, &v1.InferenceRelayList{Items: []v1.InferenceRelay{*relay}}, nil)

	w := doRelayRequest(r, "GET", "/api/v1/admin/relay/status")

	require.Equal(t, http.StatusOK, w.Code)
	var resp statusResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.True(t, resp.FallbackActive)
}

func TestRelayStatus_AlertsFiring(t *testing.T) {
	r, _, relayMock := setupRelayRouter(t, fake.NewSimpleClientset())
	instances := []v1.RelayInstanceStatus{
		{ID: "oci-1", Provider: "oci", State: "unhealthy", Healthy: false},
	}
	overrideList(relayMock, &v1.InferenceRelayList{Items: []v1.InferenceRelay{*makeRelayCR("relay-fleet", instances, 0)}}, nil)

	w := doRelayRequest(r, "GET", "/api/v1/admin/relay/status")

	require.Equal(t, http.StatusOK, w.Code)
	var resp statusResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.NotEmpty(t, resp.Alerts)
	assert.True(t, resp.Alerts[0].Firing)
	assert.True(t, resp.Alerts[1].Firing)
}

func TestRelayStatus_ProvisioningFailed(t *testing.T) {
	r, _, relayMock := setupRelayRouter(t, fake.NewSimpleClientset())
	instances := []v1.RelayInstanceStatus{
		{ID: "oci-1", Provider: "oci", State: "provisioning-failed", Healthy: false, LastProvisionError: "quota exceeded"},
	}
	overrideList(relayMock, &v1.InferenceRelayList{Items: []v1.InferenceRelay{*makeRelayCR("relay-fleet", instances, 0)}}, nil)

	w := doRelayRequest(r, "GET", "/api/v1/admin/relay/status")

	require.Equal(t, http.StatusOK, w.Code)
	var resp statusResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Instances, 1)
	assert.Contains(t, resp.Instances[0].LastProvisionError, "quota exceeded")
}

func TestRelayStatus_ListError_500(t *testing.T) {
	r, _, relayMock := setupRelayRouter(t, fake.NewSimpleClientset())
	overrideList(relayMock, nil, testError("API server unreachable"))

	w := doRelayRequest(r, "GET", "/api/v1/admin/relay/status")

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

// ─── US-43.5: SaveOCICreds tests ────────────────────────────────────────────

func TestRelayOCICreds_Create_Success(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	r, _, _ := setupRelayRouter(t, clientset)

	body := `{"tenancy":"ocid1.tenancy.oc1..aaa","user":"ocid1.user.oc1..bbb","fingerprint":"aa:bb:cc","key":"-----BEGIN RSA PRIVATE KEY-----\nfake\n-----END RSA PRIVATE KEY-----\n","region":"us-ashburn-1"}`
	w := doRelayRequest(r, "POST", "/api/v1/admin/relay/oci-creds", body)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	secret, err := clientset.CoreV1().Secrets(testNamespace).Get(context.Background(), "oci-credentials", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, "ocid1.tenancy.oc1..aaa", string(secret.Data["tenancy"]))
	assert.Equal(t, "ocid1.user.oc1..bbb", string(secret.Data["user"]))
	assert.Equal(t, "aa:bb:cc", string(secret.Data["fingerprint"]))
	assert.Equal(t, "us-ashburn-1", string(secret.Data["region"]))
}

func TestRelayOCICreds_Update_Success(t *testing.T) {
	clientset := fake.NewSimpleClientset(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "oci-credentials", Namespace: testNamespace, ResourceVersion: "1"},
	})
	r, _, _ := setupRelayRouter(t, clientset)

	body := `{"tenancy":"new-tenancy","user":"new-user","fingerprint":"new:fp","key":"new-key","region":"us-phoenix-1"}`
	w := doRelayRequest(r, "POST", "/api/v1/admin/relay/oci-creds", body)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	secret, err := clientset.CoreV1().Secrets(testNamespace).Get(context.Background(), "oci-credentials", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, "new-tenancy", string(secret.Data["tenancy"]))
}

func TestRelayOCICreds_MissingFields_400(t *testing.T) {
	r, _, _ := setupRelayRouter(t, fake.NewSimpleClientset())

	w := doRelayRequest(r, "POST", "/api/v1/admin/relay/oci-creds", `{"tenancy":"x"}`)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// ─── GCP credentials tests ──────────────────────────────────────────────────

func TestRelayGCPCreds_Create_Success(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	r, _, _ := setupRelayRouter(t, clientset)

	body := `{"serviceAccountJson":"{\"type\":\"service_account\",\"project_id\":\"my-project\"}"}`
	w := doRelayRequest(r, "POST", "/api/v1/admin/relay/gcp-creds", body)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	secret, err := clientset.CoreV1().Secrets(testNamespace).Get(context.Background(), "gcp-credentials", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Contains(t, string(secret.Data["service-account-json"]), "service_account")
}

func TestRelayGCPCreds_MissingFields_400(t *testing.T) {
	r, _, _ := setupRelayRouter(t, fake.NewSimpleClientset())

	w := doRelayRequest(r, "POST", "/api/v1/admin/relay/gcp-creds", `{}`)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// ─── AWS credentials tests ──────────────────────────────────────────────────

func TestRelayAWSCreds_Create_Success(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	r, _, _ := setupRelayRouter(t, clientset)

	body := `{"accessKeyId":"AKIATEST","secretAccessKey":"secret123","region":"us-east-1"}`
	w := doRelayRequest(r, "POST", "/api/v1/admin/relay/aws-creds", body)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	secret, err := clientset.CoreV1().Secrets(testNamespace).Get(context.Background(), "aws-relay-irwa", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, "AKIATEST", string(secret.Data["accessKeyId"]))
	assert.Equal(t, "secret123", string(secret.Data["secretAccessKey"]))
	assert.Equal(t, "us-east-1", string(secret.Data["region"]))
}

func TestRelayAWSCreds_MissingFields_400(t *testing.T) {
	r, _, _ := setupRelayRouter(t, fake.NewSimpleClientset())

	tests := []struct {
		name string
		body string
	}{
		{"missing accessKeyId", `{"secretAccessKey":"s","region":"us-east-1"}`},
		{"missing secretAccessKey", `{"accessKeyId":"a","region":"us-east-1"}`},
		{"missing region", `{"accessKeyId":"a","secretAccessKey":"s"}`},
		{"empty body", `{}`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := doRelayRequest(r, "POST", "/api/v1/admin/relay/aws-creds", tc.body)
			assert.Equal(t, http.StatusBadRequest, w.Code)
		})
	}
}

func TestRelaySetup_AWSSecretExists_Configured(t *testing.T) {
	clientset := fake.NewSimpleClientset(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "aws-relay-irwa", Namespace: testNamespace},
	})
	r, _, _ := setupRelayRouter(t, clientset)

	w := doRelayRequest(r, "GET", "/api/v1/admin/relay/setup")

	require.Equal(t, http.StatusOK, w.Code)
	var resp setupResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.True(t, resp.AWSConfigured)
}

// ─── US-43.6: Deploy tests ──────────────────────────────────────────────────

func TestRelayDeploy_Create_Success(t *testing.T) {
	r, _, relayMock := setupRelayRouter(t, fake.NewSimpleClientset())
	relayMock.On("Get", mock.Anything, "relay-fleet", mock.Anything).Return(nil, notFoundError()).Maybe()
	relayMock.On("Create", mock.Anything, mock.Anything).Return(makeRelayCR("relay-fleet", nil, 0), nil).Maybe()

	body := `{"upstreamURL":"https://opencode.ai/zen/v1","routerEndpoint":"relay-gw.example.com:51820","providers":["oci","gcp"]}`
	w := doRelayRequest(r, "POST", "/api/v1/admin/relay/deploy", body)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.True(t, resp["deployed"].(bool))
}

func TestRelayDeploy_Update_Existing(t *testing.T) {
	r, _, relayMock := setupRelayRouter(t, fake.NewSimpleClientset())
	existing := makeRelayCR("relay-fleet", nil, 0)
	existing.ResourceVersion = "42"
	relayMock.On("Get", mock.Anything, "relay-fleet", mock.Anything).Return(existing, nil).Maybe()
	relayMock.On("Update", mock.Anything, mock.Anything).Return(existing, nil).Maybe()

	body := `{"upstreamURL":"https://new.example.com","routerEndpoint":"new-gw.example.com:51820","providers":["oci"]}`
	w := doRelayRequest(r, "POST", "/api/v1/admin/relay/deploy", body)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
}

func TestRelayDeploy_AcceptsAWS_Success(t *testing.T) {
	r, _, relayMock := setupRelayRouter(t, fake.NewSimpleClientset())
	relayMock.On("Get", mock.Anything, "relay-fleet", mock.Anything).Return(nil, notFoundError()).Maybe()
	relayMock.On("Create", mock.Anything, mock.Anything).Return(makeRelayCR("relay-fleet", nil, 0), nil).Maybe()

	body := `{"upstreamURL":"https://example.com","routerEndpoint":"gw:51820","providers":["aws","oci"]}`
	w := doRelayRequest(r, "POST", "/api/v1/admin/relay/deploy", body)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
}

func TestRelayDeploy_MissingFields_400(t *testing.T) {
	r, _, _ := setupRelayRouter(t, fake.NewSimpleClientset())

	tests := []struct {
		name string
		body string
	}{
		{"missing routerEndpoint", `{"upstreamURL":"https://x.com","providers":["oci"]}`},
		{"empty providers", `{"upstreamURL":"https://x.com","routerEndpoint":"gw:51820","providers":[]}`},
		{"empty body", `{}`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := doRelayRequest(r, "POST", "/api/v1/admin/relay/deploy", tc.body)
			assert.Equal(t, http.StatusBadRequest, w.Code)
		})
	}
}

func TestRelayDeploy_Defaults_UpstreamURL(t *testing.T) {
	r, _, relayMock := setupRelayRouter(t, fake.NewSimpleClientset())
	relayMock.On("Get", mock.Anything, "relay-fleet", mock.Anything).Return(nil, notFoundError()).Maybe()
	relayMock.On("Create", mock.Anything, mock.MatchedBy(func(r *v1.InferenceRelay) bool {
		return r.Spec.UpstreamURL == "https://opencode.ai/zen/v1"
	})).Return(makeRelayCR("relay-fleet", nil, 0), nil)

	body := `{"routerEndpoint":"gw:51820","providers":["oci"]}`
	w := doRelayRequest(r, "POST", "/api/v1/admin/relay/deploy", body)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
}

func TestRelayDeploy_OCIOnly_Success(t *testing.T) {
	r, _, relayMock := setupRelayRouter(t, fake.NewSimpleClientset())
	relayMock.On("Get", mock.Anything, "relay-fleet", mock.Anything).Return(nil, notFoundError()).Maybe()
	relayMock.On("Create", mock.Anything, mock.Anything).Return(makeRelayCR("relay-fleet", nil, 0), nil).Maybe()

	body := `{"upstreamURL":"https://opencode.ai/zen/v1","routerEndpoint":"relay-gw.example.com:51820","providers":["oci"]}`
	w := doRelayRequest(r, "POST", "/api/v1/admin/relay/deploy", body)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
}

func TestRelayDeploy_GCPOnly_Success(t *testing.T) {
	r, _, relayMock := setupRelayRouter(t, fake.NewSimpleClientset())
	relayMock.On("Get", mock.Anything, "relay-fleet", mock.Anything).Return(nil, notFoundError()).Maybe()
	relayMock.On("Create", mock.Anything, mock.Anything).Return(makeRelayCR("relay-fleet", nil, 0), nil).Maybe()

	body := `{"upstreamURL":"https://opencode.ai/zen/v1","routerEndpoint":"relay-gw.example.com:51820","providers":["gcp"]}`
	w := doRelayRequest(r, "POST", "/api/v1/admin/relay/deploy", body)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
}

// ─── US-43.7: Rotate tests ──────────────────────────────────────────────────

func TestRelayRotate_Success(t *testing.T) {
	r, _, relayMock := setupRelayRouter(t, fake.NewSimpleClientset())
	existing := makeRelayCR("relay-fleet", nil, 1)
	relayMock.On("Get", mock.Anything, "relay-fleet", mock.Anything).Return(existing, nil).Maybe()
	relayMock.On("Update", mock.Anything, mock.Anything).Return(existing, nil).Maybe()

	w := doRelayRequest(r, "POST", "/api/v1/admin/relay/rotate/oci-1")

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "oci-1", resp["rotating"])
}

func TestRelayRotate_NotFound_404(t *testing.T) {
	r, _, relayMock := setupRelayRouter(t, fake.NewSimpleClientset())
	relayMock.On("Get", mock.Anything, "relay-fleet", mock.Anything).Return(nil, notFoundError()).Maybe()

	w := doRelayRequest(r, "POST", "/api/v1/admin/relay/rotate/oci-1")

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// ─── US-43.7: Pause/Resume tests ────────────────────────────────────────────

func TestRelayPause_Success(t *testing.T) {
	r, _, relayMock := setupRelayRouter(t, fake.NewSimpleClientset())
	existing := makeRelayCR("relay-fleet", nil, 1)
	relayMock.On("Get", mock.Anything, "relay-fleet", mock.Anything).Return(existing, nil).Maybe()
	relayMock.On("Update", mock.Anything, mock.Anything).Return(existing, nil).Maybe()

	w := doRelayRequest(r, "POST", "/api/v1/admin/relay/pause")

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.True(t, resp["paused"].(bool))
}

func TestRelayPause_NotFound_404(t *testing.T) {
	r, _, relayMock := setupRelayRouter(t, fake.NewSimpleClientset())
	relayMock.On("Get", mock.Anything, "relay-fleet", mock.Anything).Return(nil, notFoundError()).Maybe()

	w := doRelayRequest(r, "POST", "/api/v1/admin/relay/pause")

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestRelayResume_Success(t *testing.T) {
	r, _, relayMock := setupRelayRouter(t, fake.NewSimpleClientset())
	existing := makeRelayCR("relay-fleet", nil, 1)
	existing.Annotations = map[string]string{"relay.llmsafespace.dev/paused": "true"}
	relayMock.On("Get", mock.Anything, "relay-fleet", mock.Anything).Return(existing, nil).Maybe()
	relayMock.On("Update", mock.Anything, mock.Anything).Return(existing, nil).Maybe()

	w := doRelayRequest(r, "POST", "/api/v1/admin/relay/resume")

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.False(t, resp["paused"].(bool))
}

func TestRelayResume_NotFound_404(t *testing.T) {
	r, _, relayMock := setupRelayRouter(t, fake.NewSimpleClientset())
	relayMock.On("Get", mock.Anything, "relay-fleet", mock.Anything).Return(nil, notFoundError()).Maybe()

	w := doRelayRequest(r, "POST", "/api/v1/admin/relay/resume")

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// ─── Regression tests for IsNotFound error handling ─────────────────────────

func TestRelayRotate_NetworkError_500(t *testing.T) {
	r, _, relayMock := setupRelayRouter(t, fake.NewSimpleClientset())
	relayMock.On("Get", mock.Anything, "relay-fleet", mock.Anything).Return(nil, testError("connection refused")).Maybe()

	w := doRelayRequest(r, "POST", "/api/v1/admin/relay/rotate/oci-1")
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestRelayPause_NetworkError_500(t *testing.T) {
	r, _, relayMock := setupRelayRouter(t, fake.NewSimpleClientset())
	relayMock.On("Get", mock.Anything, "relay-fleet", mock.Anything).Return(nil, testError("timeout")).Maybe()

	w := doRelayRequest(r, "POST", "/api/v1/admin/relay/pause")
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestRelayResume_NetworkError_500(t *testing.T) {
	r, _, relayMock := setupRelayRouter(t, fake.NewSimpleClientset())
	relayMock.On("Get", mock.Anything, "relay-fleet", mock.Anything).Return(nil, testError("timeout")).Maybe()

	w := doRelayRequest(r, "POST", "/api/v1/admin/relay/resume")
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestRelayDeploy_NetworkError_500(t *testing.T) {
	r, _, relayMock := setupRelayRouter(t, fake.NewSimpleClientset())
	relayMock.On("Get", mock.Anything, "relay-fleet", mock.Anything).Return(nil, testError("connection refused")).Maybe()

	body := `{"upstreamURL":"https://example.com","routerEndpoint":"gw:51820","providers":["oci"]}`
	w := doRelayRequest(r, "POST", "/api/v1/admin/relay/deploy", body)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestRelaySetup_NoRouter_StillOK(t *testing.T) {
	// When router deployment doesn't exist, setup should return 200 with
	// routerDeployed=false (not a 500 error).
	r, _, _ := setupRelayRouter(t, fake.NewSimpleClientset())
	w := doRelayRequest(r, "GET", "/api/v1/admin/relay/setup")
	assert.Equal(t, http.StatusOK, w.Code)
	var resp setupResponse
	json.Unmarshal(w.Body.Bytes(), &resp)
	assert.False(t, resp.RouterDeployed)
}

func TestRelayOCICreds_CreateThenUpdate_Success(t *testing.T) {
	// Verify that upsertSecret handles both create (first call) and
	// update (second call) correctly.
	clientset := fake.NewSimpleClientset()
	r, _, _ := setupRelayRouter(t, clientset)

	body := `{"tenancy":"t","user":"u","fingerprint":"f","key":"k","region":"us-ashburn-1"}`

	// First call: creates the secret
	w := doRelayRequest(r, "POST", "/api/v1/admin/relay/oci-creds", body)
	require.Equal(t, http.StatusOK, w.Code)

	// Second call: updates the existing secret
	w = doRelayRequest(r, "POST", "/api/v1/admin/relay/oci-creds", body)
	require.Equal(t, http.StatusOK, w.Code)
}

// ─── Metric parsing tests ───────────────────────────────────────────────────

func TestParseRouterMetrics_BasicMetrics(t *testing.T) {
	raw := `# HELP relay_router_active_streams Current active streams
# TYPE relay_router_active_streams gauge
relay_router_active_streams 5
relay_router_requests_total{provider="oci"} 12847
relay_router_requests_total{provider="gcp"} 0
relay_router_requests_429_total{provider="oci"} 3
relay_router_streams{provider="oci"} 3
`
	data := &routerMetricsData{
		requestsByProvider:    make(map[string]int64),
		requests429ByProvider: make(map[string]int64),
		streamsByProvider:     make(map[string]int64),
	}
	parseRouterMetrics(raw, data)

	assert.Equal(t, int64(5), data.activeStreams)
	assert.Equal(t, int64(12847), data.requestsByProvider["oci"])
	assert.Equal(t, int64(0), data.requestsByProvider["gcp"])
	assert.Equal(t, int64(3), data.requests429ByProvider["oci"])
	assert.Equal(t, int64(3), data.streamsByProvider["oci"])
}

func TestParseRouterMetrics_EmptyInput(t *testing.T) {
	data := &routerMetricsData{
		requestsByProvider:    make(map[string]int64),
		requests429ByProvider: make(map[string]int64),
		streamsByProvider:     make(map[string]int64),
	}
	parseRouterMetrics("", data)
	assert.Equal(t, int64(0), data.activeStreams)
	assert.Empty(t, data.requestsByProvider)
}

func TestEgressLimitForProvider(t *testing.T) {
	assert.Equal(t, int64(100*1024*1024*1024), egressLimitForProvider("aws"))
	assert.Equal(t, int64(10*1024*1024*1024*1024), egressLimitForProvider("oci"))
	assert.Equal(t, int64(1*1024*1024*1024), egressLimitForProvider("gcp"))
	assert.Equal(t, int64(1*1024*1024*1024), egressLimitForProvider("unknown"))
}

func TestComputeCost(t *testing.T) {
	assert.Equal(t, int64(700), computeCost("aws", true).MonthlyEstimate)
	assert.Equal(t, int64(0), computeCost("aws", false).MonthlyEstimate)
	assert.Equal(t, int64(0), computeCost("oci", true).MonthlyEstimate)
	assert.Equal(t, int64(0), computeCost("gcp", true).MonthlyEstimate)
}

func TestBuildAlerts_AllHealthy(t *testing.T) {
	alerts := buildAlerts(2, 2)
	assert.False(t, alerts[0].Firing)
	assert.False(t, alerts[1].Firing)
}

func TestBuildAlerts_AllDown(t *testing.T) {
	alerts := buildAlerts(0, 2)
	assert.True(t, alerts[0].Firing)
	assert.True(t, alerts[1].Firing)
}

func TestBuildAlerts_Partial(t *testing.T) {
	alerts := buildAlerts(1, 2)
	assert.True(t, alerts[0].Firing)
	assert.False(t, alerts[1].Firing)
}

func TestExtractLabel(t *testing.T) {
	assert.Equal(t, "oci", extractLabel(`relay_router_requests_total{provider="oci"} 12847`, "provider"))
	assert.Equal(t, "", extractLabel("no labels here", "provider"))
}

func TestParseInt(t *testing.T) {
	var val int64
	parseInt("12345", &val)
	assert.Equal(t, int64(12345), val)

	parseInt("12.34", &val)
	assert.Equal(t, int64(12), val)

	parseInt("", &val)
	assert.Equal(t, int64(0), val)
}

// ─── Router metrics scraping via mock HTTP server ───────────────────────────

func TestRelayStatus_ScrapesRouterMetrics(t *testing.T) {
	metricsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("relay_router_active_streams 7\nrelay_router_requests_total{provider=\"oci\"} 999\n"))
	}))
	defer metricsServer.Close()

	clientset := fake.NewSimpleClientset()
	llmMock := k8smocks.NewMockLLMSafespaceV1Interface()
	relayMock := k8smocks.NewMockInferenceRelayInterface()
	llmMock.On("InferenceRelays").Return(relayMock).Maybe()
	relayMock.On("List", mock.Anything, mock.Anything).Return(
		&v1.InferenceRelayList{Items: []v1.InferenceRelay{*makeRelayCR("relay-fleet",
			[]v1.RelayInstanceStatus{{ID: "oci-1", Provider: "oci", State: "healthy", Healthy: true}}, 1)}}, nil,
	).Maybe()

	h := NewRelayAdminHandler(clientset, llmMock, testNamespace, testNamespace, metricsServer.URL)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/test", h.GetStatus)

	req, _ := http.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var resp statusResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, int64(7), resp.ActiveStreams)
	require.Len(t, resp.Instances, 1)
	assert.Equal(t, int64(999), resp.Instances[0].Metrics.RequestsToday)
}

func TestRelayStatus_RouterUnreachable_GracefulDegrade(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	llmMock := k8smocks.NewMockLLMSafespaceV1Interface()
	relayMock := k8smocks.NewMockInferenceRelayInterface()
	llmMock.On("InferenceRelays").Return(relayMock).Maybe()
	relayMock.On("List", mock.Anything, mock.Anything).Return(
		&v1.InferenceRelayList{Items: []v1.InferenceRelay{*makeRelayCR("relay-fleet",
			[]v1.RelayInstanceStatus{{ID: "oci-1", Provider: "oci", State: "healthy", Healthy: true}}, 1)}}, nil,
	).Maybe()

	h := NewRelayAdminHandler(clientset, llmMock, testNamespace, testNamespace, "http://127.0.0.1:1")

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/test", h.GetStatus)

	req, _ := http.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var resp statusResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, int64(0), resp.ActiveStreams)
}

// ─── E2E: Full relay admin lifecycle ────────────────────────────────────────

func TestRelayAdmin_E2E_FullLifecycle(t *testing.T) {
	clientset := fake.NewSimpleClientset()

	gin.SetMode(gin.TestMode)
	llmMock := k8smocks.NewMockLLMSafespaceV1Interface()
	relayMock := k8smocks.NewMockInferenceRelayInterface()
	llmMock.On("InferenceRelays").Return(relayMock).Maybe()

	instances := []v1.RelayInstanceStatus{
		{ID: "oci-1", Provider: "oci", State: "healthy", Healthy: true},
		{ID: "gcp-1", Provider: "gcp", State: "healthy", Healthy: true},
	}
	deployedCR := makeRelayCR("relay-fleet", instances, 2)
	relayMock.On("List", mock.Anything, mock.Anything).Return(
		&v1.InferenceRelayList{Items: []v1.InferenceRelay{*deployedCR}}, nil).Maybe()
	relayMock.On("Get", mock.Anything, "relay-fleet", mock.Anything).Return(deployedCR, nil).Maybe()
	relayMock.On("Update", mock.Anything, mock.Anything).Return(deployedCR, nil).Maybe()

	h := NewRelayAdminHandler(clientset, llmMock, testNamespace, testNamespace, "http://relay-router.test:8080")

	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("userID", "admin-1")
		c.Set("userRole", "admin")
		c.Next()
	})
	g := r.Group("/api/v1/admin/relay")
	g.GET("/setup", h.GetSetup)
	g.GET("/status", h.GetStatus)
	g.POST("/oci-creds", h.SaveOCICreds)
	g.POST("/gcp-creds", h.SaveGCPCreds)
	g.POST("/deploy", h.Deploy)
	g.POST("/rotate/:id", h.Rotate)
	g.POST("/pause", h.Pause)
	g.POST("/resume", h.Resume)

	// Step 1: Setup — nothing configured
	w := doRelayRequest(r, "GET", "/api/v1/admin/relay/setup")
	require.Equal(t, http.StatusOK, w.Code)
	var setupResp setupResponse
	json.Unmarshal(w.Body.Bytes(), &setupResp)
	assert.False(t, setupResp.OCIConfigured)
	assert.False(t, setupResp.GCPConfigured)

	// Step 2: Save OCI creds
	w = doRelayRequest(r, "POST", "/api/v1/admin/relay/oci-creds",
		`{"tenancy":"t","user":"u","fingerprint":"f","key":"k","region":"us-ashburn-1"}`)
	require.Equal(t, http.StatusOK, w.Code)

	// Step 3: Save GCP creds
	w = doRelayRequest(r, "POST", "/api/v1/admin/relay/gcp-creds",
		`{"serviceAccountJson":"{\"type\":\"service_account\"}"}`)
	require.Equal(t, http.StatusOK, w.Code)

	// Step 4: Setup shows both configured
	w = doRelayRequest(r, "GET", "/api/v1/admin/relay/setup")
	json.Unmarshal(w.Body.Bytes(), &setupResp)
	assert.True(t, setupResp.OCIConfigured)
	assert.True(t, setupResp.GCPConfigured)

	// Step 5: Status — fleet is deployed
	w = doRelayRequest(r, "GET", "/api/v1/admin/relay/status")
	require.Equal(t, http.StatusOK, w.Code)
	var status statusResponse
	json.Unmarshal(w.Body.Bytes(), &status)
	assert.True(t, status.Deployed)
	assert.Equal(t, "healthy", status.Overall)
	assert.Len(t, status.Instances, 2)

	// Step 6: Rotate
	w = doRelayRequest(r, "POST", "/api/v1/admin/relay/rotate/oci-1")
	require.Equal(t, http.StatusOK, w.Code)

	// Step 7: Pause
	w = doRelayRequest(r, "POST", "/api/v1/admin/relay/pause")
	require.Equal(t, http.StatusOK, w.Code)

	// Step 8: Resume
	w = doRelayRequest(r, "POST", "/api/v1/admin/relay/resume")
	require.Equal(t, http.StatusOK, w.Code)

	// Verify secrets persisted
	ociSecret, err := clientset.CoreV1().Secrets(testNamespace).Get(context.Background(), "oci-credentials", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, "t", string(ociSecret.Data["tenancy"]))

	gcpSecret, err := clientset.CoreV1().Secrets(testNamespace).Get(context.Background(), "gcp-credentials", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Contains(t, string(gcpSecret.Data["service-account-json"]), "service_account")
}
