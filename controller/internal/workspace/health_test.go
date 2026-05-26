package workspace

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/lenaxia/llmsafespace/pkg/agent/opencode"
	"github.com/lenaxia/llmsafespace/pkg/agentd"
	v1 "github.com/lenaxia/llmsafespace/pkg/apis/llmsafespace/v1"
)

func TestSetCondition_AppendsNew(t *testing.T) {
	r := reconcilerFor(t)
	ws := makeWorkspace("ws-cond", "default", v1.WorkspacePhaseActive)
	r.setCondition(ws, v1.WorkspaceConditionCredentialsAvailable, "True", v1.ReasonCredentialsValid, "")
	require.Len(t, ws.Status.Conditions, 1)
	assert.Equal(t, v1.WorkspaceConditionCredentialsAvailable, ws.Status.Conditions[0].Type)
	assert.Equal(t, "True", ws.Status.Conditions[0].Status)
	assert.Equal(t, v1.ReasonCredentialsValid, ws.Status.Conditions[0].Reason)
	assert.False(t, ws.Status.Conditions[0].LastTransitionTime.IsZero())
}

func TestSetCondition_UpdatesExisting(t *testing.T) {
	r := reconcilerFor(t)
	ws := makeWorkspace("ws-cond", "default", v1.WorkspacePhaseActive)
	r.setCondition(ws, v1.WorkspaceConditionCredentialsAvailable, "True", v1.ReasonCredentialsValid, "")
	firstTransition := ws.Status.Conditions[0].LastTransitionTime

	time.Sleep(10 * time.Millisecond)
	r.setCondition(ws, v1.WorkspaceConditionCredentialsAvailable, "False", v1.ReasonCredentialEmpty, "empty")
	require.Len(t, ws.Status.Conditions, 1)
	assert.Equal(t, "False", ws.Status.Conditions[0].Status)
	assert.Equal(t, v1.ReasonCredentialEmpty, ws.Status.Conditions[0].Reason)
	assert.True(t, ws.Status.Conditions[0].LastTransitionTime.After(firstTransition.Time))
}

func TestSetCondition_SameStatusAndReason_NoTransitionTimeUpdate(t *testing.T) {
	r := reconcilerFor(t)
	ws := makeWorkspace("ws-cond", "default", v1.WorkspacePhaseActive)
	r.setCondition(ws, v1.WorkspaceConditionCredentialsAvailable, "True", v1.ReasonCredentialsValid, "msg1")
	firstTransition := ws.Status.Conditions[0].LastTransitionTime

	time.Sleep(10 * time.Millisecond)
	r.setCondition(ws, v1.WorkspaceConditionCredentialsAvailable, "True", v1.ReasonCredentialsValid, "msg2")
	require.Len(t, ws.Status.Conditions, 1)
	assert.Equal(t, firstTransition, ws.Status.Conditions[0].LastTransitionTime)
	assert.Equal(t, "msg2", ws.Status.Conditions[0].Message)
}

func TestCheckCredentialState_SecretExistsValid(t *testing.T) {
	opencode.Register()
	r := reconcilerFor(t)
	ws := makeWorkspace("ws-cred-valid", "default", v1.WorkspacePhaseActive)
	credSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "workspace-creds-ws-cred-valid", Namespace: "default"},
		Data:       map[string][]byte{"provider-config": []byte(`{"apiKey":"test"}`)},
	}
	require.NoError(t, r.Create(context.Background(), credSecret))

	r.checkCredentialState(context.Background(), ws)

	found := false
	for _, c := range ws.Status.Conditions {
		if c.Type == v1.WorkspaceConditionCredentialsAvailable {
			found = true
			assert.Equal(t, "True", c.Status)
			assert.Equal(t, v1.ReasonCredentialsValid, c.Reason)
		}
	}
	assert.True(t, found, "CredentialsAvailable condition should be set")
}

