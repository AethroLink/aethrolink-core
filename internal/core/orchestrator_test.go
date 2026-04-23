package core

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/aethrolink/aethrolink-core/internal/storage"
)

func TestNewOrchestratorFailsWhenRestartReconciliationFails(t *testing.T) {
	t.Helper()
	tmp := t.TempDir()
	store, err := storage.Open(filepath.Join(tmp, "aethrolink.db"), filepath.Join(tmp, "artifacts"), "http://127.0.0.1")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	// A closed store forces boot reconciliation to fail instead of being skipped.
	_, err = NewOrchestrator(nil, store, nil, nil)
	if err == nil {
		t.Fatal("expected constructor to fail when restart reconciliation fails")
	}
	if !strings.Contains(err.Error(), "mark interrupted threads on restart") {
		t.Fatalf("expected reconciliation error, got %v", err)
	}
}
