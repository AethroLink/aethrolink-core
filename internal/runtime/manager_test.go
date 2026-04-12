package runtime

import (
	"context"
	"path/filepath"
	"testing"

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
	lease, err := manager.EnsureProcess(ctx, "hermes", "profile:coder", nil, "")
	if err != nil {
		t.Fatalf("ensure process: %v", err)
	}
	if lease.LeaseID == "" {
		t.Fatalf("expected lease id")
	}
	health := manager.Health("hermes", "profile:coder")
	if healthy, _ := health["healthy"].(bool); !healthy {
		t.Fatalf("expected healthy runtime")
	}
	if err := manager.Stop(ctx, "hermes", "profile:coder"); err != nil {
		t.Fatalf("stop runtime: %v", err)
	}
	health = manager.Health("hermes", "profile:coder")
	if healthy, _ := health["healthy"].(bool); healthy {
		t.Fatalf("expected unhealthy runtime after stop")
	}
}