func TestCheckCredentialState_SecretNotFound(t *testing.T) {
	opencode.Register()
	r := reconcilerFor(t)
	ws := makeWorkspace("ws-cred-missing", "default", v1.WorkspacePhaseActive)

	r.checkCredentialState(context.Background(), ws)

	found := false
	for _, c := range ws.Status.Conditions {
		if c.Type == v1.WorkspaceConditionCredentialsAvailable {
			found = true
			assert.Equal(t, "False", c.Status)
			assert.Equal(t, v1.ReasonCredentialSecretNotFound, c.Reason)
		}
	}
	assert.True(t, found)
}

func TestCheckCredentialState_SecretEmptyData(t *testing.T) {
	opencode.Register()
	r := reconcilerFor(t)
	ws := makeWorkspace("ws-cred-empty", "default", v1.WorkspacePhaseActive)
	credSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "workspace-creds-ws-cred-empty", Namespace: "default"},
		Data:       map[string][]byte{"provider-config": []byte(`{}`)},
	}
	require.NoError(t, r.Create(context.Background(), credSecret))

	r.checkCredentialState(context.Background(), ws)

	found := false
	for _, c := range ws.Status.Conditions {
		if c.Type == v1.WorkspaceConditionCredentialsAvailable {
			found = true
			assert.Equal(t, "False", c.Status)
			assert.Equal(t, v1.ReasonCredentialEmpty, c.Reason)
		}
	}
	assert.True(t, found)
}

func TestCheckCredentialState_SecretInvalidJSON(t *testing.T) {
	opencode.Register()
	r := reconcilerFor(t)
	ws := makeWorkspace("ws-cred-invalid", "default", v1.WorkspacePhaseActive)
	credSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "workspace-creds-ws-cred-invalid", Namespace: "default"},
		Data:       map[string][]byte{"provider-config": []byte(`not json`)},
	}
	require.NoError(t, r.Create(context.Background(), credSecret))

	r.checkCredentialState(context.Background(), ws)

	found := false
	for _, c := range ws.Status.Conditions {
		if c.Type == v1.WorkspaceConditionCredentialsAvailable {
			found = true
			assert.Equal(t, "False", c.Status)
			assert.Equal(t, v1.ReasonCredentialInvalid, c.Reason)
		}
	}
	assert.True(t, found)
}

func TestCheckCredentialState_DoesNotChangePhase(t *testing.T) {
	opencode.Register()
	r := reconcilerFor(t)
	ws := makeWorkspace("ws-cred-phase", "default", v1.WorkspacePhaseActive)

	r.checkCredentialState(context.Background(), ws)
	assert.Equal(t, v1.WorkspacePhaseActive, ws.Status.Phase, "credential check should not change phase")
}

func TestShouldRunHealthCheck_NilLastCheck(t *testing.T) {
	r := reconcilerFor(t)
	ws := makeWorkspace("ws-hc", "default", v1.WorkspacePhaseActive)
	past := metav1.NewTime(time.Now().Add(-10 * time.Minute))
	ws.Status.StartTime = &past
	assert.True(t, r.shouldRunHealthCheck(ws))
}

func TestShouldRunHealthCheck_WithinGracePeriod(t *testing.T) {
	r := reconcilerFor(t)
	ws := makeWorkspace("ws-hc", "default", v1.WorkspacePhaseActive)
	now := metav1.Now()
	ws.Status.StartTime = &now
	assert.False(t, r.shouldRunHealthCheck(ws))
}

func TestShouldRunHealthCheck_WithinInterval(t *testing.T) {
	r := reconcilerFor(t)
	ws := makeWorkspace("ws-hc", "default", v1.WorkspacePhaseActive)
	past := metav1.NewTime(time.Now().Add(-5 * time.Minute))
	ws.Status.StartTime = &past
	recent := metav1.NewTime(time.Now().Add(-1 * time.Minute))
	ws.Status.LastHealthCheckAt = &recent
	assert.False(t, r.shouldRunHealthCheck(ws))
}

