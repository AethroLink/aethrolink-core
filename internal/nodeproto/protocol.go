package nodeproto

import (
	"fmt"
	"time"

	"github.com/aethrolink/aethrolink-core/pkg/types"
)

// MessageType names the concrete node-to-node protocol payload family.
type MessageType string

const (
	MessageTypeTaskSubmit   MessageType = "task.submit"
	MessageTypeTaskAccepted MessageType = "task.accepted"
	MessageTypeTaskEvent    MessageType = "task.event"
	MessageTypeTaskResume   MessageType = "task.resume"
	MessageTypeTaskCancel   MessageType = "task.cancel"
	MessageTypeHealth       MessageType = "node.health"
	MessageTypeError        MessageType = "error"
)

// NodeHealthResponse is the minimal peer liveness contract exposed by node transport.
type NodeHealthResponse struct {
	NodeID    string    `json:"node_id"`
	OK        bool      `json:"ok"`
	Protocol  string    `json:"protocol"`
	CheckedAt time.Time `json:"checked_at"`
}

// MessageType returns the health discriminator for peer capability checks.
func (r NodeHealthResponse) MessageType() MessageType {
	return MessageTypeHealth
}

// TaskSubmitRequest is the typed remote execution request sent by an origin node.
type TaskSubmitRequest struct {
	OriginNodeID      string                `json:"origin_node_id"`
	OriginProxyTaskID string                `json:"origin_proxy_task_id"`
	OriginThreadID    string                `json:"origin_thread_id,omitempty"`
	TargetAgentID     string                `json:"target_agent_id"`
	Intent            string                `json:"intent"`
	Payload           map[string]any        `json:"payload"`
	RuntimeOptions    map[string]any        `json:"runtime_options,omitempty"`
	Trace             types.TraceContext    `json:"trace"`
	Delivery          *types.DeliveryPolicy `json:"delivery,omitempty"`
	SubmittedAt       time.Time             `json:"submitted_at"`
}

// MessageType returns the wire discriminator without requiring generic envelopes.
func (r TaskSubmitRequest) MessageType() MessageType {
	return MessageTypeTaskSubmit
}

// Validate enforces the ownership and routing fields needed before relay.
func (r TaskSubmitRequest) Validate() error {
	if r.OriginNodeID == "" {
		return fmt.Errorf("origin_node_id is required")
	}
	if r.OriginProxyTaskID == "" {
		return fmt.Errorf("origin_proxy_task_id is required")
	}
	if r.TargetAgentID == "" {
		return fmt.Errorf("target_agent_id is required")
	}
	if err := types.ValidateIntent(r.Intent); err != nil {
		return err
	}
	if r.Payload == nil {
		return fmt.Errorf("payload is required")
	}
	return nil
}

// TaskAcceptedResponse binds an origin proxy task to the destination task truth.
type TaskAcceptedResponse struct {
	OriginProxyTaskID   string    `json:"origin_proxy_task_id"`
	DestinationNodeID   string    `json:"destination_node_id"`
	DestinationTaskID   string    `json:"destination_task_id"`
	DestinationThreadID string    `json:"destination_thread_id,omitempty"`
	AcceptedAt          time.Time `json:"accepted_at"`
}

// MessageType returns the accepted discriminator for transport routing.
func (r TaskAcceptedResponse) MessageType() MessageType {
	return MessageTypeTaskAccepted
}

// Validate ensures accepted responses can create durable remote bindings.
func (r TaskAcceptedResponse) Validate() error {
	if r.OriginProxyTaskID == "" {
		return fmt.Errorf("origin_proxy_task_id is required")
	}
	if r.DestinationNodeID == "" {
		return fmt.Errorf("destination_node_id is required")
	}
	if r.DestinationTaskID == "" {
		return fmt.Errorf("destination_task_id is required")
	}
	return nil
}

// TaskEventFrame carries a destination event back to the origin proxy task.
type TaskEventFrame struct {
	OriginProxyTaskID   string              `json:"origin_proxy_task_id"`
	DestinationNodeID   string              `json:"destination_node_id"`
	DestinationTaskID   string              `json:"destination_task_id"`
	DestinationThreadID string              `json:"destination_thread_id,omitempty"`
	Seq                 int64               `json:"seq"`
	Kind                types.TaskEventKind `json:"kind"`
	State               types.TaskStatus    `json:"state"`
	Source              types.EventSource   `json:"source"`
	Message             string              `json:"message,omitempty"`
	Data                map[string]any      `json:"data,omitempty"`
	RemoteExecutionID   string              `json:"remote_execution_id,omitempty"`
	RemoteSessionID     string              `json:"remote_session_id,omitempty"`
	OccurredAt          time.Time           `json:"occurred_at"`
}

// MessageType returns the wire discriminator for remote lifecycle events.
func (f TaskEventFrame) MessageType() MessageType {
	return MessageTypeTaskEvent
}

