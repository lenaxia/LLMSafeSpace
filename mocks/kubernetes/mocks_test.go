package kubernetes_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"

	kmocks "github.com/lenaxia/llmsafespace/mocks/kubernetes"
	v1 "github.com/lenaxia/llmsafespace/pkg/apis/llmsafespace/v1"
	"github.com/lenaxia/llmsafespace/pkg/interfaces"
)

// Compile-time interface checks — if any mock is missing a method the file
// won't compile and CI catches it immediately without running a single test.
var (
	_ interfaces.KubernetesClient            = (*kmocks.MockKubernetesClient)(nil)
	_ interfaces.LLMSafespaceV1Interface     = (*kmocks.MockLLMSafespaceV1Interface)(nil)
	_ interfaces.SandboxInterface            = (*kmocks.MockSandboxInterface)(nil)
	_ interfaces.RuntimeEnvironmentInterface = (*kmocks.MockRuntimeEnvironmentInterface)(nil)
	_ interfaces.SandboxProfileInterface     = (*kmocks.MockSandboxProfileInterface)(nil)
	_ watch.Interface                        = (*kmocks.MockWatch)(nil)
)

// ===== MockKubernetesClient =====

func TestMockKubernetesClient_Start(t *testing.T) {
	m := kmocks.NewMockKubernetesClient()
	m.On("Start").Return(nil)
	assert.NoError(t, m.Start())
	m.AssertExpectations(t)
}

func TestMockKubernetesClient_Start_Error(t *testing.T) {
	m := kmocks.NewMockKubernetesClient()
	m.On("Start").Return(errors.New("conn refused"))
	assert.EqualError(t, m.Start(), "conn refused")
	m.AssertExpectations(t)
}

func TestMockKubernetesClient_Stop(t *testing.T) {
	m := kmocks.NewMockKubernetesClient()
	m.On("Stop").Return()
	m.Stop()
	m.AssertExpectations(t)
}

func TestMockKubernetesClient_Clientset(t *testing.T) {
	m := kmocks.NewMockKubernetesClient()
	cs := k8sfake.NewSimpleClientset()
	m.On("Clientset").Return(cs)
	assert.Equal(t, cs, m.Clientset())
	m.AssertExpectations(t)
}

func TestMockKubernetesClient_RESTConfig(t *testing.T) {
	m := kmocks.NewMockKubernetesClient()
	cfg := &rest.Config{Host: "https://k8s.example.com"}
	m.On("RESTConfig").Return(cfg)
	assert.Equal(t, cfg, m.RESTConfig())
	m.AssertExpectations(t)
}

func TestMockKubernetesClient_InformerFactory_Nil(t *testing.T) {
	m := kmocks.NewMockKubernetesClient()
	m.On("InformerFactory").Return(nil)
	assert.Nil(t, m.InformerFactory())
	m.AssertExpectations(t)
}

func TestMockKubernetesClient_LlmsafespaceV1(t *testing.T) {
	m := kmocks.NewMockKubernetesClient()
	v1iface := kmocks.NewMockLLMSafespaceV1Interface()
	m.On("LlmsafespaceV1").Return(v1iface)
	assert.Equal(t, v1iface, m.LlmsafespaceV1())
	m.AssertExpectations(t)
}

// ===== MockLLMSafespaceV1Interface =====

func TestMockLLMSafespaceV1_Sandboxes(t *testing.T) {
	m := kmocks.NewMockLLMSafespaceV1Interface()
	sb := kmocks.NewMockSandboxInterface()
	m.On("Sandboxes", "default").Return(sb)
	assert.Equal(t, sb, m.Sandboxes("default"))
	m.AssertExpectations(t)
}

func TestMockLLMSafespaceV1_RuntimeEnvironments(t *testing.T) {
	m := kmocks.NewMockLLMSafespaceV1Interface()
	rte := kmocks.NewMockRuntimeEnvironmentInterface()
	m.On("RuntimeEnvironments", "default").Return(rte)
	assert.Equal(t, rte, m.RuntimeEnvironments("default"))
	m.AssertExpectations(t)
}

func TestMockLLMSafespaceV1_SandboxProfiles(t *testing.T) {
	m := kmocks.NewMockLLMSafespaceV1Interface()
	sp := kmocks.NewMockSandboxProfileInterface()
	m.On("SandboxProfiles", "default").Return(sp)
	assert.Equal(t, sp, m.SandboxProfiles("default"))
	m.AssertExpectations(t)
}

// ===== MockSandboxInterface =====

