package types

import "time"

// ThreadStatus tracks the durable lifecycle of a two-agent conversation thread.
type ThreadStatus string

const (
	ThreadStatusActive      ThreadStatus = "active"
	ThreadStatusPaused      ThreadStatus = "paused"
	ThreadStatusInterrupted ThreadStatus = "interrupted"
	ThreadStatusCompleted   ThreadStatus = "completed"
	ThreadStatusFailed      ThreadStatus = "failed"
	ThreadStatusCancelled   ThreadStatus = "cancelled"
)

// ThreadRecord is the first-class persisted continuity object above tasks.
type ThreadRecord struct {
	ThreadID          string         `json:"thread_id"`
	AgentAID          string         `json:"agent_a_id"`
	AgentBID          string         `json:"agent_b_id"`
	Status            ThreadStatus   `json:"status"`
	ContinuityKey     string         `json:"continuity_key"`
	LastTaskID        string         `json:"last_task_id,omitempty"`
	LastActorAgentID  string         `json:"last_actor_agent_id,omitempty"`
	LastTargetAgentID string         `json:"last_target_agent_id,omitempty"`
	Metadata          map[string]any `json:"metadata,omitempty"`
	CreatedAt         time.Time      `json:"created_at"`
	UpdatedAt         time.Time      `json:"updated_at"`
}

// ThreadTurn records one ordered hop inside a durable thread boundary.
type ThreadTurn struct {
	ThreadID            string      `json:"thread_id"`
	TurnIndex           int64       `json:"turn_index"`
	TaskID              string      `json:"task_id,omitempty"`
	SenderAgentID       string      `json:"sender_agent_id"`
	TargetAgentID       string      `json:"target_agent_id"`
	TargetOwner         TargetOwner `json:"target_owner,omitempty"`
	RemotePeerID        string      `json:"remote_peer_id,omitempty"`
	DestinationNodeID   string      `json:"destination_node_id,omitempty"`
	DestinationTaskID   string      `json:"destination_task_id,omitempty"`
	DestinationThreadID string      `json:"destination_thread_id,omitempty"`
	RemoteSessionID     string      `json:"remote_session_id,omitempty"`
	RemoteExecutionID   string      `json:"remote_execution_id,omitempty"`
	Status              string      `json:"status"`
	CreatedAt           time.Time   `json:"created_at"`
	UpdatedAt           time.Time   `json:"updated_at"`
}

// ThreadParticipantOwnership makes local-vs-peer ownership explicit in inspect output.
type ThreadParticipantOwnership struct {
	AgentID string      `json:"agent_id"`
	Owner   TargetOwner `json:"owner"`
	PeerID  string      `json:"peer_id,omitempty"`
	NodeID  string      `json:"node_id,omitempty"`
}

// ThreadContinuitySide shows one agent's current reusable continuity state.
type ThreadContinuitySide struct {
	AgentID               string           `json:"agent_id"`
	LastRemoteSessionID   string           `json:"last_remote_session_id,omitempty"`
	LastRemoteExecutionID string           `json:"last_remote_execution_id,omitempty"`
	SessionBindings       []SessionBinding `json:"session_bindings,omitempty"`
}

// ThreadNextContinue describes what the next manual continue call would target.
type ThreadNextContinue struct {
	SenderAgentID          string `json:"sender_agent_id"`
	TargetAgentID          string `json:"target_agent_id"`
	WillReuseRemoteSession bool   `json:"will_reuse_remote_session"`
}

// ThreadInspection bundles the operator-facing continuity view for one thread.
type ThreadInspection struct {
	Thread             ThreadRecord                 `json:"thread"`
	Turns              []ThreadTurn                 `json:"turns"`
	Participants       []ThreadParticipantOwnership `json:"participants"`
	Continuity         []ThreadContinuitySide       `json:"continuity"`
	NextContinue       ThreadNextContinue           `json:"next_continue"`
	InterruptionReason string                       `json:"interruption_reason,omitempty"`
}

// ThreadCreateRequest creates a durable two-agent thread boundary.
type ThreadCreateRequest struct {
	AgentAID      string         `json:"agent_a_id"`
	AgentBID      string         `json:"agent_b_id"`
	ContinuityKey string         `json:"continuity_key,omitempty"`
	Metadata      map[string]any `json:"metadata,omitempty"`
}

// Normalize fills defaults for thread creation without inventing participants.
func (r *ThreadCreateRequest) Normalize() {
	if r.Metadata == nil {
		r.Metadata = map[string]any{}
	}
	if r.ContinuityKey == "" && r.AgentAID != "" && r.AgentBID != "" {
		r.ContinuityKey = r.AgentAID + ":" + r.AgentBID
	}
}

// ThreadContinueRequest describes one explicit next turn on a thread.
type ThreadContinueRequest struct {
	Sender                string                    `json:"sender"`
	TargetAgentID         string                    `json:"target_agent_id,omitempty"`
	Intent                string                    `json:"intent"`
	IntentByAgent         map[string]string         `json:"intent_by_agent,omitempty"`
	Payload               map[string]any            `json:"payload"`
	PayloadByAgent        map[string]map[string]any `json:"payload_by_agent,omitempty"`
	RuntimeOptions        map[string]any            `json:"runtime_options,omitempty"`
	ConversationID        string                    `json:"conversation_id,omitempty"`
	Delivery              *DeliveryPolicy           `json:"delivery,omitempty"`
	AutoContinue          bool                      `json:"auto_continue,omitempty"`
	MaxTurns              int                       `json:"max_turns,omitempty"`
	StopOnAwaitingInput   bool                      `json:"stop_on_awaiting_input,omitempty"`
	StopOnSameActorRepeat bool                      `json:"stop_on_same_actor_repeat,omitempty"`
	StopOnTerminalError   bool                      `json:"stop_on_terminal_error,omitempty"`
	Metadata              map[string]any            `json:"metadata,omitempty"`
}

// Normalize fills default maps and delivery for explicit thread continuation.
func (r *ThreadContinueRequest) Normalize() {
	if r.Payload == nil {
		r.Payload = map[string]any{}
	}
	if r.IntentByAgent == nil {
		r.IntentByAgent = map[string]string{}
	}
	if r.PayloadByAgent == nil {
		r.PayloadByAgent = map[string]map[string]any{}
	}
	if r.RuntimeOptions == nil {
		r.RuntimeOptions = map[string]any{}
	}
	if r.Metadata == nil {
		r.Metadata = map[string]any{}
	}
	if r.Delivery == nil {
		d := DefaultDeliveryPolicy()
		r.Delivery = &d
	}
	if r.MaxTurns <= 0 {
		r.MaxTurns = 1
	}
}

// IntentForAgent returns the request intent for one actor with per-agent override.
func (r ThreadContinueRequest) IntentForAgent(agentID string) string {
	if intent, ok := r.IntentByAgent[agentID]; ok && intent != "" {
		return intent
	}
	return r.Intent
}

// PayloadForAgent returns the request payload for one actor with per-agent override.
func (r ThreadContinueRequest) PayloadForAgent(agentID string) map[string]any {
	if payload, ok := r.PayloadByAgent[agentID]; ok && payload != nil {
		return payload
	}
	return r.Payload
}