func TestShouldRunHealthCheck_BackoffAfterFailures(t *testing.T) {
	r := reconcilerFor(t)
	ws := makeWorkspace("ws-hc", "default", v1.WorkspacePhaseActive)
	past := metav1.NewTime(time.Now().Add(-30 * time.Minute))
	ws.Status.StartTime = &past
	ws.Status.ConsecutiveHealthFailures = 3

	withinBackoff := metav1.NewTime(time.Now().Add(-10 * time.Minute))
	ws.Status.LastHealthCheckAt = &withinBackoff
	assert.False(t, r.shouldRunHealthCheck(ws), "should not run within backoff interval")

	afterBackoff := metav1.NewTime(time.Now().Add(-16 * time.Minute))
	ws.Status.LastHealthCheckAt = &afterBackoff
	assert.True(t, r.shouldRunHealthCheck(ws), "should run after backoff interval")
}

func setupHealthTest(t *testing.T, statusResp agentd.StatuszResponse) (*WorkspaceReconciler, *v1.Workspace, *httptest.Server) {
	t.Helper()
	opencode.Register()

	origPort := agentdPort
	agentdPort = 0
	t.Cleanup(func() { agentdPort = origPort })

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	server := &httptest.Server{
		Listener: listener,
		Config: &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(statusResp)
		})},
	}
	server.Start()
	t.Cleanup(server.Close)

	_, portStr, _ := net.SplitHostPort(server.Listener.Addr().String())
	agentdPort, _ = strconv.Atoi(portStr)

	scheme := testScheme(t)
	ws := makeWorkspace("ws-health", "default", v1.WorkspacePhaseActive)
	past := metav1.NewTime(time.Now().Add(-10 * time.Minute))
	ws.Status.StartTime = &past
	ws.Status.PodIP = "127.0.0.1"

	fc := fake.NewClientBuilder().
		WithScheme(scheme).
		WithRuntimeObjects(ws).
		WithStatusSubresource(&v1.Workspace{}).
		Build()

	r := &WorkspaceReconciler{Client: fc, Scheme: scheme}

	origInterval := healthCheckInterval
	healthCheckInterval = 0
	t.Cleanup(func() { healthCheckInterval = origInterval })

	return r, ws, server
}

func TestCheckAgentHealth_Healthy(t *testing.T) {
	r, ws, server := setupHealthTest(t, agentd.StatuszResponse{
		Healthy: true, Ready: true, Connected: []string{"opencode"},
		ProvidersConfigured: 1, SessionsActive: 2, AgentVersion: "1.2.27", UptimeSeconds: 3600,
	})
	defer server.Close()

	r.checkAgentHealth(context.Background(), ws)

	found := false
	for _, c := range ws.Status.Conditions {
		if c.Type == v1.WorkspaceConditionAgentHealthy {
			found = true
			assert.Equal(t, "True", c.Status)
			assert.Equal(t, v1.ReasonAgentHealthy, c.Reason)
		}
	}
	assert.True(t, found)
	assert.Equal(t, int32(0), ws.Status.ConsecutiveHealthFailures)
	assert.NotNil(t, ws.Status.LastHealthCheckAt)
}

func TestCheckAgentHealth_Degraded(t *testing.T) {
	r, ws, server := setupHealthTest(t, agentd.StatuszResponse{
		Healthy: true, Ready: false, Connected: []string{},
		ProvidersConfigured: 1, AgentVersion: "1.2.27",
	})
	defer server.Close()

	r.checkAgentHealth(context.Background(), ws)

	found := false
	for _, c := range ws.Status.Conditions {
		if c.Type == v1.WorkspaceConditionAgentHealthy {
			found = true
			assert.Equal(t, "False", c.Status)
			assert.Equal(t, v1.ReasonAgentDegraded, c.Reason)
		}
	}
	assert.True(t, found)
}

