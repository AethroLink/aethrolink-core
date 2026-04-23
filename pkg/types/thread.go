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
	ThreadID          string    `json:"thread_id"`
	TurnIndex         int64     `json:"turn_index"`
	TaskID            string    `json:"task_id,omitempty"`
	SenderAgentID     string    `json:"sender_agent_id"`
	TargetAgentID     string    `json:"target_agent_id"`
	RemoteSessionID   string    `json:"remote_session_id,omitempty"`
	RemoteExecutionID string    `json:"remote_execution_id,omitempty"`
	Status            string    `json:"status"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
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
	Sender         string          `json:"sender"`
	TargetAgentID  string          `json:"target_agent_id,omitempty"`
	Intent         string          `json:"intent"`
	Payload        map[string]any  `json:"payload"`
	RuntimeOptions map[string]any  `json:"runtime_options,omitempty"`
	ConversationID string          `json:"conversation_id,omitempty"`
	Delivery       *DeliveryPolicy `json:"delivery,omitempty"`
	Metadata       map[string]any  `json:"metadata,omitempty"`
}

// Normalize fills default maps and delivery for explicit thread continuation.
func (r *ThreadContinueRequest) Normalize() {
	if r.Payload == nil {
		r.Payload = map[string]any{}
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
}
