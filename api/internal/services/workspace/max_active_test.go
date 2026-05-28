package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	apiinterfaces "github.com/lenaxia/llmsafespace/api/internal/interfaces"
	v1 "github.com/lenaxia/llmsafespace/pkg/apis/llmsafespace/v1"
	pkginterfaces "github.com/lenaxia/llmsafespace/pkg/interfaces"
	"github.com/lenaxia/llmsafespace/pkg/settings"
	"github.com/lenaxia/llmsafespace/pkg/types"
)

type mockSettingsStore struct {
	data map[string]json.RawMessage
}

func (m *mockSettingsStore) GetAllInstanceSettings(_ context.Context) (map[string]json.RawMessage, error) {
	return m.data, nil
}
func (m *mockSettingsStore) SetInstanceSetting(_ context.Context, key string, value json.RawMessage) error {
	m.data[key] = value
	return nil
}

type mockMaxActiveDB struct {
	workspaces []*types.WorkspaceMetadata
	suspended  []string
}

func (m *mockMaxActiveDB) ListWorkspaces(_ context.Context, _ string, _, _ int) ([]*types.WorkspaceMetadata, *types.PaginationMetadata, error) {
	return m.workspaces, nil, nil
}

type testLogger struct{}

func (l *testLogger) Debug(msg string, keysAndValues ...interface{})            {}
func (l *testLogger) Info(msg string, keysAndValues ...interface{})             {}
func (l *testLogger) Warn(msg string, keysAndValues ...interface{})             {}
func (l *testLogger) Error(msg string, err error, keysAndValues ...interface{}) {}
func (l *testLogger) Fatal(msg string, err error, keysAndValues ...interface{}) {}
func (l *testLogger) With(keysAndValues ...interface{}) pkginterfaces.LoggerInterface {
	return l
}
func (l *testLogger) Sync() error { return nil }

func TestEnforceMaxActive_BelowCap_NoSuspension(t *testing.T) {
	store := &mockSettingsStore{data: make(map[string]json.RawMessage)}
	raw, _ := json.Marshal(3)
	store.data["workspace.maxActiveWorkspacesPerUser"] = raw

	instanceSvc := settings.NewInstanceService(store, nil)

	svc := &Service{
		logger:           &testLogger{},
		instanceSettings: instanceSvc,
		dbService:        &mockDBForMaxActive{workspaces: []*types.WorkspaceMetadata{
			{ID: "ws-1", Phase: "Running", UpdatedAt: time.Now().Add(-1 * time.Hour)},
			{ID: "ws-2", Phase: "Running", UpdatedAt: time.Now()},
		}},
	}

	suspended, err := svc.enforceMaxActiveWorkspaces(context.Background(), "user-1", "ws-target")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if suspended != "" {
		t.Errorf("expected no suspension (below cap), got %q", suspended)
	}
}

func TestEnforceMaxActive_AtCap_SuspendsStalest(t *testing.T) {
	store := &mockSettingsStore{data: make(map[string]json.RawMessage)}
	raw, _ := json.Marshal(2)
	store.data["workspace.maxActiveWorkspacesPerUser"] = raw

	instanceSvc := settings.NewInstanceService(store, nil)

	now := time.Now()
	db := &mockDBForMaxActive{workspaces: []*types.WorkspaceMetadata{
		{ID: "ws-old", UserID: "user-1", Phase: "Running", UpdatedAt: now.Add(-2 * time.Hour)},
		{ID: "ws-new", UserID: "user-1", Phase: "Running", UpdatedAt: now},
	}}

	// Provide a K8s mock that returns a workspace CRD in Active phase
	k8sMock := &mockK8sForMaxActive{
		workspaces: map[string]*v1.Workspace{
			"ws-old": {
				ObjectMeta: metav1.ObjectMeta{Name: "ws-old"},
				Status:     v1.WorkspaceStatus{Phase: v1.WorkspacePhaseActive},
			},
		},
	}

	svc := &Service{
		logger:           &testLogger{},
		instanceSettings: instanceSvc,
		dbService:        db,
		k8sClient:        k8sMock,
		config:           &Config{Namespace: "default"},
	}

	suspended, err := svc.enforceMaxActiveWorkspaces(context.Background(), "user-1", "ws-target")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if suspended != "ws-old" {
		t.Errorf("expected ws-old to be suspended (stalest), got %q", suspended)
	}
}