func TestCheckAgentHealth_Unhealthy(t *testing.T) {
	r, ws, server := setupHealthTest(t, agentd.StatuszResponse{
		Healthy: false, Ready: false, Connected: nil,
	})
	defer server.Close()

	r.checkAgentHealth(context.Background(), ws)

	found := false
	for _, c := range ws.Status.Conditions {
		if c.Type == v1.WorkspaceConditionAgentHealthy {
			found = true
			assert.Equal(t, "False", c.Status)
			assert.Equal(t, v1.ReasonAgentUnhealthy, c.Reason)
		}
	}
	assert.True(t, found)
	assert.Equal(t, int32(1), ws.Status.ConsecutiveHealthFailures)
}

func TestCheckAgentHealth_ConnectionRefused(t *testing.T) {
	opencode.Register()
	r := reconcilerFor(t)
	ws := makeWorkspace("ws-connref", "default", v1.WorkspacePhaseActive)
	past := metav1.NewTime(time.Now().Add(-10 * time.Minute))
	ws.Status.StartTime = &past
	ws.Status.PodIP = "127.0.0.1"

	origInterval := healthCheckInterval
	origPort := agentdPort
	healthCheckInterval = 0
	agentdPort = 1
	defer func() {
		healthCheckInterval = origInterval
		agentdPort = origPort
	}()

	r.checkAgentHealth(context.Background(), ws)

	found := false
	for _, c := range ws.Status.Conditions {
		if c.Type == v1.WorkspaceConditionAgentHealthy {
			found = true
			assert.Equal(t, "Unknown", c.Status)
			assert.Equal(t, v1.ReasonHealthCheckFailed, c.Reason)
		}
	}
	assert.True(t, found)
	assert.Equal(t, int32(1), ws.Status.ConsecutiveHealthFailures)
}

func TestCheckAgentHealth_SuccessResetsFailures(t *testing.T) {
	r, ws, server := setupHealthTest(t, agentd.StatuszResponse{
		Healthy: true, Ready: true, Connected: []string{"opencode"},
		ProvidersConfigured: 1, AgentVersion: "1.2.27",
	})
	defer server.Close()
	ws.Status.ConsecutiveHealthFailures = 5

	r.checkAgentHealth(context.Background(), ws)
	assert.Equal(t, int32(0), ws.Status.ConsecutiveHealthFailures, "success should reset failure count")
}

func TestCheckAgentHealth_UnhealthyRepairsPodAfterThreshold(t *testing.T) {
	opencode.Register()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(agentd.StatuszResponse{Healthy: false})
	}))
	defer server.Close()

	scheme := testScheme(t)
	ws := makeWorkspace("ws-repair", "default", v1.WorkspacePhaseActive)
	ws.UID = "ws-repair-uid"
	past := metav1.NewTime(time.Now().Add(-10 * time.Minute))
	ws.Status.StartTime = &past
	_, portStr, _ := net.SplitHostPort(server.Listener.Addr().String())
	port, _ := strconv.Atoi(portStr)
	ws.Status.PodIP = "127.0.0.1"

	pod := makeRunningPod(podName("ws-repair", string(ws.UID)), "default", "127.0.0.1")

	origInterval := healthCheckInterval
	origPort := agentdPort
	healthCheckInterval = 0
	agentdPort = port
	defer func() {
		healthCheckInterval = origInterval
		agentdPort = origPort
	}()

	fc := fake.NewClientBuilder().
		WithScheme(scheme).
		WithRuntimeObjects(ws, pod).
		WithStatusSubresource(&v1.Workspace{}).
		Build()
	r := &WorkspaceReconciler{Client: fc, Scheme: scheme}

	ws.Status.ConsecutiveHealthFailures = 2
	r.checkAgentHealth(context.Background(), ws)

	assert.Equal(t, int32(3), ws.Status.ConsecutiveHealthFailures)
	assert.Equal(t, v1.WorkspacePhaseCreating, ws.Status.Phase, "should transition to Creating to restart pod")
	assert.Empty(t, ws.Status.PodIP, "PodIP should be cleared")
	assert.Equal(t, int32(1), ws.Status.RestartCount, "RestartCount should increment")

	var podCheck corev1.Pod
	getErr := fc.Get(context.Background(), types.NamespacedName{Name: podName("ws-repair", string(ws.UID)), Namespace: "default"}, &podCheck)
	assert.True(t, getErr != nil, "pod should be deleted after health failure threshold")
}

