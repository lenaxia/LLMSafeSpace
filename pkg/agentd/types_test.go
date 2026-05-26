package agentd

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestHealthzResponse_JSON(t *testing.T) {
	resp := HealthzResponse{Healthy: true, Version: "1.2.27", UptimeSeconds: 3600}
	data, err := json.Marshal(resp)
	assert.NoError(t, err)
	assert.Contains(t, string(data), `"healthy":true`)
	assert.Contains(t, string(data), `"version":"1.2.27"`)
	assert.Contains(t, string(data), `"uptime_seconds":3600`)
}

func TestReadyzResponse_JSON(t *testing.T) {
	resp := ReadyzResponse{
		Ready:               true,
		ProvidersConnected:  []string{"opencode"},
		ProvidersConfigured: 1,
		AgentVersion:        "1.2.27",
		AgentType:           "opencode",
	}
	data, err := json.Marshal(resp)
	assert.NoError(t, err)

	var decoded ReadyzResponse
	assert.NoError(t, json.Unmarshal(data, &decoded))
	assert.True(t, decoded.Ready)
	assert.Equal(t, []string{"opencode"}, decoded.ProvidersConnected)
	assert.Equal(t, 1, decoded.ProvidersConfigured)
}

func TestStatuszResponse_JSON(t *testing.T) {
	resp := StatuszResponse{
		Healthy:             true,
		Ready:               true,
		Connected:           []string{"opencode"},
		ProvidersConfigured: 1,
		SessionsActive:      3,
		SessionsError:       0,
		LastError:           "",
		AgentType:           "opencode",
		AgentVersion:        "1.2.27",
		UptimeSeconds:       7200,
	}
	data, err := json.Marshal(resp)
	assert.NoError(t, err)

	var decoded StatuszResponse
	assert.NoError(t, json.Unmarshal(data, &decoded))
	assert.Equal(t, 3, decoded.SessionsActive)
	assert.Equal(t, 7200, decoded.UptimeSeconds)
}

func TestReadyzResponse_NotReady(t *testing.T) {
	resp := ReadyzResponse{Ready: false, ProvidersConnected: []string{}}
	data, err := json.Marshal(resp)
	assert.NoError(t, err)
	assert.Contains(t, string(data), `"ready":false`)
}

func TestStatuszResponse_EmptyFields(t *testing.T) {
	resp := StatuszResponse{}
	data, err := json.Marshal(resp)
	assert.NoError(t, err)
	var decoded StatuszResponse
	assert.NoError(t, json.Unmarshal(data, &decoded))
	assert.Nil(t, decoded.Connected)
	assert.Empty(t, decoded.LastError)
}
