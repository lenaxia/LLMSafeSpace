package settings

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
)

// mockUserStore is a test double for UserStore.
type mockUserStore struct {
	mu     sync.Mutex
	data   map[string]map[string]json.RawMessage // userID → key → value
	getErr error
	setErr error
}

func newMockUserStore() *mockUserStore {
	return &mockUserStore{data: make(map[string]map[string]json.RawMessage)}
}

func (m *mockUserStore) GetAllUserSettings(_ context.Context, userID string) (map[string]json.RawMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.getErr != nil {
		return nil, m.getErr
	}
	if m.data[userID] == nil {
		return map[string]json.RawMessage{}, nil
	}
	cp := make(map[string]json.RawMessage, len(m.data[userID]))
	for k, v := range m.data[userID] {
		cp[k] = v
	}
	return cp, nil
}

func (m *mockUserStore) SetUserSetting(_ context.Context, userID, key string, value json.RawMessage) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.setErr != nil {
		return m.setErr
	}
	if m.data[userID] == nil {
		m.data[userID] = make(map[string]json.RawMessage)
	}
	m.data[userID][key] = value
	return nil
}

func (m *mockUserStore) setForUser(userID, key string, value any) {
	raw, _ := json.Marshal(value)
	m.mu.Lock()
	if m.data[userID] == nil {
		m.data[userID] = make(map[string]json.RawMessage)
	}
	m.data[userID][key] = raw
	m.mu.Unlock()
}

func newTestUserService(store *mockUserStore) *UserService {
	return NewUserService(store, &mockLogger{})
}

func TestUserService_GetBool_ReturnsDBValue(t *testing.T) {
	store := newMockUserStore()
	store.setForUser("user1", "compactMode", true)
	svc := newTestUserService(store)

	val, err := svc.GetBool(context.Background(), "user1", "compactMode")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val != true {
		t.Errorf("expected true, got %v", val)
	}
}

func TestUserService_GetBool_ReturnsDefault(t *testing.T) {
	store := newMockUserStore()
	svc := newTestUserService(store)

	val, err := svc.GetBool(context.Background(), "user1", "compactMode")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val != false {
		t.Errorf("expected false (default), got %v", val)
	}
}

func TestUserService_GetBool_UnknownKey(t *testing.T) {
	store := newMockUserStore()
	svc := newTestUserService(store)

	_, err := svc.GetBool(context.Background(), "user1", "nonexistent")
	if err == nil {
		t.Error("expected error for unknown key")
	}
}

func TestUserService_GetInt_ReturnsDBValue(t *testing.T) {
	store := newMockUserStore()
	store.setForUser("user1", "fontSize", 18)
	svc := newTestUserService(store)

	val, err := svc.GetInt(context.Background(), "user1", "fontSize")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val != 18 {
		t.Errorf("expected 18, got %d", val)
	}
}

func TestUserService_GetInt_ReturnsDefault(t *testing.T) {
	store := newMockUserStore()
	svc := newTestUserService(store)

	val, err := svc.GetInt(context.Background(), "user1", "fontSize")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val != 14 {
		t.Errorf("expected 14 (default), got %d", val)
	}
}

func TestUserService_GetString_ReturnsDBValue(t *testing.T) {
	store := newMockUserStore()
	store.setForUser("user1", "preferredModel", "claude-3")
	svc := newTestUserService(store)

	val, err := svc.GetString(context.Background(), "user1", "preferredModel")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val != "claude-3" {
		t.Errorf("expected claude-3, got %q", val)
	}
}

func TestUserService_GetAll_MergesWithDefaults(t *testing.T) {
	store := newMockUserStore()
	store.setForUser("user1", "theme", "dark")
	svc := newTestUserService(store)

	all, err := svc.GetAll(context.Background(), "user1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if all["theme"] != "dark" {
		t.Errorf("expected dark for overridden key, got %v", all["theme"])
	}
	if all["fontSize"] != float64(14) && all["fontSize"] != 14 {
		t.Errorf("expected 14 (default) for non-overridden key, got %v (%T)", all["fontSize"], all["fontSize"])
	}
	if len(all) != len(UserSettings()) {
		t.Errorf("expected %d keys, got %d", len(UserSettings()), len(all))
	}
}

func TestUserService_Set_ValidValue(t *testing.T) {
	store := newMockUserStore()
	svc := newTestUserService(store)

	err := svc.Set(context.Background(), "user1", "fontSize", 20)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	val, err := svc.GetInt(context.Background(), "user1", "fontSize")
	if err != nil {
		t.Fatalf("unexpected error on get: %v", err)
	}
	if val != 20 {
		t.Errorf("expected 20, got %d", val)
	}
}

func TestUserService_Set_InvalidValue_OutOfRange(t *testing.T) {
	store := newMockUserStore()
	svc := newTestUserService(store)

	err := svc.Set(context.Background(), "user1", "fontSize", 99)
	if err == nil {
		t.Error("expected validation error for out of range")
	}
}

func TestUserService_Set_InvalidValue_BadEnum(t *testing.T) {
	store := newMockUserStore()
	svc := newTestUserService(store)

	err := svc.Set(context.Background(), "user1", "theme", "neon")
	if err == nil {
		t.Error("expected validation error for invalid enum")
	}
}

func TestUserService_Set_UnknownKey(t *testing.T) {
	store := newMockUserStore()
	svc := newTestUserService(store)

	err := svc.Set(context.Background(), "user1", "nonexistent", true)
	if err == nil {
		t.Error("expected error for unknown key")
	}
}

func TestUserService_UserIsolation(t *testing.T) {
	store := newMockUserStore()
	store.setForUser("user1", "theme", "dark")
	store.setForUser("user2", "theme", "light")
	svc := newTestUserService(store)

	val1, _ := svc.GetString(context.Background(), "user1", "theme")
	val2, _ := svc.GetString(context.Background(), "user2", "theme")

	if val1 != "dark" {
		t.Errorf("user1 expected dark, got %q", val1)
	}
	if val2 != "light" {
		t.Errorf("user2 expected light, got %q", val2)
	}
}

func TestUserService_DBError_ReturnsError(t *testing.T) {
	store := newMockUserStore()
	store.getErr = fmt.Errorf("connection refused")
	svc := newTestUserService(store)

	_, err := svc.GetBool(context.Background(), "user1", "compactMode")
	if err == nil {
		t.Error("expected error when DB is down")
	}
}

func TestUserService_Set_DBWriteError(t *testing.T) {
	store := newMockUserStore()
	store.setErr = fmt.Errorf("write failed")
	svc := newTestUserService(store)

	err := svc.Set(context.Background(), "user1", "theme", "dark")
	if err == nil {
		t.Error("expected error when DB write fails")
	}
}
