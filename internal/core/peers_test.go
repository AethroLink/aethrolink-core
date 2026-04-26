package core

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

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

func TestSyncPeerTargetsPreservesPeerLoadErrors(t *testing.T) {
	ctx := context.Background()
	orchestrator, store := setupPeerTestOrchestrator(t)
	if _, err := orchestrator.AddPeer(ctx, atypes.PeerUpsertRequest{PeerID: "peer-b", BaseURL: "http://127.0.0.1:1"}); err != nil {
		t.Fatalf("add peer: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	_, err := orchestrator.SyncPeerTargets(ctx, "peer-b")
	if err == nil {
		t.Fatal("expected closed store error")
	}
	if errors.Is(err, ErrPeerNotFound) {
		t.Fatalf("expected storage error to be preserved, got %v", err)
	}
}

func TestSyncPeerTargetsMarksPreviouslyOnlinePeerOfflineOnProbeFailure(t *testing.T) {
	ctx := context.Background()
	orchestrator, store := setupPeerTestOrchestrator(t)
	peerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/node/health":
			_ = json.NewEncoder(w).Encode(nodeproto.NodeHealthResponse{NodeID: "node-b", OK: true})
		case "/v1/targets":
			_ = json.NewEncoder(w).Encode(map[string]any{"targets": []atypes.RuntimeSpec{{TargetID: "kept", Owner: atypes.TargetOwnerLocal}}})
		default:
			http.NotFound(w, r)
		}
	}))

	if _, err := orchestrator.AddPeer(ctx, atypes.PeerUpsertRequest{PeerID: "peer-b", BaseURL: peerServer.URL}); err != nil {
		t.Fatalf("add peer: %v", err)
	}
	if _, err := orchestrator.SyncPeerTargets(ctx, "peer-b"); err != nil {
		t.Fatalf("sync peer online: %v", err)
	}
	peerServer.Close()

	_, err := orchestrator.SyncPeerTargets(ctx, "peer-b")
	if err == nil {
		t.Fatal("expected sync failure after peer server closes")
	}
	peer, err := store.GetPeer(ctx, "peer-b")
	if err != nil {
		t.Fatalf("get peer after failed sync: %v", err)
	}
	if peer.Status != atypes.PeerStatusOffline {
		t.Fatalf("expected failed sync to mark peer offline, got %q", peer.Status)
	}
}

