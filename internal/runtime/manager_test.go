package runtime

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/aethrolink/aethrolink-core/internal/storage"
)

func TestEnsureProcessPersistsLeaseAndStopReleasesIt(t *testing.T) {
	tmp := t.TempDir()
	store, err := storage.Open(filepath.Join(tmp, "aethrolink.db"), filepath.Join(tmp, "artifacts"), "http://127.0.0.1")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	manager := NewManager(store)
	ctx := context.Background()
	lease, err := manager.EnsureProcess(ctx, "hermes", "executor:coder", nil, "")
	if err != nil {
		t.Fatalf("ensure process: %v", err)
	}
	if lease.LeaseID == "" {
		t.Fatalf("expected lease id")
	}
	health := manager.Health("hermes", "executor:coder")
	if healthy, _ := health["healthy"].(bool); !healthy {
		t.Fatalf("expected healthy runtime")
	}
	if err := manager.Stop(ctx, "hermes", "executor:coder"); err != nil {
		t.Fatalf("stop runtime: %v", err)
	}
	health = manager.Health("hermes", "executor:coder")
	if healthy, _ := health["healthy"].(bool); healthy {
		t.Fatalf("expected unhealthy runtime after stop")
	}
}

func TestEnsureStdioWorkerFailsFastForEarlyExit(t *testing.T) {
	tmp := t.TempDir()
	store, err := storage.Open(filepath.Join(tmp, "aethrolink.db"), filepath.Join(tmp, "artifacts"), "http://127.0.0.1")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	manager := NewManager(store)
	ctx := context.Background()
	if _, _, err := manager.EnsureStdioWorker(ctx, "dead-target", "default", []string{"sh", "-lc", "exit 1"}); err == nil {
		t.Fatalf("expected fast-exit worker to fail readiness")
	}
}

func TestStdioWorkerRequestReturnsExitErrorWhenProcessDiesBeforeReply(t *testing.T) {
	worker, err := spawnStdioWorker([]string{"sh", "-lc", "echo boom >&2; exit 1"})
	if err != nil {
		t.Fatalf("spawn stdio worker: %v", err)
	}
	_, err = worker.RequestWithTimeout("initialize", map[string]any{}, 2*time.Second)
	if err == nil {
		t.Fatalf("expected request to fail when worker exits")
	}
	if got := err.Error(); got == "rpc timeout" {
		t.Fatalf("expected exit error, got timeout")
	}
}