func TestMockSandboxInterface_Create_Success(t *testing.T) {
	m := kmocks.NewMockSandboxInterface()
	in := &v1.Sandbox{ObjectMeta: metav1.ObjectMeta{Name: "sb-1"}}
	out := &v1.Sandbox{ObjectMeta: metav1.ObjectMeta{Name: "sb-1", ResourceVersion: "1"}}
	m.On("Create", in).Return(out, nil)

	got, err := m.Create(in)
	assert.NoError(t, err)
	assert.Equal(t, "1", got.ResourceVersion)
	m.AssertExpectations(t)
}

func TestMockSandboxInterface_Create_Error(t *testing.T) {
	m := kmocks.NewMockSandboxInterface()
	in := &v1.Sandbox{ObjectMeta: metav1.ObjectMeta{Name: "sb-1"}}
	m.On("Create", in).Return((*v1.Sandbox)(nil), errors.New("already exists"))

	got, err := m.Create(in)
	assert.Nil(t, got)
	assert.EqualError(t, err, "already exists")
	m.AssertExpectations(t)
}

func TestMockSandboxInterface_Update(t *testing.T) {
	m := kmocks.NewMockSandboxInterface()
	sb := &v1.Sandbox{ObjectMeta: metav1.ObjectMeta{Name: "sb-1"}}
	m.On("Update", sb).Return(sb, nil)
	got, err := m.Update(sb)
	assert.NoError(t, err)
	assert.Equal(t, sb.Name, got.Name)
	m.AssertExpectations(t)
}

func TestMockSandboxInterface_UpdateStatus(t *testing.T) {
	m := kmocks.NewMockSandboxInterface()
	sb := &v1.Sandbox{ObjectMeta: metav1.ObjectMeta{Name: "sb-1"}}
	m.On("UpdateStatus", sb).Return(sb, nil)
	got, err := m.UpdateStatus(sb)
	assert.NoError(t, err)
	assert.Equal(t, sb.Name, got.Name)
	m.AssertExpectations(t)
}

func TestMockSandboxInterface_Delete_Success(t *testing.T) {
	m := kmocks.NewMockSandboxInterface()
	m.On("Delete", "sb-1", mock.AnythingOfType("v1.DeleteOptions")).Return(nil)
	assert.NoError(t, m.Delete("sb-1", metav1.DeleteOptions{}))
	m.AssertExpectations(t)
}

func TestMockSandboxInterface_Delete_Error(t *testing.T) {
	m := kmocks.NewMockSandboxInterface()
	m.On("Delete", "sb-1", mock.Anything).Return(errors.New("not found"))
	assert.EqualError(t, m.Delete("sb-1", metav1.DeleteOptions{}), "not found")
	m.AssertExpectations(t)
}

func TestMockSandboxInterface_Get_Success(t *testing.T) {
	m := kmocks.NewMockSandboxInterface()
	sb := &v1.Sandbox{ObjectMeta: metav1.ObjectMeta{Name: "sb-1"}}
	m.On("Get", "sb-1", mock.AnythingOfType("v1.GetOptions")).Return(sb, nil)
	got, err := m.Get("sb-1", metav1.GetOptions{})
	assert.NoError(t, err)
	assert.Equal(t, "sb-1", got.Name)
	m.AssertExpectations(t)
}

func TestMockSandboxInterface_Get_Nil(t *testing.T) {
	m := kmocks.NewMockSandboxInterface()
	m.On("Get", "missing", mock.Anything).Return((*v1.Sandbox)(nil), errors.New("not found"))
	got, err := m.Get("missing", metav1.GetOptions{})
	assert.Nil(t, got)
	assert.Error(t, err)
	m.AssertExpectations(t)
}

func TestMockSandboxInterface_List_Success(t *testing.T) {
	m := kmocks.NewMockSandboxInterface()
	list := &v1.SandboxList{Items: []v1.Sandbox{{ObjectMeta: metav1.ObjectMeta{Name: "sb-1"}}}}
	m.On("List", mock.AnythingOfType("v1.ListOptions")).Return(list, nil)
	got, err := m.List(metav1.ListOptions{})
	assert.NoError(t, err)
	assert.Len(t, got.Items, 1)
	m.AssertExpectations(t)
}

func TestMockSandboxInterface_List_Nil(t *testing.T) {
	m := kmocks.NewMockSandboxInterface()
	m.On("List", mock.Anything).Return((*v1.SandboxList)(nil), errors.New("timeout"))
	got, err := m.List(metav1.ListOptions{})
	assert.Nil(t, got)
	assert.Error(t, err)
	m.AssertExpectations(t)
}

func TestMockSandboxInterface_Watch(t *testing.T) {
	m := kmocks.NewMockSandboxInterface()
	w := kmocks.NewMockWatch()
	w.On("Stop").Return()
	m.On("Watch", mock.Anything).Return(w, nil)
	got, err := m.Watch(metav1.ListOptions{})
	assert.NoError(t, err)
	assert.NotNil(t, got)
	got.Stop()
	m.AssertExpectations(t)
}

