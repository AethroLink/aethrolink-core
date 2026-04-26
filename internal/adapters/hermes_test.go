package adapters

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/aethrolink/aethrolink-core/internal/agents"
	"github.com/aethrolink/aethrolink-core/internal/runtime"
	"github.com/aethrolink/aethrolink-core/internal/storage"
	atypes "github.com/aethrolink/aethrolink-core/pkg/types"
)

func TestHermesRecoverFinalTextReturnsEmptyWhenWorkerMissing(t *testing.T) {
	adapter := newHermesAdapterForRecoveryTest(t, nil)
	dialect := adapter.dialects["hermes"]
	if got := adapter.recoverFinalText(context.Background(), "hermes_test", "executor:mimoportal", "sess-missing", atypes.WorkspaceBinding{CWD: "."}, map[string]any{"executor": "mimoportal"}, dialect); got != "" {
		t.Fatalf("expected empty text without worker, got %q", got)
	}
}

func TestHermesEnsureReadyRelaunchesUnresponsiveWorker(t *testing.T) {
	command, attemptsPath := hungThenHealthyACPCommand(t)
	adapter := newHermesAdapterForRecoveryTest(t, command)
	_, err := adapter.EnsureReady(context.Background(), "hermes_test", map[string]any{"executor": "mimoportal", "initialize_timeout_ms": 50})
	if err != nil {
		t.Fatalf("ensure ready: %v", err)
	}
	if got := readAttemptCount(t, attemptsPath); got != 2 {
		t.Fatalf("expected hung worker to be relaunched once, got %d launches", got)
	}
}

func TestHermesRecoverFinalTextUsesSessionLoadReplay(t *testing.T) {
	adapter := newHermesAdapterForRecoveryTest(t, nil)
	lease, err := adapter.EnsureReady(context.Background(), "hermes_test", map[string]any{"executor": "mimoportal"})
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
	dialect := adapter.dialects["hermes"]
	if got := adapter.recoverFinalText(context.Background(), "hermes_test", lease.SubcontextKey, sessionID, atypes.WorkspaceBinding{CWD: "."}, map[string]any{"executor": "mimoportal"}, dialect); got != "RECOVERED" {
		t.Fatalf("expected recovered text, got %q", got)
	}
}

func newHermesAdapterForRecoveryTest(t *testing.T, commandOverride ...[]string) *ACPAdapter {
	t.Helper()
	root := filepath.Clean(filepath.Join("..", ".."))
	command := []string{"go", "run", root + "/cmd/fake-acp-client-agent"}
	if len(commandOverride) > 0 && commandOverride[0] != nil {
		command = commandOverride[0]
	}
	tmp := t.TempDir()
	store, err := storage.Open(filepath.Join(tmp, "aethrolink.db"), filepath.Join(tmp, "artifacts"), "http://127.0.0.1")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	manager := runtime.NewManager(store)
	agentService := agents.NewService(store)
	if _, err := agentService.Register(context.Background(), atypes.AgentRegistrationRequest{
		AgentID:       "hermes_test",
		DisplayName:   "hermes-test",
		TransportKind: "local_managed",
		Adapter:       "acp",
		Dialect:       "hermes",
		Launch:        atypes.LaunchSpec{Mode: atypes.LaunchModeManaged, Command: command},
		Defaults:      map[string]any{"executor": "mimoportal"},
		Capabilities:  []string{"research.topic"},
	}); err != nil {
		t.Fatalf("register agent: %v", err)
	}
	t.Cleanup(func() {
		_ = manager.StopAll(context.Background())
		_ = store.Close()
	})
	return NewACPAdapter(agentService, manager)
}

// hungThenHealthyACPCommand creates a worker that hangs once, then responds on relaunch.
func hungThenHealthyACPCommand(t *testing.T) ([]string, string) {
	t.Helper()
	tmp := t.TempDir()
	attemptsPath := filepath.Join(tmp, "attempts")
	scriptPath := filepath.Join(tmp, "hung_then_healthy_acp.py")
	script := `import json, os, sys, time
attempts_path = sys.argv[1]
try:
    attempts = int(open(attempts_path).read().strip())
except Exception:
    attempts = 0
attempts += 1
open(attempts_path, "w").write(str(attempts))
if attempts == 1:
    while True:
        time.sleep(60)
for line in sys.stdin:
    msg = json.loads(line)
    method = msg.get("method")
    result = {}
    if method == "session/new":
        result = {"sessionId": "healthy-session"}
    sys.stdout.write(json.dumps({"jsonrpc": "2.0", "id": msg.get("id"), "result": result}) + "\n")
    sys.stdout.flush()
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o600); err != nil {
		t.Fatalf("write fake acp script: %v", err)
	}
	return []string{"python3", scriptPath, attemptsPath}, attemptsPath
}

// readAttemptCount returns how many ACP process launches the fake command observed.
func readAttemptCount(t *testing.T, path string) int {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read attempt count: %v", err)
	}
	count, err := strconv.Atoi(string(raw))
	if err != nil {
		t.Fatalf("parse attempt count: %v", err)
	}
	return count
}
