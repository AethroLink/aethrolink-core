package adapters

import (
	"testing"

	atypes "github.com/aethrolink/aethrolink-core/pkg/types"
)

// TestACPDefaultDialectsIncludeGoose proves the generic ACP adapter can resolve
// a Goose CLI target without borrowing Hermes or OpenClaw semantics.
func TestACPDefaultDialectsIncludeGoose(t *testing.T) {
	dialect, ok := defaultACPDialects()["goose"]
	if !ok {
		t.Fatalf("expected goose dialect to be registered")
	}
	if dialect.Name() != "goose" {
		t.Fatalf("expected goose dialect name, got %q", dialect.Name())
	}
}

// TestGooseDialectUsesProfileScopedSubcontextAndConversationStickyKey keeps
// long-lived Goose process reuse separate by profile while preserving
// conversation-scoped sticky sessions.
func TestGooseDialectUsesProfileScopedSubcontextAndConversationStickyKey(t *testing.T) {
	dialect, ok := defaultACPDialects()["goose"]
	if !ok {
		t.Fatalf("expected goose dialect to be registered")
	}
	if got := dialect.SubcontextKey(map[string]any{}); got != "profile:default" {
		t.Fatalf("expected default Goose profile scope, got %q", got)
	}
	if got := dialect.SubcontextKey(map[string]any{"profile": "qa"}); got != "profile:qa" {
		t.Fatalf("expected explicit Goose profile scope, got %q", got)
	}
	task := atypes.TaskEnvelope{ConversationID: "conv-123", RuntimeOptions: map[string]any{"profile": "qa"}, TaskID: "task-123"}
	if got := dialect.StickyKey(task); got != "conv-123" {
		t.Fatalf("expected conversation sticky key, got %q", got)
	}
}

// TestGooseDialectBuildsStandardACPPayloads verifies Goose CLI uses the same
// ACP session payload shape documented by Goose itself while keeping
// Goose-specific binding metadata local to the dialect.
func TestGooseDialectBuildsStandardACPPayloads(t *testing.T) {
	dialect, ok := defaultACPDialects()["goose"]
	if !ok {
		t.Fatalf("expected goose dialect to be registered")
	}
	binding := atypes.WorkspaceBinding{CWD: "/repo", MCPServers: []map[string]any{{"name": "memory"}}}
	openPayload := dialect.OpenSessionPayload(binding, map[string]any{"profile": "qa"})
	if got, _ := openPayload["cwd"].(string); got != "/repo" {
		t.Fatalf("expected Goose open-session cwd, got %q", got)
	}
	if got, ok := openPayload["mcpServers"].([]map[string]any); !ok || len(got) != 1 {
		t.Fatalf("expected Goose open-session MCP servers, got %#v", openPayload["mcpServers"])
	}
	loadPayload := dialect.LoadSessionPayload("sess-123", binding, map[string]any{"profile": "qa"})
	if got, _ := loadPayload["sessionId"].(string); got != "sess-123" {
		t.Fatalf("expected Goose load-session id, got %q", got)
	}
	promptPayload := dialect.PromptPayload("sess-123", "Reply exactly OK", binding, map[string]any{"profile": "qa"})
	promptItems, ok := promptPayload["prompt"].([]map[string]any)
	if !ok || len(promptItems) != 1 {
		t.Fatalf("expected Goose prompt items, got %#v", promptPayload["prompt"])
	}
	if got, _ := promptItems[0]["text"].(string); got != "Reply exactly OK" {
		t.Fatalf("expected Goose prompt text, got %q", got)
	}
	if dialect.BindingName() != "goose_acp" {
		t.Fatalf("expected Goose binding name, got %q", dialect.BindingName())
	}
}