func TestCheckAgentHealth_UnhealthyBelowThreshold_NoRepair(t *testing.T) {
	opencode.Register()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(agentd.StatuszResponse{Healthy: false})
	}))
	defer server.Close()

	scheme := testScheme(t)
	ws := makeWorkspace("ws-below", "default", v1.WorkspacePhaseActive)
	past := metav1.NewTime(time.Now().Add(-10 * time.Minute))
	ws.Status.StartTime = &past
	_, portStr, _ := net.SplitHostPort(server.Listener.Addr().String())
	port, _ := strconv.Atoi(portStr)
	ws.Status.PodIP = "127.0.0.1"

	origInterval := healthCheckInterval
	origPort := agentdPort
	healthCheckInterval = 0
	agentdPort = port
	defer func() {
		healthCheckInterval = origInterval
		agentdPort = origPort
	}()

	fc := fake.NewClientBuilder().
		WithScheme(scheme).
		WithRuntimeObjects(ws).
		WithStatusSubresource(&v1.Workspace{}).
		Build()
	r := &WorkspaceReconciler{Client: fc, Scheme: scheme}

	ws.Status.ConsecutiveHealthFailures = 1
	r.checkAgentHealth(context.Background(), ws)

	assert.Equal(t, int32(2), ws.Status.ConsecutiveHealthFailures)
	assert.Equal(t, v1.WorkspacePhaseActive, ws.Status.Phase, "should stay Active below threshold")
	assert.Equal(t, int32(0), ws.Status.RestartCount)
}

func TestCheckAgentHealth_EmptyPodIP(t *testing.T) {
	opencode.Register()
	r := reconcilerFor(t)
	ws := makeWorkspace("ws-noip", "default", v1.WorkspacePhaseActive)
	past := metav1.NewTime(time.Now().Add(-10 * time.Minute))
	ws.Status.StartTime = &past
	ws.Status.PodIP = ""

	r.checkAgentHealth(context.Background(), ws)
	assert.Empty(t, ws.Status.Conditions, "no health check should run without PodIP")
}

func TestBuildPod_HTTPProbes(t *testing.T) {
	opencode.Register()
	ws := makeWorkspace("ws-probes", "default", v1.WorkspacePhaseCreating)
	ws.Status.PVCName = "workspace-ws-probes"
	pvc := makeBoundPVC("workspace-ws-probes", "default", ws.UID)
	pwSecret := makePasswordSecret("ws-probes", "default")
	rte := makeRuntimeEnv("python-3.11")
	r := reconcilerFor(t, ws, pvc, pwSecret, rte)

	pod, err := r.buildPod(context.Background(), ws)
	require.NoError(t, err)

	require.NotNil(t, pod.Spec.Containers[0].ReadinessProbe)
	assert.NotNil(t, pod.Spec.Containers[0].ReadinessProbe.HTTPGet)
	assert.Equal(t, "/v1/readyz", pod.Spec.Containers[0].ReadinessProbe.HTTPGet.Path)
	assert.Equal(t, int32(4097), pod.Spec.Containers[0].ReadinessProbe.HTTPGet.Port.IntVal)
	assert.Nil(t, pod.Spec.Containers[0].ReadinessProbe.TCPSocket)

	require.NotNil(t, pod.Spec.Containers[0].LivenessProbe)
	assert.NotNil(t, pod.Spec.Containers[0].LivenessProbe.HTTPGet)
	assert.Equal(t, "/v1/healthz", pod.Spec.Containers[0].LivenessProbe.HTTPGet.Path)
	assert.Equal(t, int32(4097), pod.Spec.Containers[0].LivenessProbe.HTTPGet.Port.IntVal)
	assert.Nil(t, pod.Spec.Containers[0].LivenessProbe.TCPSocket)

	portNames := make(map[string]bool)
	for _, p := range pod.Spec.Containers[0].Ports {
		portNames[p.Name] = true
	}
	assert.True(t, portNames["opencode"], "opencode port should be declared")
	assert.True(t, portNames["agentd"], "agentd port should be declared")
}

