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
)

func TestHermesRecoverFinalTextReturnsEmptyWhenWorkerMissing(t *testing.T) {
	adapter := newHermesAdapterForRecoveryTest(t)
	if got := adapter.recoverFinalText(context.Background(), "hermes_test", "mimoportal", "sess-missing"); got != "" {
		t.Fatalf("expected empty text without worker, got %q", got)
	}
}

func TestHermesRecoverFinalTextUsesSessionLoadReplay(t *testing.T) {
	adapter := newHermesAdapterForRecoveryTest(t)
	lease, err := adapter.EnsureReady(context.Background(), "hermes_test", map[string]any{"profile": "mimoportal"})
	if err != nil {
		t.Fatalf("ensure ready: %v", err)
	}
	worker := adapter.runtime.GetStdioWorker("hermes_test", lease.SubcontextKey)
	if worker == nil {
		t.Fatalf("expected hermes worker")
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
	_, err = worker.RequestWithTimeout("session/prompt", map[string]any{"sessionId": sessionID, "prompt": []map[string]any{{"type": "text", "text": "Say exactly RECOVERED"}}}, 5*time.Second)
	if err != nil {
		t.Fatalf("session/prompt: %v", err)
	}
	if got := adapter.recoverFinalText(context.Background(), "hermes_test", "mimoportal", sessionID); got != "RECOVERED" {
		t.Fatalf("expected recovered text, got %q", got)
	}
}

func newHermesAdapterForRecoveryTest(t *testing.T) *HermesAdapter {
	t.Helper()
	root := filepath.Clean(filepath.Join("..", ".."))
	registryPath := filepath.Join(t.TempDir(), "registry.yaml")
	registryYAML := []byte("runtimes:\n  hermes_test:\n    adapter: hermes\n    launch:\n      mode: managed\n      commands:\n        mimoportal: [\"go\", \"run\", \"" + root + "/cmd/fake-acp-client-agent\"]\n    defaults:\n      profile: mimoportal\n    capabilities:\n      - research.topic\n")
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
	return NewHermesAdapter(registry, manager)
}