func TestEnforceMaxActive_NilSettings_NoEnforcement(t *testing.T) {
	svc := &Service{
		logger:           &testLogger{},
		instanceSettings: nil, // not configured
	}

	suspended, err := svc.enforceMaxActiveWorkspaces(context.Background(), "user-1", "ws-target")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if suspended != "" {
		t.Errorf("expected no enforcement when settings nil, got %q", suspended)
	}
}

func TestEnforceMaxActive_ExcludesTarget(t *testing.T) {
	store := &mockSettingsStore{data: make(map[string]json.RawMessage)}
	raw, _ := json.Marshal(1)
	store.data["workspace.maxActiveWorkspacesPerUser"] = raw

	instanceSvc := settings.NewInstanceService(store, nil)

	now := time.Now()
	db := &mockDBForMaxActive{workspaces: []*types.WorkspaceMetadata{
		// The target workspace is Running — should be excluded from count
		{ID: "ws-target", Phase: "Running", UpdatedAt: now},
	}}

	svc := &Service{
		logger:           &testLogger{},
		instanceSettings: instanceSvc,
		dbService:        db,
	}

	suspended, err := svc.enforceMaxActiveWorkspaces(context.Background(), "user-1", "ws-target")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if suspended != "" {
		t.Errorf("expected no suspension (target excluded from count), got %q", suspended)
	}
}

func TestEnforceMaxActive_SuspendedWorkspacesNotCounted(t *testing.T) {
	store := &mockSettingsStore{data: make(map[string]json.RawMessage)}
	raw, _ := json.Marshal(2)
	store.data["workspace.maxActiveWorkspacesPerUser"] = raw

	instanceSvc := settings.NewInstanceService(store, nil)

	now := time.Now()
	db := &mockDBForMaxActive{workspaces: []*types.WorkspaceMetadata{
		{ID: "ws-1", Phase: "Running", UpdatedAt: now},
		{ID: "ws-2", Phase: "Suspended", UpdatedAt: now.Add(-1 * time.Hour)},
		{ID: "ws-3", Phase: "Terminated", UpdatedAt: now.Add(-2 * time.Hour)},
	}}

	svc := &Service{
		logger:           &testLogger{},
		instanceSettings: instanceSvc,
		dbService:        db,
	}

	suspended, err := svc.enforceMaxActiveWorkspaces(context.Background(), "user-1", "ws-target")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Only ws-1 is Running (ws-2 is Suspended, ws-3 is Terminated) — below cap of 2
	if suspended != "" {
		t.Errorf("expected no suspension (only 1 active), got %q", suspended)
	}
}

// mockDBForMaxActive implements the subset of DatabaseService needed for enforcement tests.
type mockDBForMaxActive struct {
	apiinterfaces.DatabaseService // embed to satisfy interface
	workspaces                   []*types.WorkspaceMetadata
}

func (m *mockDBForMaxActive) ListWorkspaces(_ context.Context, _ string, _, _ int) ([]*types.WorkspaceMetadata, *types.PaginationMetadata, error) {
	return m.workspaces, nil, nil
}

func (m *mockDBForMaxActive) GetWorkspace(_ context.Context, workspaceID string) (*types.WorkspaceMetadata, error) {
	for _, ws := range m.workspaces {
		if ws.ID == workspaceID {
			return ws, nil
		}
	}
	return nil, fmt.Errorf("not found")
}

// mockK8sForMaxActive satisfies the k8s client interface for suspend calls.
type mockK8sForMaxActive struct {
	pkginterfaces.KubernetesClient
	workspaces map[string]*v1.Workspace
}

func (m *mockK8sForMaxActive) LlmsafespaceV1() pkginterfaces.LLMSafespaceV1Interface {
	return &mockLLMV1ForMaxActive{workspaces: m.workspaces}
}

type mockLLMV1ForMaxActive struct {
	pkginterfaces.LLMSafespaceV1Interface
	workspaces map[string]*v1.Workspace
}

func (m *mockLLMV1ForMaxActive) Workspaces(_ string) pkginterfaces.WorkspaceInterface {
	return &mockWSInterfaceForMaxActive{workspaces: m.workspaces}
}