// ===== MockRuntimeEnvironmentInterface =====

func TestMockRuntimeEnvironmentInterface_Create(t *testing.T) {
	m := kmocks.NewMockRuntimeEnvironmentInterface()
	rte := &v1.RuntimeEnvironment{ObjectMeta: metav1.ObjectMeta{Name: "python-310"}}
	m.On("Create", rte).Return(rte, nil)
	got, err := m.Create(rte)
	assert.NoError(t, err)
	assert.Equal(t, "python-310", got.Name)
	m.AssertExpectations(t)
}

func TestMockRuntimeEnvironmentInterface_Get_Nil(t *testing.T) {
	m := kmocks.NewMockRuntimeEnvironmentInterface()
	m.On("Get", "missing", mock.Anything).Return((*v1.RuntimeEnvironment)(nil), errors.New("not found"))
	got, err := m.Get("missing", metav1.GetOptions{})
	assert.Nil(t, got)
	assert.Error(t, err)
	m.AssertExpectations(t)
}

func TestMockRuntimeEnvironmentInterface_List(t *testing.T) {
	m := kmocks.NewMockRuntimeEnvironmentInterface()
	list := &v1.RuntimeEnvironmentList{Items: []v1.RuntimeEnvironment{{ObjectMeta: metav1.ObjectMeta{Name: "python-310"}}}}
	m.On("List", mock.Anything).Return(list, nil)
	got, err := m.List(metav1.ListOptions{})
	assert.NoError(t, err)
	assert.Len(t, got.Items, 1)
	m.AssertExpectations(t)
}

// ===== MockSandboxProfileInterface =====

func TestMockSandboxProfileInterface_Create(t *testing.T) {
	m := kmocks.NewMockSandboxProfileInterface()
	sp := &v1.SandboxProfile{ObjectMeta: metav1.ObjectMeta{Name: "default-profile"}}
	m.On("Create", sp).Return(sp, nil)
	got, err := m.Create(sp)
	assert.NoError(t, err)
	assert.Equal(t, "default-profile", got.Name)
	m.AssertExpectations(t)
}

func TestMockSandboxProfileInterface_Delete(t *testing.T) {
	m := kmocks.NewMockSandboxProfileInterface()
	m.On("Delete", "default-profile", mock.Anything).Return(nil)
	assert.NoError(t, m.Delete("default-profile", metav1.DeleteOptions{}))
	m.AssertExpectations(t)
}

func TestMockSandboxProfileInterface_Get_Nil(t *testing.T) {
	m := kmocks.NewMockSandboxProfileInterface()
	m.On("Get", "missing", mock.Anything).Return((*v1.SandboxProfile)(nil), errors.New("not found"))
	got, err := m.Get("missing", metav1.GetOptions{})
	assert.Nil(t, got)
	assert.Error(t, err)
	m.AssertExpectations(t)
}

// ===== MockWatch =====

func TestMockWatch_ResultChan_ReceivesEvent(t *testing.T) {
	w := kmocks.NewMockWatch()
	sb := &v1.Sandbox{ObjectMeta: metav1.ObjectMeta{Name: "sb-1"}}

	go w.SendEvent(watch.Added, sb)

	event := <-w.ResultChan()
	assert.Equal(t, watch.Added, event.Type)
	assert.Equal(t, sb, event.Object)
}

func TestMockWatch_ResultChan_MultipleEvents(t *testing.T) {
	w := kmocks.NewMockWatch()
	sb1 := &v1.Sandbox{ObjectMeta: metav1.ObjectMeta{Name: "sb-1"}}
	sb2 := &v1.Sandbox{ObjectMeta: metav1.ObjectMeta{Name: "sb-2"}}

	go func() {
		w.SendEvent(watch.Added, sb1)
		w.SendEvent(watch.Modified, sb2)
	}()

	e1 := <-w.ResultChan()
	e2 := <-w.ResultChan()
	assert.Equal(t, watch.Added, e1.Type)
	assert.Equal(t, watch.Modified, e2.Type)
}

func TestMockWatch_Stop(t *testing.T) {
	w := kmocks.NewMockWatch()
	w.On("Stop").Return()
	w.Stop()
	w.AssertExpectations(t)
	// Channel must be closed after Stop
	_, open := <-w.ResultChan()
	assert.False(t, open, "ResultChan must be closed after Stop")
}

func TestMockWatch_StopIsIdempotent(t *testing.T) {
	w := kmocks.NewMockWatch()
	w.On("Stop").Return()
	// Calling Stop twice must not panic (sync.Once protects the channel close)
	w.Stop()
	assert.NotPanics(t, func() { w.Stop() })
}
