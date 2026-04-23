package adapters

import (
	"fmt"

	atypes "github.com/aethrolink/aethrolink-core/pkg/types"
)

type openClawACPDialect struct{}

func newOpenClawACPDialect() acpDialect { return openClawACPDialect{} }

func (openClawACPDialect) Name() string { return "openclaw" }

func (openClawACPDialect) Command(spec atypes.RuntimeSpec, options map[string]any) ([]string, error) {
	if len(spec.Launch.Command) == 0 {
		return nil, fmt.Errorf("missing launch command for target %s", spec.TargetID)
	}
	return spec.Launch.Command, nil
}

func (openClawACPDialect) SubcontextKey(options map[string]any) string {
	return "session:" + openClawSessionKey(options)
}

func (openClawACPDialect) StickyKey(task atypes.TaskEnvelope) string {
	if task.ThreadID != "" {
		return task.ThreadID
	}
	return openClawSessionKey(task.RuntimeOptions)
}

func (openClawACPDialect) WorkspaceBinding(task atypes.TaskEnvelope) atypes.WorkspaceBinding {
	return atypes.WorkspaceBinding{
		CWD:        adapterCWD(task.RuntimeOptions),
		AttachMCP:  false,
		MCPServers: []map[string]any{},
		Metadata:   map[string]any{"session_key": openClawSessionKey(task.RuntimeOptions)},
	}
}

func (openClawACPDialect) BindingName() string { return "acp_client_stdio" }

func (openClawACPDialect) BindingMetadata(options map[string]any, binding atypes.WorkspaceBinding) map[string]any {
	return map[string]any{"session_key": openClawSessionKey(options), "cwd": binding.CWD}
}

func (openClawACPDialect) OpenSessionPayload(binding atypes.WorkspaceBinding, options map[string]any) map[string]any {
	return map[string]any{"cwd": binding.CWD, "mcpServers": binding.MCPServers}
}

func (openClawACPDialect) LoadSessionPayload(sessionID string, binding atypes.WorkspaceBinding, options map[string]any) map[string]any {
	return map[string]any{"sessionId": sessionID, "cwd": binding.CWD, "mcpServers": binding.MCPServers}
}

func (openClawACPDialect) PromptText(task atypes.TaskEnvelope) string {
	promptText := extractPromptTextFromPayload(task)
	if promptText == "" && len(task.Payload) > 0 {
		promptText, _ = marshalPayloadJSON(task.Payload)
	}
	if promptText == "" {
		return "Say exactly OPENCLAW OK"
	}
	return promptText
}

func (openClawACPDialect) PromptPayload(sessionID, promptText string, binding atypes.WorkspaceBinding, options map[string]any) map[string]any {
	return map[string]any{
		"sessionId": sessionID,
		"prompt":    []map[string]any{{"type": "text", "text": promptText}},
	}
}

func (openClawACPDialect) ResumePayload(sessionID string, payload map[string]any) (map[string]any, error) {
	return nil, fmt.Errorf("openclaw adapter does not support resume in this real ACP bridge slice")
}

func (openClawACPDialect) AcceptedEvent(task atypes.TaskEnvelope, sessionID string) atypes.LocalRuntimeEvent {
	return atypes.LocalRuntimeEvent{
		Kind:      atypes.LocalRuntimeEventProgress,
		State:     atypes.TaskStatusRunning,
		Message:   "OpenClaw accepted the task",
		Data:      map[string]any{"session_key": openClawStickySessionKey(task), "session_id": sessionID},
		CreatedAt: atypes.NowUTC(),
	}
}

func (openClawACPDialect) CompletionEvent(task atypes.TaskEnvelope, finalText string) atypes.LocalRuntimeEvent {
	return atypes.LocalRuntimeEvent{
		Kind:      atypes.LocalRuntimeEventTerminal,
		State:     atypes.TaskStatusCompleted,
		Message:   "OpenClaw completed the task",
		Data:      map[string]any{"result": map[string]any{"text": finalText}},
		CreatedAt: atypes.NowUTC(),
	}
}

func (openClawACPDialect) AdapterState(task atypes.TaskEnvelope, sessionID string) map[string]any {
	sessionKey := openClawStickySessionKey(task)
	return map[string]any{
		"dialect":                 "openclaw",
		"session_key":             sessionKey,
		"sticky_key":              sessionKey,
		"session_idle_timeout_ms": task.RuntimeOptions["session_idle_timeout_ms"],
	}
}

func (openClawACPDialect) RehydrateState(task atypes.TaskRecord) map[string]any {
	sessionKey := openClawStickySessionKey(atypes.TaskEnvelope{ThreadID: task.ThreadID, ConversationID: task.ConversationID, RuntimeOptions: task.RuntimeOptions, TaskID: task.TaskID})
	return map[string]any{"dialect": "openclaw", "session_key": sessionKey, "sticky_key": sessionKey}
}

func (openClawACPDialect) ShouldRecoverFinalText(finalText string, sawStructured bool) bool {
	return adapterTrimText(finalText) == "" && sawStructured
}

func (openClawACPDialect) ShouldEmitSyntheticCompletion(finalText string, sawStructured bool, sawTerminal bool) bool {
	if sawTerminal {
		return false
	}
	return !(sawStructured && adapterTrimText(finalText) == "")
}

func (openClawACPDialect) HandleNotification(params map[string]any, tracker *acpNotificationTracker) ([]atypes.LocalRuntimeEvent, bool) {
	if text, ok := chunkText(params); ok {
		tracker.AppendChunk(text)
		return nil, false
	}
	event, ok := params["event"].(map[string]any)
	if !ok || event == nil {
		return nil, false
	}
	tracker.MarkStructuredEvent()
	mapped := acpNotificationToRuntimeEvent(event)
	return []atypes.LocalRuntimeEvent{mapped}, mapped.State.IsTerminal()
}

func openClawSessionKey(runtimeOptions map[string]any) string {
	sessionKey := asString(runtimeOptions, "session_key")
	if sessionKey == "" {
		sessionKey = "main"
	}
	return sessionKey
}

func openClawStickySessionKey(task atypes.TaskEnvelope) string {
	if task.ThreadID != "" {
		return task.ThreadID
	}
	return openClawSessionKey(task.RuntimeOptions)
}
