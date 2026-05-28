package server

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"

	lmocks "github.com/lenaxia/llmsafespace/mocks/logger"
	pkginterfaces "github.com/lenaxia/llmsafespace/pkg/interfaces"
	"github.com/lenaxia/llmsafespace/pkg/settings"
)

type stubSettingsStore struct {
	data map[string]json.RawMessage
}

func (s *stubSettingsStore) GetAllInstanceSettings(_ context.Context) (map[string]json.RawMessage, error) {
	return s.data, nil
}
func (s *stubSettingsStore) SetInstanceSetting(_ context.Context, _ string, _ json.RawMessage) error {
	return nil
}

func TestGetMaxActiveSessions_FromSettings(t *testing.T) {
	data := map[string]json.RawMessage{
		"workspace.defaultMaxActiveSessions": json.RawMessage(`10`),
	}
	var log pkginterfaces.LoggerInterface = lmocks.NewMockLogger()
	svc := settings.NewInstanceService(&stubSettingsStore{data: data}, log)
	svc.Start()

	result := getMaxActiveSessions(context.Background(), svc)
	assert.Equal(t, 10, result)
}

func TestGetMaxActiveSessions_NilSettings_ReturnsFive(t *testing.T) {
	result := getMaxActiveSessions(context.Background(), nil)
	assert.Equal(t, 5, result)
}

func TestGetMaxActiveSessions_SettingsError_ReturnsFive(t *testing.T) {
	// Empty store — key not in store, but schema default is 5
	var log pkginterfaces.LoggerInterface = lmocks.NewMockLogger()
	svc := settings.NewInstanceService(&stubSettingsStore{data: map[string]json.RawMessage{}}, log)
	svc.Start()

	result := getMaxActiveSessions(context.Background(), svc)
	assert.Equal(t, 5, result)
}
