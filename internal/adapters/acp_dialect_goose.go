package adapters

import (
	"fmt"

	atypes "github.com/aethrolink/aethrolink-core/pkg/types"
)

// gooseACPDialect adapts Goose CLI's ACP server to AethroLink's local runtime
// contract without inheriting Hermes- or OpenClaw-specific scoping rules.
type gooseACPDialect struct{}

// newGooseACPDialect installs Goose as a first-class ACP target.
func newGooseACPDialect() acpDialect { return gooseACPDialect{} }

// Name exposes the registry-facing dialect key.
func (gooseACPDialect) Name() string { return "goose" }

// Command reuses the configured Goose CLI launch command as-is.
func (gooseACPDialect) Command(spec atypes.RuntimeSpec, options map[string]any) ([]string, error) {
	if len(spec.Launch.Command) == 0 {
		return nil, fmt.Errorf("missing launch command for target %s", spec.TargetID)
	}
	return spec.Launch.Command, nil
}

// SubcontextKey scopes reusable Goose processes by profile so different Goose
// profile configs do not share one stdio worker accidentally.
func (gooseACPDialect) SubcontextKey(options map[string]any) string {
	return "profile:" + gooseProfile(options)
}

// StickyKey keeps Goose session reuse aligned with the caller conversation.
func (gooseACPDialect) StickyKey(task atypes.TaskEnvelope) string {
	if key := asString(task.RuntimeOptions, "session_key"); key != "" {
		return key
	}
	if task.ConversationID != "" {
		return task.ConversationID
	}
	return task.TaskID
}

// WorkspaceBinding asks Goose to open ACP sessions in the caller cwd with any
// client-supplied MCP servers attached.
func (gooseACPDialect) WorkspaceBinding(task atypes.TaskEnvelope) atypes.WorkspaceBinding {
	return atypes.WorkspaceBinding{
		CWD:        adapterCWD(task.RuntimeOptions),
		AttachMCP:  true,
		MCPServers: []map[string]any{},
		Metadata:   map[string]any{"profile": gooseProfile(task.RuntimeOptions)},
	}
}

// BindingName keeps persisted handles distinguishable from Hermes/OpenClaw ACP.
func (gooseACPDialect) BindingName() string { return "goose_acp" }

// BindingMetadata records the Goose profile and cwd that produced the session.
func (gooseACPDialect) BindingMetadata(options map[string]any, binding atypes.WorkspaceBinding) map[string]any {
	return map[string]any{"profile": gooseProfile(options), "cwd": binding.CWD}
}

// OpenSessionPayload follows Goose's documented ACP session/new shape.
func (gooseACPDialect) OpenSessionPayload(binding atypes.WorkspaceBinding, options map[string]any) map[string]any {
	return map[string]any{"cwd": binding.CWD, "mcpServers": binding.MCPServers}
}

// LoadSessionPayload follows Goose's documented ACP session/load shape.
func (gooseACPDialect) LoadSessionPayload(sessionID string, binding atypes.WorkspaceBinding, options map[string]any) map[string]any {
	return map[string]any{"sessionId": sessionID, "cwd": binding.CWD, "mcpServers": binding.MCPServers}
}

// PromptText prefers explicit caller text and falls back to a thin smoke-test prompt.
func (gooseACPDialect) PromptText(task atypes.TaskEnvelope) string {
	promptText := extractPromptTextFromPayload(task)
	if promptText == "" {
		return "Say exactly OK"
	}
	return promptText
}

// PromptPayload matches Goose's ACP prompt array format.
func (gooseACPDialect) PromptPayload(sessionID, promptText string, binding atypes.WorkspaceBinding, options map[string]any) map[string]any {
	return map[string]any{
		"sessionId": sessionID,
		"prompt":    []map[string]any{{"type": "text", "text": promptText}},
	}
}

// ResumePayload stays disabled until Goose resume semantics are proven live here.
func (gooseACPDialect) ResumePayload(sessionID string, payload map[string]any) (map[string]any, error) {
	return nil, fmt.Errorf("goose adapter does not support resume in this thin ACP slice")
}

// AcceptedEvent reports that Goose started executing the prompt.
func (gooseACPDialect) AcceptedEvent(task atypes.TaskEnvelope, sessionID string) atypes.LocalRuntimeEvent {
	return atypes.LocalRuntimeEvent{
		Kind:      atypes.LocalRuntimeEventProgress,
		State:     atypes.TaskStatusRunning,
		Message:   "Goose accepted the task",
		Data:      map[string]any{"profile": gooseProfile(task.RuntimeOptions), "session_id": sessionID},
		CreatedAt: atypes.NowUTC(),
	}
}

// CompletionEvent normalizes Goose terminal text into the shared task result shape.
func (gooseACPDialect) CompletionEvent(task atypes.TaskEnvelope, finalText string) atypes.LocalRuntimeEvent {
	return atypes.LocalRuntimeEvent{
		Kind:      atypes.LocalRuntimeEventTerminal,
		State:     atypes.TaskStatusCompleted,
		Message:   "Goose completed the task",
		Data:      map[string]any{"result": map[string]any{"text": finalText}},
		CreatedAt: atypes.NowUTC(),
	}
}

// AdapterState persists the Goose-specific session metadata needed for reloads.
func (gooseACPDialect) AdapterState(task atypes.TaskEnvelope, sessionID string) map[string]any {
	return map[string]any{
		"dialect":                 "goose",
		"profile":                 gooseProfile(task.RuntimeOptions),
		"session_id":              sessionID,
		"sticky_key":              gooseACPDialect{}.StickyKey(task),
		"session_idle_timeout_ms": task.RuntimeOptions["session_idle_timeout_ms"],
	}
}

// RehydrateState rebuilds Goose handle metadata from persisted task records.
func (gooseACPDialect) RehydrateState(task atypes.TaskRecord) map[string]any {
	return map[string]any{
		"dialect":    "goose",
		"profile":    gooseProfile(task.RuntimeOptions),
		"session_id": taskRemoteSessionID(task),
		"sticky_key": gooseACPDialect{}.StickyKey(atypes.TaskEnvelope{ConversationID: task.ConversationID, RuntimeOptions: task.RuntimeOptions, TaskID: task.TaskID}),
	}
}

// ShouldRecoverFinalText retries session replay when Goose completes without text.
func (gooseACPDialect) ShouldRecoverFinalText(finalText string, _ bool) bool {
	return adapterTrimText(finalText) == ""
}

// ShouldEmitSyntheticCompletion keeps Goose aligned with Hermes-style completion fallback.
func (gooseACPDialect) ShouldEmitSyntheticCompletion(string, bool, bool) bool {
	return true
}

// HandleNotification reuses generic ACP text-chunk handling for Goose updates.
func (gooseACPDialect) HandleNotification(params map[string]any, tracker *acpNotificationTracker) ([]atypes.LocalRuntimeEvent, bool) {
	if text, ok := chunkText(params); ok {
		tracker.AppendChunk(text)
	}
	return nil, false
}

// gooseProfile isolates per-profile Goose configuration with a stable default.
func gooseProfile(runtimeOptions map[string]any) string {
	profile := asString(runtimeOptions, "profile")
	if profile == "" {
		profile = "default"
	}
	return profile
}
