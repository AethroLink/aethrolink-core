package nodeproto

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/aethrolink/aethrolink-core/pkg/types"
)

func TestTaskSubmitRequestCarriesOriginAndDestinationOwnership(t *testing.T) {
	submit := TaskSubmitRequest{
		OriginNodeID:      "node-a",
		OriginProxyTaskID: "task-local-1",
		OriginThreadID:    "thread-origin-1",
		TargetAgentID:     "researcher",
		Intent:            "research.summary",
		Payload:           map[string]any{"topic": "multinode"},
		RuntimeOptions:    map[string]any{"executor": "research"},
		Trace:             types.TraceContext{TraceID: "trace-1"},
		Delivery:          types.DefaultDeliveryPolicy(),
		SubmittedAt:       time.Date(2026, 4, 24, 7, 0, 0, 0, time.UTC),
	}

	if err := submit.Validate(); err != nil {
		t.Fatalf("expected valid submit request: %v", err)
	}
	if submit.MessageType() != MessageTypeTaskSubmit {
		t.Fatalf("expected task.submit message type, got %q", submit.MessageType())
	}

	encoded, err := json.Marshal(submit)
	if err != nil {
		t.Fatalf("marshal submit request: %v", err)
	}
	var decoded TaskSubmitRequest
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal submit request: %v", err)
	}
	if decoded.OriginNodeID != "node-a" || decoded.OriginProxyTaskID != "task-local-1" || decoded.TargetAgentID != "researcher" {
		t.Fatalf("submit ownership fields were not preserved: %+v", decoded)
	}
}

func TestTaskEventFrameMapsRemoteExecutionToOriginProxyTask(t *testing.T) {
	event := TaskEventFrame{
		OriginProxyTaskID:   "task-local-1",
		DestinationNodeID:   "node-b",
		DestinationTaskID:   "task-remote-9",
		DestinationThreadID: "thread-remote-2",
		Seq:                 3,
		Kind:                types.TaskEventTaskCompleted,
		State:               types.TaskStatusCompleted,
		Source:              types.EventSourceRuntime,
		Message:             "remote task completed",
		RemoteExecutionID:   "run-9",
		RemoteSessionID:     "session-4",
		OccurredAt:          time.Date(2026, 4, 24, 7, 1, 0, 0, time.UTC),
	}

	if err := event.Validate(); err != nil {
		t.Fatalf("expected valid event frame: %v", err)
	}
	if event.MessageType() != MessageTypeTaskEvent {
		t.Fatalf("expected task.event message type, got %q", event.MessageType())
	}

	local := event.ToLocalTaskEvent("event-1")
	if local.TaskID != "task-local-1" {
		t.Fatalf("expected local proxy task id, got %q", local.TaskID)
	}
	if local.Source != types.EventSourceTransport {
		t.Fatalf("expected remote event to be stored as transport-sourced, got %q", local.Source)
	}
	if local.Data["destination_task_id"] != "task-remote-9" || local.Data["destination_node_id"] != "node-b" {
		t.Fatalf("expected destination ownership metadata, got %+v", local.Data)
	}
}

func TestAcceptedAndErrorResponsesHaveTypedMessageDiscriminators(t *testing.T) {
	accepted := TaskAcceptedResponse{
		OriginProxyTaskID:   "task-local-1",
		DestinationNodeID:   "node-b",
		DestinationTaskID:   "task-remote-9",
		DestinationThreadID: "thread-remote-2",
		AcceptedAt:          time.Date(2026, 4, 24, 7, 4, 0, 0, time.UTC),
	}
	errorResponse := ErrorResponse{
		MessageTypeHint: MessageTypeTaskSubmit,
		Code:            "peer_unreachable",
		Message:         "peer node is unreachable",
		Retryable:       true,
	}

	if err := accepted.Validate(); err != nil {
		t.Fatalf("expected valid accepted response: %v", err)
	}
	if accepted.MessageType() != MessageTypeTaskAccepted {
		t.Fatalf("expected task.accepted message type, got %q", accepted.MessageType())
	}
	if errorResponse.MessageType() != MessageTypeError {
		t.Fatalf("expected error message type, got %q", errorResponse.MessageType())
	}
}

func TestControlMessagesRequireOriginProxyAndDestinationTaskIDs(t *testing.T) {
	resume := TaskResumeRequest{
		OriginNodeID:      "node-a",
		OriginProxyTaskID: "task-local-1",
		DestinationTaskID: "task-remote-9",
		Payload:           map[string]any{"answer": "continue"},
		Trace:             types.TraceContext{TraceID: "trace-2"},
		RequestedAt:       time.Date(2026, 4, 24, 7, 2, 0, 0, time.UTC),
	}
	cancel := TaskCancelRequest{
		OriginNodeID:      "node-a",
		OriginProxyTaskID: "task-local-1",
		DestinationTaskID: "task-remote-9",
		Reason:            "operator requested",
		Trace:             types.TraceContext{TraceID: "trace-3"},
		RequestedAt:       time.Date(2026, 4, 24, 7, 3, 0, 0, time.UTC),
	}

	if err := resume.Validate(); err != nil {
		t.Fatalf("expected valid resume request: %v", err)
	}
	if resume.MessageType() != MessageTypeTaskResume {
		t.Fatalf("expected task.resume message type, got %q", resume.MessageType())
	}
	if err := cancel.Validate(); err != nil {
		t.Fatalf("expected valid cancel request: %v", err)
	}
	if cancel.MessageType() != MessageTypeTaskCancel {
		t.Fatalf("expected task.cancel message type, got %q", cancel.MessageType())
	}
}

func TestValidationRejectsUntargetedSubmit(t *testing.T) {
	submit := TaskSubmitRequest{
		OriginNodeID:      "node-a",
		OriginProxyTaskID: "task-local-1",
		Intent:            "research.summary",
	}

	if err := submit.Validate(); err == nil {
		t.Fatal("expected missing target agent to fail validation")
	}
}
