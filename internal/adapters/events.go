package adapters

import (
	"encoding/json"

	atypes "github.com/aethrolink/aethrolink-core/pkg/types"
)

// mapACPEventToTaskEvent normalizes adapter-local ACP events into the
// task event shape that the orchestrator persists.
func mapACPEventToTaskEvent(taskID string, raw map[string]any) atypes.TaskEvent {
	kind, _ := raw["kind"].(string)
	message, _ := raw["message"].(string)
	data, _ := raw["data"].(map[string]any)
	status := atypes.TaskStatusRunning
	eventKind := atypes.TaskEventTaskRunning
	switch kind {
	case "task.awaiting_input":
		status = atypes.TaskStatusAwaitingInput
		eventKind = atypes.TaskEventTaskAwaitingInput
	case "task.completed":
		status = atypes.TaskStatusCompleted
		eventKind = atypes.TaskEventTaskCompleted
	case "task.failed":
		status = atypes.TaskStatusFailed
		eventKind = atypes.TaskEventTaskFailed
	case "task.cancelled":
		status = atypes.TaskStatusCancelled
		eventKind = atypes.TaskEventTaskCancelled
	default:
		status = atypes.TaskStatusRunning
		eventKind = atypes.TaskEventTaskRunning
	}
	return atypes.TaskEvent{EventID: atypes.NewID(), TaskID: taskID, Kind: eventKind, State: status, Source: atypes.EventSourceAdapter, Message: message, Data: data, CreatedAt: atypes.NowUTC()}
}

// mapHTTPRunToTaskEvent converts polled HTTP runtime state into the same
// task event contract used by local/stdio adapters.
func mapHTTPRunToTaskEvent(taskID string, raw map[string]any) atypes.TaskEvent {
	status, _ := raw["status"].(string)
	data := map[string]any{}
	if result, ok := raw["result"].(map[string]any); ok {
		data["result"] = result
	}
	if reason, ok := raw["reason"].(string); ok && reason != "" {
		data["reason"] = reason
	}
	kind := atypes.TaskEventTaskRunning
	state := atypes.TaskStatusRunning
	message := "Task running"
	switch status {
	case "awaiting_input":
		kind = atypes.TaskEventTaskAwaitingInput
		state = atypes.TaskStatusAwaitingInput
		message = "Runtime requires additional input"
	case "completed":
		kind = atypes.TaskEventTaskCompleted
		state = atypes.TaskStatusCompleted
		message = "Task completed"
	case "failed":
		kind = atypes.TaskEventTaskFailed
		state = atypes.TaskStatusFailed
		message = "Task failed"
	case "cancelled":
		kind = atypes.TaskEventTaskCancelled
		state = atypes.TaskStatusCancelled
		message = "Task cancelled"
	}
	return atypes.TaskEvent{EventID: atypes.NewID(), TaskID: taskID, Kind: kind, State: state, Source: atypes.EventSourceAdapter, Message: message, Data: data, CreatedAt: atypes.NowUTC()}
}

func marshalPayloadJSON(v map[string]any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