// Validate ensures event frames can be attached to both local and remote records.
func (f TaskEventFrame) Validate() error {
	if f.OriginProxyTaskID == "" {
		return fmt.Errorf("origin_proxy_task_id is required")
	}
	if f.DestinationNodeID == "" {
		return fmt.Errorf("destination_node_id is required")
	}
	if f.DestinationTaskID == "" {
		return fmt.Errorf("destination_task_id is required")
	}
	if f.Kind == "" {
		return fmt.Errorf("kind is required")
	}
	if f.State == "" {
		return fmt.Errorf("state is required")
	}
	return nil
}

// ToLocalTaskEvent stores remote ownership metadata in the origin task event log.
func (f TaskEventFrame) ToLocalTaskEvent(eventID string, localSeq int64) types.TaskEvent {
	data := map[string]any{}
	for key, value := range f.Data {
		if isReservedEventDataKey(key) {
			continue
		}
		data[key] = value
	}
	// Canonical ownership fields are written after remote data so peers cannot spoof them.
	data["destination_node_id"] = f.DestinationNodeID
	data["destination_task_id"] = f.DestinationTaskID
	data["remote_event_seq"] = f.Seq
	if f.DestinationThreadID != "" {
		data["destination_thread_id"] = f.DestinationThreadID
	}
	if f.RemoteExecutionID != "" {
		data["remote_execution_id"] = f.RemoteExecutionID
	}
	if f.RemoteSessionID != "" {
		data["remote_session_id"] = f.RemoteSessionID
	}
	return types.TaskEvent{
		EventID:   eventID,
		TaskID:    f.OriginProxyTaskID,
		Seq:       localSeq,
		Kind:      f.Kind,
		State:     f.State,
		Source:    types.EventSourceTransport,
		Message:   f.Message,
		Data:      data,
		CreatedAt: f.OccurredAt,
	}
}

// isReservedEventDataKey protects origin-side audit fields from peer payload data.
func isReservedEventDataKey(key string) bool {
	switch key {
	case "destination_node_id", "destination_task_id", "destination_thread_id", "remote_execution_id", "remote_session_id", "remote_event_seq":
		return true
	default:
		return false
	}
}

// TaskResumeRequest asks the destination node to continue a known remote task.
type TaskResumeRequest struct {
	OriginNodeID      string             `json:"origin_node_id"`
	OriginProxyTaskID string             `json:"origin_proxy_task_id"`
	DestinationTaskID string             `json:"destination_task_id"`
	Payload           map[string]any     `json:"payload"`
	Trace             types.TraceContext `json:"trace"`
	RequestedAt       time.Time          `json:"requested_at"`
}

// MessageType returns the resume discriminator for node transport.
func (r TaskResumeRequest) MessageType() MessageType {
	return MessageTypeTaskResume
}

// Validate prevents ambiguous resume calls without both local and remote IDs.
func (r TaskResumeRequest) Validate() error {
	return validateControlIDs(r.OriginNodeID, r.OriginProxyTaskID, r.DestinationTaskID)
}

// TaskCancelRequest asks the destination node to cancel a known remote task.
type TaskCancelRequest struct {
	OriginNodeID      string             `json:"origin_node_id"`
	OriginProxyTaskID string             `json:"origin_proxy_task_id"`
	DestinationTaskID string             `json:"destination_task_id"`
	Reason            string             `json:"reason,omitempty"`
	Trace             types.TraceContext `json:"trace"`
	RequestedAt       time.Time          `json:"requested_at"`
}

// MessageType returns the cancel discriminator for node transport.
func (r TaskCancelRequest) MessageType() MessageType {
	return MessageTypeTaskCancel
}

// Validate prevents ambiguous cancellation without both local and remote IDs.
func (r TaskCancelRequest) Validate() error {
	return validateControlIDs(r.OriginNodeID, r.OriginProxyTaskID, r.DestinationTaskID)
}

// ErrorResponse is the typed failure surface for node protocol endpoints.
type ErrorResponse struct {
	MessageTypeHint MessageType `json:"message_type,omitempty"`
	Code            string      `json:"code"`
	Message         string      `json:"message"`
	Retryable       bool        `json:"retryable"`
}

// MessageType returns the error discriminator for failed node operations.
func (r ErrorResponse) MessageType() MessageType {
	return MessageTypeError
}

// validateControlIDs centralizes relay ID invariants for resume/cancel.
func validateControlIDs(originNodeID string, originProxyTaskID string, destinationTaskID string) error {
	if originNodeID == "" {
		return fmt.Errorf("origin_node_id is required")
	}
	if originProxyTaskID == "" {
		return fmt.Errorf("origin_proxy_task_id is required")
	}
	if destinationTaskID == "" {
		return fmt.Errorf("destination_task_id is required")
	}
	return nil
}
