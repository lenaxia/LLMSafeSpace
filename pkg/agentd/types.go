package agentd

// HealthzResponse is the response for GET /v1/healthz.
type HealthzResponse struct {
	Healthy       bool   `json:"healthy"`
	Version       string `json:"version"`
	UptimeSeconds int    `json:"uptime_seconds"`
}

// ReadyzResponse is the response for GET /v1/readyz.
type ReadyzResponse struct {
	Ready               bool     `json:"ready"`
	ProvidersConnected  []string `json:"providers_connected"`
	ProvidersConfigured int      `json:"providers_configured"`
	AgentVersion        string   `json:"agent_version"`
	AgentType           string   `json:"agent_type"`
}

// SessionInfo describes a single opencode session.
type SessionInfo struct {
	ID     string `json:"id"`
	Title  string `json:"title,omitempty"`
	Status string `json:"status"` // "idle" | "busy"
}

// DiskUsage reports workspace filesystem usage.
type DiskUsage struct {
	UsedBytes  int64 `json:"used_bytes"`
	TotalBytes int64 `json:"total_bytes"`
}

// StatuszResponse is the response for GET /v1/statusz.
type StatuszResponse struct {
	Healthy             bool          `json:"healthy"`
	Ready               bool          `json:"ready"`
	Connected           []string      `json:"connected"`
	ProvidersConfigured int           `json:"providers_configured"`
	Sessions            []SessionInfo `json:"sessions"`
	SessionsActive      int           `json:"sessions_active"`
	SessionsError       int           `json:"sessions_error"`
	LastError           string        `json:"last_error"`
	AgentType           string        `json:"agent_type"`
	AgentVersion        string        `json:"agent_version"`
	UptimeSeconds       int           `json:"uptime_seconds"`
	Disk                *DiskUsage    `json:"disk,omitempty"`
}
