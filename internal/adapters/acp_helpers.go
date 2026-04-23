package adapters

import (
	"strings"
	"time"

	atypes "github.com/aethrolink/aethrolink-core/pkg/types"
)

// extractPromptTextFromPayload keeps the shared ACP adapter focused on runtime
// transport while dialects decide how to interpret the resolved prompt text.
func extractPromptTextFromPayload(task atypes.TaskEnvelope) string {
	if text, ok := task.Payload["text"].(string); ok && text != "" {
		return text
	}
	if prompt, ok := task.Payload["prompt"].(string); ok && prompt != "" {
		return prompt
	}
	return ""
}

// adapterTrimText centralizes whitespace trimming for final-text decisions.
func adapterTrimText(text string) string {
	return strings.TrimSpace(text)
}

// adapterCWD resolves the runtime working directory with a safe local default.
func adapterCWD(runtimeOptions map[string]any) string {
	cwd, _ := runtimeOptions["cwd"].(string)
	if cwd == "" {
		cwd = "."
	}
	return cwd
}

// normalizeACPWorkspaceBinding keeps empty dialect bindings aligned with the
// local-first default working directory.
func normalizeACPWorkspaceBinding(binding atypes.WorkspaceBinding) atypes.WorkspaceBinding {
	if binding.CWD == "" {
		binding.CWD = "."
	}
	return binding
}

// acpPromptTimeout keeps prompt RPCs alive long enough for slow runtimes while
// still respecting longer idle timeout overrides.
func acpPromptTimeout(idleTimeout time.Duration) time.Duration {
	if idleTimeout < time.Minute {
		return time.Minute
	}
	return idleTimeout
}

// mergeAdapterOptions combines runtime defaults with request overrides without
// mutating the stored registry copy.
func mergeAdapterOptions(base, override map[string]any) map[string]any {
	out := cloneAdapterOptions(base)
	for key, value := range override {
		out[key] = value
	}
	return out
}

// cloneAdapterOptions gives ACP helpers a local mutable copy when they need to
// merge runtime defaults with per-request overrides.
func cloneAdapterOptions(in map[string]any) map[string]any {
	if in == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

// sessionMatches ignores notification noise from other ACP sessions sharing the
// same worker process.
func sessionMatches(params map[string]any, sessionID string) bool {
	sid, _ := params["sessionId"].(string)
	if sid == "" {
		sid, _ = params["session_id"].(string)
	}
	return sid == sessionID
}

// chunkText extracts streamed assistant text from generic ACP chunk updates.
func chunkText(params map[string]any) (string, bool) {
	update, _ := params["update"].(map[string]any)
	if update == nil {
		return "", false
	}
	if updateType, _ := update["sessionUpdate"].(string); updateType != "agent_message_chunk" {
		return "", false
	}
	content, _ := update["content"].(map[string]any)
	if content == nil {
		return "", false
	}
	text, _ := content["text"].(string)
	if text == "" {
		return "", false
	}
	return text, true
}

// taskRemoteSessionID safely pulls the persisted remote session id from a task
// record when the adapter needs to rebuild handle state.
func taskRemoteSessionID(task atypes.TaskRecord) string {
	if task.Remote == nil {
		return ""
	}
	return task.Remote.RemoteSessionID
}
