package adapters

import (
	"encoding/json"

	atypes "github.com/aethrolink/aethrolink-core/pkg/types"
)

// runtimeEventToTaskEvent converts a normalized local runtime event into the
// orchestrator-facing persisted task event shape.
func runtimeEventToTaskEvent(taskID string, event atypes.LocalRuntimeEvent) atypes.TaskEvent {
	return atypes.TaskEvent{
		EventID:   atypes.NewID(),
		TaskID:    taskID,
		Kind:      runtimeEventKindToTaskEventKind(event),
		State:     event.State,
		Source:    atypes.EventSourceAdapter,
		Message:   event.Message,
		Data:      cloneRuntimeEventData(event.Data),
		CreatedAt: event.CreatedAt,
	}
}

func runtimeEventKindToTaskEventKind(event atypes.LocalRuntimeEvent) atypes.TaskEventKind {
	if event.Kind == atypes.LocalRuntimeEventTerminal {
		switch event.State {
		case atypes.TaskStatusCompleted:
			return atypes.TaskEventTaskCompleted
		case atypes.TaskStatusFailed:
			return atypes.TaskEventTaskFailed
		case atypes.TaskStatusCancelled:
			return atypes.TaskEventTaskCancelled
		}
	}
	if event.State == atypes.TaskStatusAwaitingInput {
		return atypes.TaskEventTaskAwaitingInput
	}
	return atypes.TaskEventTaskRunning
}

// acpNotificationToRuntimeEvent normalizes ACP adapter-local notifications into
// the runtime-neutral event shape used by the local-first adapter stack.
func acpNotificationToRuntimeEvent(raw map[string]any) atypes.LocalRuntimeEvent {
	kind, _ := raw["kind"].(string)
	message, _ := raw["message"].(string)
	data, _ := raw["data"].(map[string]any)
	state := atypes.TaskStatusRunning
	eventKind := atypes.LocalRuntimeEventProgress
	switch kind {
	case "task.awaiting_input":
		state = atypes.TaskStatusAwaitingInput
		eventKind = atypes.LocalRuntimeEventStateChange
	case "task.completed":
		state = atypes.TaskStatusCompleted
		eventKind = atypes.LocalRuntimeEventTerminal
	case "task.failed":
		state = atypes.TaskStatusFailed
		eventKind = atypes.LocalRuntimeEventTerminal
	case "task.cancelled":
		state = atypes.TaskStatusCancelled
		eventKind = atypes.LocalRuntimeEventTerminal
	}
	return atypes.LocalRuntimeEvent{
		Kind:      eventKind,
		State:     state,
		Message:   message,
		Data:      cloneRuntimeEventData(data),
		CreatedAt: atypes.NowUTC(),
	}
}

func marshalPayloadJSON(v map[string]any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func cloneRuntimeEventData(in map[string]any) map[string]any {
	if in == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		if nested, ok := value.(map[string]any); ok {
			out[key] = cloneRuntimeEventData(nested)
			continue
		}
		out[key] = value
	}
	return out
}
