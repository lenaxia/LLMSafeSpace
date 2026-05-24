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
	_ interfaces.RuntimeEnvironmentInterface = (*kmocks.MockRuntimeEnvironmentInterface)(nil)
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


func TestMockLLMSafespaceV1_RuntimeEnvironments(t *testing.T) {
	m := kmocks.NewMockLLMSafespaceV1Interface()
	rte := kmocks.NewMockRuntimeEnvironmentInterface()
	m.On("RuntimeEnvironments", "default").Return(rte)
	assert.Equal(t, rte, m.RuntimeEnvironments("default"))
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
