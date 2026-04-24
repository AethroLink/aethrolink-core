package types

import "time"

// PeerStatus tracks node liveness separately from durable peer registration.
type PeerStatus string

const (
	PeerStatusOnline  PeerStatus = "online"
	PeerStatusOffline PeerStatus = "offline"
)

// PeerRecord is the local node's durable view of another AethroLink node.
type PeerRecord struct {
	PeerID       string         `json:"peer_id"`
	DisplayName  string         `json:"display_name"`
	BaseURL      string         `json:"base_url"`
	Status       PeerStatus     `json:"status"`
	Capabilities []string       `json:"capabilities,omitempty"`
	Metadata     map[string]any `json:"metadata,omitempty"`
	RegisteredAt time.Time      `json:"registered_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
	LastSeenAt   time.Time      `json:"last_seen_at"`
}

// PeerUpsertRequest is the operator-facing static peer registration payload.
type PeerUpsertRequest struct {
	PeerID       string         `json:"peer_id"`
	DisplayName  string         `json:"display_name,omitempty"`
	BaseURL      string         `json:"base_url"`
	Capabilities []string       `json:"capabilities,omitempty"`
	Metadata     map[string]any `json:"metadata,omitempty"`
}

// PeerTargetStatus describes a cached exported target independently of peer liveness.
type PeerTargetStatus string

const (
	PeerTargetStatusAvailable   PeerTargetStatus = "available"
	PeerTargetStatusUnavailable PeerTargetStatus = "unavailable"
)

// PeerTargetRecord caches a target exported by a peer for local discovery.
type PeerTargetRecord struct {
	PeerID       string           `json:"peer_id"`
	TargetID     string           `json:"target_id"`
	DisplayName  string           `json:"display_name"`
	Capabilities []string         `json:"capabilities,omitempty"`
	Defaults     map[string]any   `json:"defaults,omitempty"`
	Metadata     map[string]any   `json:"metadata,omitempty"`
	Status       PeerTargetStatus `json:"status"`
	SyncedAt     time.Time        `json:"synced_at"`
}

// PeerSyncResponse reports the target cache refresh result for one peer.
type PeerSyncResponse struct {
	Peer    PeerRecord         `json:"peer"`
	Targets []PeerTargetRecord `json:"targets"`
}

const (
	// RemoteRelayStatusPending means the origin accepted relay work but has not confirmed stream handoff.
	RemoteRelayStatusPending = "relay_pending"
	// RemoteRelayStatusStreaming means the origin is actively mirroring the destination event stream.
	RemoteRelayStatusStreaming = "relay_streaming"
	// RemoteRelayStatusInterrupted means restart or stream loss made relay completion unknown.
	RemoteRelayStatusInterrupted = "relay_interrupted"
)

// RemoteTaskBinding records how an origin proxy task maps to destination execution.
type RemoteTaskBinding struct {
	LocalTaskID         string    `json:"local_task_id"`
	RemotePeerID        string    `json:"remote_peer_id"`
	DestinationNodeID   string    `json:"destination_node_id"`
	DestinationTaskID   string    `json:"destination_task_id"`
	DestinationThreadID string    `json:"destination_thread_id,omitempty"`
	Status              string    `json:"status"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

// TargetOwner marks whether discovery resolved a local runtime or peer-owned target.
type TargetOwner string

const (
	TargetOwnerLocal  TargetOwner = "local"
	TargetOwnerRemote TargetOwner = "remote"
)
