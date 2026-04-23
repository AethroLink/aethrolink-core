package adapters

import (
	"fmt"

	atypes "github.com/aethrolink/aethrolink-core/pkg/types"
)

type hermesACPDialect struct{}

func newHermesACPDialect() acpDialect { return hermesACPDialect{} }

func (hermesACPDialect) Name() string { return "hermes" }

func (hermesACPDialect) Command(spec atypes.RuntimeSpec, options map[string]any) ([]string, error) {
	if len(spec.Launch.Commands) > 0 {
		executor := hermesExecutor(options)
		if command := spec.Launch.Commands[executor]; len(command) > 0 {
			return command, nil
		}
		return nil, fmt.Errorf("missing hermes command for executor %s", executor)
	}
	if len(spec.Launch.Command) == 0 {
		return nil, fmt.Errorf("missing launch command for target %s", spec.TargetID)
	}
	return spec.Launch.Command, nil
}

func (hermesACPDialect) SubcontextKey(options map[string]any) string {
	return "executor:" + hermesExecutor(options)
}

func (hermesACPDialect) StickyKey(task atypes.TaskEnvelope) string {
	if key := asString(task.RuntimeOptions, "session_key"); key != "" {
		return key
	}
	if task.ConversationID != "" {
		return task.ConversationID
	}
	return task.TaskID
}

func (hermesACPDialect) WorkspaceBinding(task atypes.TaskEnvelope) atypes.WorkspaceBinding {
	return atypes.WorkspaceBinding{
		CWD:        adapterCWD(task.RuntimeOptions),
		AttachMCP:  true,
		MCPServers: []map[string]any{},
		Metadata:   map[string]any{"executor": hermesExecutor(task.RuntimeOptions)},
	}
}

func (hermesACPDialect) BindingName() string { return "hermes_acp" }

func (hermesACPDialect) BindingMetadata(options map[string]any, binding atypes.WorkspaceBinding) map[string]any {
	return map[string]any{"executor": hermesExecutor(options), "cwd": binding.CWD}
}

func (hermesACPDialect) OpenSessionPayload(binding atypes.WorkspaceBinding, options map[string]any) map[string]any {
	return map[string]any{"cwd": binding.CWD, "mcpServers": binding.MCPServers}
}

func (hermesACPDialect) LoadSessionPayload(sessionID string, binding atypes.WorkspaceBinding, options map[string]any) map[string]any {
	return map[string]any{"sessionId": sessionID}
}

func (hermesACPDialect) PromptText(task atypes.TaskEnvelope) string {
	promptText := extractPromptTextFromPayload(task)
	if promptText == "" {
		return "Say exactly OK"
	}
	return promptText
}

func (hermesACPDialect) PromptPayload(sessionID, promptText string, binding atypes.WorkspaceBinding, options map[string]any) map[string]any {
	return map[string]any{
		"sessionId": sessionID,
		"prompt":    []map[string]any{{"type": "text", "text": promptText}},
	}
}

func (hermesACPDialect) ResumePayload(sessionID string, payload map[string]any) (map[string]any, error) {
	return nil, fmt.Errorf("hermes adapter does not support resume in this thin real-runtime slice")
}

func (hermesACPDialect) AcceptedEvent(task atypes.TaskEnvelope, sessionID string) atypes.LocalRuntimeEvent {
	return atypes.LocalRuntimeEvent{
		Kind:      atypes.LocalRuntimeEventProgress,
		State:     atypes.TaskStatusRunning,
		Message:   "Hermes accepted the task",
		Data:      map[string]any{"executor": hermesExecutor(task.RuntimeOptions), "session_id": sessionID},
		CreatedAt: atypes.NowUTC(),
	}
}

func (hermesACPDialect) CompletionEvent(task atypes.TaskEnvelope, finalText string) atypes.LocalRuntimeEvent {
	return atypes.LocalRuntimeEvent{
		Kind:      atypes.LocalRuntimeEventTerminal,
		State:     atypes.TaskStatusCompleted,
		Message:   "Hermes completed the task",
		Data:      map[string]any{"result": map[string]any{"text": finalText}},
		CreatedAt: atypes.NowUTC(),
	}
}

func (hermesACPDialect) AdapterState(task atypes.TaskEnvelope, sessionID string) map[string]any {
	return map[string]any{
		"dialect":                 "hermes",
		"executor":                hermesExecutor(task.RuntimeOptions),
		"session_id":              sessionID,
		"sticky_key":              hermesACPDialect{}.StickyKey(task),
		"session_idle_timeout_ms": task.RuntimeOptions["session_idle_timeout_ms"],
	}
}

func (hermesACPDialect) RehydrateState(task atypes.TaskRecord) map[string]any {
	return map[string]any{
		"dialect":    "hermes",
		"executor":   hermesExecutor(task.RuntimeOptions),
		"session_id": taskRemoteSessionID(task),
		"sticky_key": hermesACPDialect{}.StickyKey(atypes.TaskEnvelope{ConversationID: task.ConversationID, RuntimeOptions: task.RuntimeOptions, TaskID: task.TaskID}),
	}
}

func (hermesACPDialect) ShouldRecoverFinalText(finalText string, _ bool) bool {
	return adapterTrimText(finalText) == ""
}

func (hermesACPDialect) ShouldEmitSyntheticCompletion(string, bool, bool) bool {
	return true
}

func (hermesACPDialect) HandleNotification(params map[string]any, tracker *acpNotificationTracker) ([]atypes.LocalRuntimeEvent, bool) {
	if text, ok := chunkText(params); ok {
		tracker.AppendChunk(text)
	}
	return nil, false
}

func hermesExecutor(runtimeOptions map[string]any) string {
	executor := asString(runtimeOptions, "executor")
	if executor == "" {
		executor = "aethrolink-agent"
	}
	return executor
}