func TestSyncAllPeerTargetsContinuesAfterPeerFailure(t *testing.T) {
	ctx := context.Background()
	orchestrator, store := setupPeerTestOrchestrator(t)
	peerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/node/health":
			_ = json.NewEncoder(w).Encode(nodeproto.NodeHealthResponse{NodeID: "node-c", OK: true})
		case "/v1/targets":
			_ = json.NewEncoder(w).Encode(map[string]any{"targets": []atypes.RuntimeSpec{{TargetID: "healthy", Owner: atypes.TargetOwnerLocal}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer peerServer.Close()

	if _, err := orchestrator.AddPeer(ctx, atypes.PeerUpsertRequest{PeerID: "peer-b", BaseURL: "http://127.0.0.1:1"}); err != nil {
		t.Fatalf("add failed peer: %v", err)
	}
	if _, err := orchestrator.AddPeer(ctx, atypes.PeerUpsertRequest{PeerID: "peer-c", BaseURL: peerServer.URL}); err != nil {
		t.Fatalf("add healthy peer: %v", err)
	}
	responses, err := orchestrator.SyncAllPeerTargets(ctx)
	if err == nil {
		t.Fatal("expected partial sync error")
	}
	if len(responses) != 1 || responses[0].Peer.PeerID != "peer-c" {
		t.Fatalf("expected healthy peer to sync after failed peer, got %+v", responses)
	}
	targets, err := store.ListPeerTargets(ctx)
	if err != nil {
		t.Fatalf("list peer targets: %v", err)
	}
	if len(targets) != 1 || targets[0].TargetID != "healthy" {
		t.Fatalf("expected healthy peer target to be cached, got %+v", targets)
	}
}

func TestBackgroundPeerSyncRefreshesCachedTargets(t *testing.T) {
	ctx := context.Background()
	orchestrator, store := setupPeerTestOrchestrator(t)
	exported := []atypes.RuntimeSpec{{TargetID: "background", Owner: atypes.TargetOwnerLocal}}
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
	stop := orchestrator.StartPeerSyncLoop(ctx, 10*time.Millisecond, func(error) {})
	defer stop()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		targets, err := store.ListPeerTargets(ctx)
		if err != nil {
			t.Fatalf("list peer targets: %v", err)
		}
		if len(targets) == 1 && targets[0].TargetID == "background" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("background sync did not cache peer target before timeout")
}

func TestSyncAllPeerTargetsMarksTimedOutPeerOffline(t *testing.T) {
	ctx := context.Background()
	orchestrator, store := setupPeerTestOrchestrator(t)
	oldTimeout := peerSyncRequestTimeout
	peerSyncRequestTimeout = 20 * time.Millisecond
	t.Cleanup(func() { peerSyncRequestTimeout = oldTimeout })
	var hang atomic.Bool
	peerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hang.Load() {
			<-r.Context().Done()
			return
		}
		switch r.URL.Path {
		case "/v1/node/health":
			_ = json.NewEncoder(w).Encode(nodeproto.NodeHealthResponse{NodeID: "node-b", OK: true})
		case "/v1/targets":
			_ = json.NewEncoder(w).Encode(map[string]any{"targets": []atypes.RuntimeSpec{{TargetID: "initial", Owner: atypes.TargetOwnerLocal}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer peerServer.Close()

	if _, err := orchestrator.AddPeer(ctx, atypes.PeerUpsertRequest{PeerID: "peer-b", BaseURL: peerServer.URL}); err != nil {
		t.Fatalf("add peer: %v", err)
	}
	if _, err := orchestrator.SyncAllPeerTargets(ctx); err != nil {
		t.Fatalf("sync peer online: %v", err)
	}
	hang.Store(true)
	if _, err := orchestrator.SyncAllPeerTargets(ctx); err == nil {
		t.Fatal("expected hung peer sync to fail")
	}
	peer, err := store.GetPeer(ctx, "peer-b")
	if err != nil {
		t.Fatalf("get peer after timed-out sync: %v", err)
	}
	if peer.Status != atypes.PeerStatusOffline {
		t.Fatalf("expected timed-out peer to be marked offline, got %q", peer.Status)
	}
}

func TestBackgroundPeerSyncBoundsHungPeerAndContinues(t *testing.T) {
	ctx := context.Background()
	orchestrator, store := setupPeerTestOrchestrator(t)
	oldTimeout := peerSyncRequestTimeout
	peerSyncRequestTimeout = 20 * time.Millisecond
	t.Cleanup(func() { peerSyncRequestTimeout = oldTimeout })

	hungPeer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer hungPeer.Close()
	healthyPeer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/node/health":
			_ = json.NewEncoder(w).Encode(nodeproto.NodeHealthResponse{NodeID: "node-c", OK: true})
		case "/v1/targets":
			_ = json.NewEncoder(w).Encode(map[string]any{"targets": []atypes.RuntimeSpec{{TargetID: "after-hung", Owner: atypes.TargetOwnerLocal}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer healthyPeer.Close()

	if _, err := orchestrator.AddPeer(ctx, atypes.PeerUpsertRequest{PeerID: "peer-b", BaseURL: hungPeer.URL}); err != nil {
		t.Fatalf("add hung peer: %v", err)
	}
	if _, err := orchestrator.AddPeer(ctx, atypes.PeerUpsertRequest{PeerID: "peer-c", BaseURL: healthyPeer.URL}); err != nil {
		t.Fatalf("add healthy peer: %v", err)
	}
	stop := orchestrator.StartPeerSyncLoop(ctx, 10*time.Millisecond, func(error) {})
	defer stop()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		targets, err := store.ListPeerTargets(ctx)
		if err != nil {
			t.Fatalf("list peer targets: %v", err)
		}
		if len(targets) == 1 && targets[0].TargetID == "after-hung" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("background sync did not continue after hung peer")
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
