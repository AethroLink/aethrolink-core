package adapters

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aethrolink/aethrolink-core/internal/config"
	"github.com/aethrolink/aethrolink-core/internal/runtime"
	"github.com/aethrolink/aethrolink-core/internal/storage"
	atypes "github.com/aethrolink/aethrolink-core/pkg/types"
)

func TestOpenClawRecoverFinalTextReturnsEmptyWhenWorkerMissing(t *testing.T) {
	adapter := newOpenClawAdapterForRecoveryTest(t)
	dialect := adapter.dialects["openclaw"]
	if got := adapter.recoverFinalText(context.Background(), "openclaw_test", "session:design", "sess-missing", atypes.WorkspaceBinding{CWD: "."}, map[string]any{"session_key": "design"}, dialect); got != "" {
		t.Fatalf("expected empty text without worker, got %q", got)
	}
}

func TestOpenClawRecoverFinalTextUsesSessionLoadReplay(t *testing.T) {
	adapter := newOpenClawAdapterForRecoveryTest(t)
	lease, err := adapter.EnsureReady(context.Background(), "openclaw_test", map[string]any{"session_key": "design"})
	if err != nil {
		t.Fatalf("ensure ready: %v", err)
	}
	worker := adapter.runtime.GetStdioWorker("openclaw_test", lease.SubcontextKey)
	if worker == nil {
		t.Fatalf("expected openclaw worker")
	}
	if _, err := worker.RequestWithTimeout("initialize", map[string]any{"protocolVersion": 1}, 5*time.Second); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	result, err := worker.RequestWithTimeout("session/new", map[string]any{"cwd": ".", "mcpServers": []any{}}, 5*time.Second)
	if err != nil {
		t.Fatalf("session/new: %v", err)
	}
	sessionID, _ := result["sessionId"].(string)
	if sessionID == "" {
		t.Fatalf("missing session id")
	}
	_, err = worker.RequestWithTimeout("session/prompt", map[string]any{"sessionId": sessionID, "prompt": []map[string]any{{"type": "text", "text": "Say exactly OPENCLAW-RECOVERED"}}}, 5*time.Second)
	if err != nil {
		t.Fatalf("session/prompt: %v", err)
	}
	dialect := adapter.dialects["openclaw"]
	if got := adapter.recoverFinalText(context.Background(), "openclaw_test", lease.SubcontextKey, sessionID, atypes.WorkspaceBinding{CWD: "."}, map[string]any{"session_key": "design"}, dialect); got != "OPENCLAW-RECOVERED" {
		t.Fatalf("expected recovered text, got %q", got)
	}
}

func newOpenClawAdapterForRecoveryTest(t *testing.T) *ACPAdapter {
	t.Helper()
	root := filepath.Clean(filepath.Join("..", ".."))
	registryPath := filepath.Join(t.TempDir(), "registry.yaml")
	registryYAML := []byte("runtimes:\n  openclaw_test:\n    adapter: acp\n    dialect: openclaw\n    launch:\n      mode: managed\n      command: [\"go\", \"run\", \"" + root + "/cmd/fake-acp-client-agent\"]\n    defaults:\n      session_key: design\n    capabilities:\n      - ui.review\n")
	if err := os.WriteFile(registryPath, registryYAML, 0o644); err != nil {
		t.Fatalf("write registry: %v", err)
	}
	registry, err := config.LoadRegistry(registryPath)
	if err != nil {
		t.Fatalf("load registry: %v", err)
	}
	tmp := t.TempDir()
	store, err := storage.Open(filepath.Join(tmp, "aethrolink.db"), filepath.Join(tmp, "artifacts"), "http://127.0.0.1")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	manager := runtime.NewManager(store)
	t.Cleanup(func() {
		_ = manager.StopAll(context.Background())
		_ = store.Close()
	})
	return NewACPAdapter(registry, manager)
}
