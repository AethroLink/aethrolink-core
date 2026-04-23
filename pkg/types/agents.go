package types

import "time"

type AgentStatus string

const (
	AgentStatusOnline  AgentStatus = "online"
	AgentStatusOffline AgentStatus = "offline"
)

const DefaultAgentLeaseTTL = 5 * time.Minute

type AgentRecord struct {
	AgentID        string         `json:"agent_id"`
	DisplayName    string         `json:"display_name"`
	TransportKind  string         `json:"transport_kind"`
	Endpoint       string         `json:"endpoint,omitempty"`
	Adapter        string         `json:"adapter,omitempty"`
	Dialect        string         `json:"dialect,omitempty"`
	Healthcheck    string         `json:"healthcheck,omitempty"`
	Launch         LaunchSpec     `json:"launch"`
	Defaults       map[string]any `json:"defaults,omitempty"`
	Capabilities   []string       `json:"capabilities,omitempty"`
	StickyMode     string         `json:"sticky_mode,omitempty"`
	Metadata       map[string]any `json:"metadata,omitempty"`
	Status         AgentStatus    `json:"status"`
	RegisteredAt   time.Time      `json:"registered_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
	LastSeenAt     time.Time      `json:"last_seen_at"`
	LeaseExpiresAt time.Time      `json:"lease_expires_at"`
}

func (r AgentRecord) EffectiveStatus(now time.Time) AgentStatus {
	if now.After(r.LeaseExpiresAt) {
		return AgentStatusOffline
	}
	return AgentStatusOnline
}

func (r AgentRecord) WithEffectiveStatus(now time.Time) AgentRecord {
	r.Status = r.EffectiveStatus(now)
	return r
}

type AgentRegistrationRequest struct {
	AgentID         string         `json:"agent_id,omitempty"`
	DisplayName     string         `json:"display_name"`
	TransportKind   string         `json:"transport_kind"`
	Endpoint        string         `json:"endpoint,omitempty"`
	Adapter         string         `json:"adapter,omitempty"`
	Dialect         string         `json:"dialect,omitempty"`
	Healthcheck     string         `json:"healthcheck,omitempty"`
	Launch          LaunchSpec     `json:"launch"`
	Defaults        map[string]any `json:"defaults,omitempty"`
	Capabilities    []string       `json:"capabilities,omitempty"`
	StickyMode      string         `json:"sticky_mode,omitempty"`
	Metadata        map[string]any `json:"metadata,omitempty"`
	LeaseTTLSeconds int            `json:"lease_ttl_seconds,omitempty"`
}

func (r *AgentRegistrationRequest) Normalize() {
	if r.AgentID == "" {
		r.AgentID = NewID()
	}
	if r.Capabilities == nil {
		r.Capabilities = []string{}
	}
	if r.Defaults == nil {
		r.Defaults = map[string]any{}
	}
	if r.Metadata == nil {
		r.Metadata = map[string]any{}
	}
	if r.LeaseTTLSeconds <= 0 {
		r.LeaseTTLSeconds = int(DefaultAgentLeaseTTL / time.Second)
	}
}

type AgentHeartbeatRequest struct {
	LeaseTTLSeconds int `json:"lease_ttl_seconds,omitempty"`
}

func (r *AgentHeartbeatRequest) Normalize() {
	if r.LeaseTTLSeconds <= 0 {
		r.LeaseTTLSeconds = int(DefaultAgentLeaseTTL / time.Second)
	}
}
