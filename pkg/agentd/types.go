package agentd

type HealthzResponse struct {
	Healthy       bool   `json:"healthy"`
	Version       string `json:"version"`
	UptimeSeconds int    `json:"uptime_seconds"`
}

type ReadyzResponse struct {
	Ready               bool     `json:"ready"`
	ProvidersConnected  []string `json:"providers_connected"`
	ProvidersConfigured int      `json:"providers_configured"`
	AgentVersion        string   `json:"agent_version"`
	AgentType           string   `json:"agent_type"`
}

type StatuszResponse struct {
	Healthy             bool     `json:"healthy"`
	Ready               bool     `json:"ready"`
	Connected           []string `json:"connected"`
	ProvidersConfigured int      `json:"providers_configured"`
	SessionsActive      int      `json:"sessions_active"`
	SessionsError       int      `json:"sessions_error"`
	LastError           string   `json:"last_error"`
	AgentType           string   `json:"agent_type"`
	AgentVersion        string   `json:"agent_version"`
	UptimeSeconds       int      `json:"uptime_seconds"`
}
