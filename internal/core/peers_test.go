package core

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/aethrolink/aethrolink-core/internal/nodeproto"
	"github.com/aethrolink/aethrolink-core/internal/storage"
	atypes "github.com/aethrolink/aethrolink-core/pkg/types"
)

func setupPeerTestOrchestrator(t *testing.T) (*Orchestrator, *storage.SQLiteStore) {
	t.Helper()
	tmp := t.TempDir()
	store, err := storage.Open(filepath.Join(tmp, "aethrolink.db"), filepath.Join(tmp, "artifacts"), "http://127.0.0.1")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	orchestrator, err := NewOrchestratorWithNodeID(staticDiscovery{}, store, nil, nil, "node-a")
	if err != nil {
		t.Fatalf("create orchestrator: %v", err)
	}
	return orchestrator, store
}

func TestSyncPeerTargetsRemovesTargetsMissingFromLatestExport(t *testing.T) {
	ctx := context.Background()
	orchestrator, store := setupPeerTestOrchestrator(t)
	exported := []atypes.RuntimeSpec{{TargetID: "kept", Owner: atypes.TargetOwnerLocal}, {TargetID: "removed", Owner: atypes.TargetOwnerLocal}}
	peerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/node/health":
			_ = json.NewEncoder(w).Encode(nodeproto.NodeHealthResponse{NodeID: "node-b", OK: true})
		case "/v1/targets":
			_ = json.NewEncoder(w).Encode(map[string]any{"targets": exported})
		default:
			http.NotFound(w, r)
		}
	}))
	defer peerServer.Close()

	if _, err := orchestrator.AddPeer(ctx, atypes.PeerUpsertRequest{PeerID: "peer-b", BaseURL: peerServer.URL}); err != nil {
		t.Fatalf("add peer: %v", err)
	}
	if _, err := orchestrator.SyncPeerTargets(ctx, "peer-b"); err != nil {
		t.Fatalf("initial sync peer targets: %v", err)
	}
	exported = []atypes.RuntimeSpec{{TargetID: "kept", Owner: atypes.TargetOwnerLocal}}
	if _, err := orchestrator.SyncPeerTargets(ctx, "peer-b"); err != nil {
		t.Fatalf("refresh sync peer targets: %v", err)
	}

	targets, err := store.ListPeerTargets(ctx)
	if err != nil {
		t.Fatalf("list peer targets: %v", err)
	}
	if len(targets) != 1 || targets[0].TargetID != "kept" {
		t.Fatalf("expected stale target to be removed after sync refresh, got %+v", targets)
	}
}

func TestAddPeerRejectsNonHTTPBaseURL(t *testing.T) {
	orchestrator, _ := setupPeerTestOrchestrator(t)

	_, err := orchestrator.AddPeer(context.Background(), atypes.PeerUpsertRequest{PeerID: "peer-b", BaseURL: "ftp://127.0.0.1:9000"})
	if err == nil {
		t.Fatal("expected non-http peer base url to be rejected")
	}
	if err != ErrPeerBaseURLInvalid {
		t.Fatalf("expected ErrPeerBaseURLInvalid, got %v", err)
	}
}