type mockWSInterfaceForMaxActive struct {
	pkginterfaces.WorkspaceInterface
	workspaces map[string]*v1.Workspace
}

func (m *mockWSInterfaceForMaxActive) Get(name string, _ metav1.GetOptions) (*v1.Workspace, error) {
	if ws, ok := m.workspaces[name]; ok {
		return ws, nil
	}
	return nil, fmt.Errorf("not found")
}

func (m *mockWSInterfaceForMaxActive) UpdateStatus(ws *v1.Workspace) (*v1.Workspace, error) {
	m.workspaces[ws.Name] = ws
	return ws, nil
}

func TestParseStorageSize(t *testing.T) {
	tests := []struct {
		input string
		want  int64
	}{
		{"1Gi", 1 * 1024 * 1024 * 1024},
		{"10Gi", 10 * 1024 * 1024 * 1024},
		{"512Mi", 512 * 1024 * 1024},
		{"1Mi", 1 * 1024 * 1024},
		{"", 0},
		{"x", 0},
		{"GB", 0},
		{"5TB", 0},
	}
	for _, tt := range tests {
		got := parseStorageSize(tt.input)
		if got != tt.want {
			t.Errorf("parseStorageSize(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func TestEnforceMaxStorage_BelowMax_Passes(t *testing.T) {
	store := &mockSettingsStore{data: make(map[string]json.RawMessage)}
	raw, _ := json.Marshal("10Gi")
	store.data["workspace.maxStorageSize"] = raw

	instanceSvc := settings.NewInstanceService(store, nil)
	svc := &Service{
		logger:           &testLogger{},
		instanceSettings: instanceSvc,
	}

	err := svc.enforceMaxStorageSize(context.Background(), "5Gi")
	if err != nil {
		t.Errorf("expected no error for 5Gi <= 10Gi, got %v", err)
	}
}

func TestEnforceMaxStorage_AtMax_Passes(t *testing.T) {
	store := &mockSettingsStore{data: make(map[string]json.RawMessage)}
	raw, _ := json.Marshal("10Gi")
	store.data["workspace.maxStorageSize"] = raw

	instanceSvc := settings.NewInstanceService(store, nil)
	svc := &Service{
		logger:           &testLogger{},
		instanceSettings: instanceSvc,
	}

	err := svc.enforceMaxStorageSize(context.Background(), "10Gi")
	if err != nil {
		t.Errorf("expected no error for 10Gi == 10Gi, got %v", err)
	}
}

func TestEnforceMaxStorage_AboveMax_Fails(t *testing.T) {
	store := &mockSettingsStore{data: make(map[string]json.RawMessage)}
	raw, _ := json.Marshal("10Gi")
	store.data["workspace.maxStorageSize"] = raw

	instanceSvc := settings.NewInstanceService(store, nil)
	svc := &Service{
		logger:           &testLogger{},
		instanceSettings: instanceSvc,
	}

	err := svc.enforceMaxStorageSize(context.Background(), "20Gi")
	if err == nil {
		t.Error("expected error for 20Gi > 10Gi")
	}
}

func TestEnforceMaxStorage_NilSettings_Passes(t *testing.T) {
	svc := &Service{
		logger:           &testLogger{},
		instanceSettings: nil,
	}

	err := svc.enforceMaxStorageSize(context.Background(), "100Gi")
	if err != nil {
		t.Errorf("expected no error when settings nil, got %v", err)
	}
}

func TestEnforceMaxStorage_MiVsGi(t *testing.T) {
	store := &mockSettingsStore{data: make(map[string]json.RawMessage)}
	raw, _ := json.Marshal("1Gi")
	store.data["workspace.maxStorageSize"] = raw

	instanceSvc := settings.NewInstanceService(store, nil)
	svc := &Service{
		logger:           &testLogger{},
		instanceSettings: instanceSvc,
	}

	// 512Mi < 1Gi — should pass
	err := svc.enforceMaxStorageSize(context.Background(), "512Mi")
	if err != nil {
		t.Errorf("expected 512Mi <= 1Gi to pass, got %v", err)
	}

	// 2048Mi > 1Gi — should fail
	err = svc.enforceMaxStorageSize(context.Background(), "2048Mi")
	if err == nil {
		t.Error("expected 2048Mi > 1Gi to fail")
	}
}