func TestReconcile_Active_CredentialConditionSet(t *testing.T) {
	opencode.Register()
	scheme := testScheme(t)
	ws := makeWorkspace("ws-active-cond", "default", v1.WorkspacePhaseActive)
	ws.Status.PodIP = "10.0.0.1"
	now := metav1.Now()
	ws.Status.StartTime = &now
	expectedPodName := podName("ws-active-cond", string(ws.UID))
	pod := makeRunningPod(expectedPodName, "default", "10.0.0.1")
	pwSecret := makePasswordSecret("ws-active-cond", "default")
	credSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "workspace-creds-ws-active-cond", Namespace: "default"},
		Data:       map[string][]byte{"provider-config": []byte(`{"apiKey":"test"}`)},
	}

	fc := fake.NewClientBuilder().
		WithScheme(scheme).
		WithRuntimeObjects(ws, pod, pwSecret, credSecret).
		WithStatusSubresource(&v1.Workspace{}).
		Build()
	r := &WorkspaceReconciler{Client: fc, Scheme: scheme}

	_, err := r.Reconcile(context.Background(), reqFor("ws-active-cond", "default"))
	require.NoError(t, err)

	updated := &v1.Workspace{}
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: "ws-active-cond", Namespace: "default"}, updated))

	found := false
	for _, c := range updated.Status.Conditions {
		if c.Type == v1.WorkspaceConditionCredentialsAvailable {
			found = true
			assert.Equal(t, "True", c.Status)
		}
	}
	assert.True(t, found, "CredentialsAvailable condition should be set during handleActive")
}

func TestInitContainerScript_NoElseBranch(t *testing.T) {
	opencode.Register()
	ws := makeWorkspace("ws-init", "default", v1.WorkspacePhaseCreating)
	ws.Status.PVCName = "workspace-ws-init"
	pvc := makeBoundPVC("workspace-ws-init", "default", ws.UID)
	pwSecret := makePasswordSecret("ws-init", "default")
	rte := makeRuntimeEnv("python-3.11")
	r := reconcilerFor(t, ws, pvc, pwSecret, rte)

	pod, err := r.buildPod(context.Background(), ws)
	require.NoError(t, err)

	var credInit *corev1.Container
	for i := range pod.Spec.InitContainers {
		if pod.Spec.InitContainers[i].Name == "credential-setup" {
			credInit = &pod.Spec.InitContainers[i]
			break
		}
	}
	require.NotNil(t, credInit, "credential-setup init container should exist")
	require.Len(t, credInit.Command, 3)
	assert.Equal(t, "/bin/sh", credInit.Command[0])
	assert.Equal(t, "-c", credInit.Command[1])
	script := credInit.Command[2]
	assert.NotContains(t, script, "echo '{}'", "init script should NOT write empty JSON when no creds exist")
	assert.Contains(t, script, "cp /mnt/secrets/password/password /sandbox-cfg/password", "password should always be copied")
	assert.Contains(t, script, "cp /mnt/secrets/credentials/provider-config /sandbox-cfg/credentials", "creds should be copied when present")
}

