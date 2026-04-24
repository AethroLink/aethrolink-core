package storage

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	atypes "github.com/aethrolink/aethrolink-core/pkg/types"
)

func TestSQLiteStorePersistsPeers(t *testing.T) {
	tmp := t.TempDir()
	store, err := Open(filepath.Join(tmp, "aethrolink.db"), filepath.Join(tmp, "artifacts"), "http://127.0.0.1:7777")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	now := time.Date(2026, 4, 24, 8, 0, 0, 0, time.UTC)
	peer := atypes.PeerRecord{
		PeerID:       "node-b",
		DisplayName:  "Research node",
		BaseURL:      "http://127.0.0.1:9092",
		Status:       atypes.PeerStatusOnline,
		Capabilities: []string{"node.tasks", "node.targets"},
		Metadata:     map[string]any{"zone": "local"},
		RegisteredAt: now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	}

	if err := store.UpsertPeer(ctx, peer); err != nil {
		t.Fatalf("upsert peer: %v", err)
	}
	loaded, err := store.GetPeer(ctx, "node-b")
	if err != nil {
		t.Fatalf("get peer: %v", err)
	}
	if loaded.BaseURL != peer.BaseURL || loaded.Status != atypes.PeerStatusOnline {
		t.Fatalf("peer was not preserved: %+v", loaded)
	}
	if len(loaded.Capabilities) != 2 || loaded.Metadata["zone"] != "local" {
		t.Fatalf("peer metadata was not preserved: %+v", loaded)
	}
}

func TestSQLiteStorePersistsRemoteTargetsForOfflinePeer(t *testing.T) {
	tmp := t.TempDir()
	store, err := Open(filepath.Join(tmp, "aethrolink.db"), filepath.Join(tmp, "artifacts"), "http://127.0.0.1:7777")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	now := time.Date(2026, 4, 24, 8, 0, 0, 0, time.UTC)
	peer := atypes.PeerRecord{
		PeerID:       "node-b",
		DisplayName:  "Research node",
		BaseURL:      "http://127.0.0.1:9092",
		Status:       atypes.PeerStatusOffline,
		Capabilities: []string{"node.targets"},
		Metadata:     map[string]any{},
		RegisteredAt: now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	}
	target := atypes.PeerTargetRecord{
		PeerID:       "node-b",
		TargetID:     "researcher",
		DisplayName:  "Remote researcher",
		Capabilities: []string{"research.summary"},
		Defaults:     map[string]any{"executor": "research"},
		Metadata:     map[string]any{"exported_by": "node-b"},
		Status:       atypes.PeerTargetStatusAvailable,
		SyncedAt:     now,
	}

	if err := store.UpsertPeer(ctx, peer); err != nil {
		t.Fatalf("upsert peer: %v", err)
	}
	if err := store.UpsertPeerTarget(ctx, target); err != nil {
		t.Fatalf("upsert peer target: %v", err)
	}
	targets, err := store.ListPeerTargets(ctx)
	if err != nil {
		t.Fatalf("list peer targets: %v", err)
	}
	if len(targets) != 1 {
		t.Fatalf("expected one peer target, got %d", len(targets))
	}
	if targets[0].TargetID != "researcher" || targets[0].PeerID != "node-b" {
		t.Fatalf("remote target identity was not preserved: %+v", targets[0])
	}
	if len(targets[0].Capabilities) != 1 || targets[0].Defaults["executor"] != "research" {
		t.Fatalf("remote target payload was not preserved: %+v", targets[0])
	}
}