func TestReconcile_Active_SuspendOnCredentialLoss(t *testing.T) {
	opencode.Register()
	scheme := testScheme(t)
	ws := makeWorkspace("ws-suspend-cred", "default", v1.WorkspacePhaseActive)
	ws.Status.PodIP = "10.0.0.1"
	now := metav1.Now()
	ws.Status.StartTime = &now
	ws.Annotations = map[string]string{"llmsafespace.dev/suspend-on-cred-loss": "true"}
	expectedPodName := podName("ws-suspend-cred", string(ws.UID))
	pod := makeRunningPod(expectedPodName, "default", "10.0.0.1")
	pwSecret := makePasswordSecret("ws-suspend-cred", "default")

	fc := fake.NewClientBuilder().
		WithScheme(scheme).
		WithRuntimeObjects(ws, pod, pwSecret).
		WithStatusSubresource(&v1.Workspace{}).
		Build()
	r := &WorkspaceReconciler{Client: fc, Scheme: scheme}

	_, err := r.Reconcile(context.Background(), reqFor("ws-suspend-cred", "default"))
	require.NoError(t, err)

	updated := &v1.Workspace{}
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: "ws-suspend-cred", Namespace: "default"}, updated))
	assert.Equal(t, v1.WorkspacePhaseSuspending, updated.Status.Phase, "should suspend on credential loss with annotation")
}

func TestReconcile_Active_NoSuspendWithoutAnnotation(t *testing.T) {
	opencode.Register()
	scheme := testScheme(t)
	ws := makeWorkspace("ws-no-suspend", "default", v1.WorkspacePhaseActive)
	ws.Status.PodIP = "10.0.0.1"
	now := metav1.Now()
	ws.Status.StartTime = &now
	expectedPodName := podName("ws-no-suspend", string(ws.UID))
	pod := makeRunningPod(expectedPodName, "default", "10.0.0.1")
	pwSecret := makePasswordSecret("ws-no-suspend", "default")

	fc := fake.NewClientBuilder().
		WithScheme(scheme).
		WithRuntimeObjects(ws, pod, pwSecret).
		WithStatusSubresource(&v1.Workspace{}).
		Build()
	r := &WorkspaceReconciler{Client: fc, Scheme: scheme}

	result, err := r.Reconcile(context.Background(), reqFor("ws-no-suspend", "default"))
	require.NoError(t, err)

	updated := &v1.Workspace{}
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: "ws-no-suspend", Namespace: "default"}, updated))
	assert.Equal(t, v1.WorkspacePhaseActive, updated.Status.Phase, "should stay Active without annotation")
	assert.Equal(t, requeueActive, result.RequeueAfter)
}

func TestReconcile_Active_NoSuspendWhenCredentialsValid(t *testing.T) {
	opencode.Register()
	scheme := testScheme(t)
	ws := makeWorkspace("ws-cred-ok", "default", v1.WorkspacePhaseActive)
	ws.Status.PodIP = "10.0.0.1"
	now := metav1.Now()
	ws.Status.StartTime = &now
	ws.Annotations = map[string]string{"llmsafespace.dev/suspend-on-cred-loss": "true"}
	expectedPodName := podName("ws-cred-ok", string(ws.UID))
	pod := makeRunningPod(expectedPodName, "default", "10.0.0.1")
	pwSecret := makePasswordSecret("ws-cred-ok", "default")
	credSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "workspace-creds-ws-cred-ok", Namespace: "default"},
		Data:       map[string][]byte{"provider-config": []byte(`{"apiKey":"test"}`)},
	}

	fc := fake.NewClientBuilder().
		WithScheme(scheme).
		WithRuntimeObjects(ws, pod, pwSecret, credSecret).
		WithStatusSubresource(&v1.Workspace{}).
		Build()
	r := &WorkspaceReconciler{Client: fc, Scheme: scheme}

	result, err := r.Reconcile(context.Background(), reqFor("ws-cred-ok", "default"))
	require.NoError(t, err)

	updated := &v1.Workspace{}
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: "ws-cred-ok", Namespace: "default"}, updated))
	assert.Equal(t, v1.WorkspacePhaseActive, updated.Status.Phase, "should stay Active when credentials are valid")
	assert.Equal(t, requeueActive, result.RequeueAfter)
}

func makeRuntimeEnv(name string) *v1.RuntimeEnvironment {
	return &v1.RuntimeEnvironment{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec:       v1.RuntimeEnvironmentSpec{Image: "ghcr.io/test/" + name, Language: "python", Version: "3.11"},
	}
}
