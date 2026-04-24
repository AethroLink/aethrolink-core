package agents

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/aethrolink/aethrolink-core/internal/storage"
	atypes "github.com/aethrolink/aethrolink-core/pkg/types"
)

func TestServiceListRuntimesIncludesRemotePeerTargets(t *testing.T) {
	tmp := t.TempDir()
	store, err := storage.Open(filepath.Join(tmp, "aethrolink.db"), filepath.Join(tmp, "artifacts"), "http://127.0.0.1:7777")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	now := time.Date(2026, 4, 24, 8, 0, 0, 0, time.UTC)
	if err := store.UpsertPeer(ctx, atypes.PeerRecord{
		PeerID:       "node-b",
		DisplayName:  "Research node",
		BaseURL:      "http://127.0.0.1:9092",
		Status:       atypes.PeerStatusOffline,
		Capabilities: []string{"node.targets"},
		Metadata:     map[string]any{},
		RegisteredAt: now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	}); err != nil {
		t.Fatalf("upsert peer: %v", err)
	}
	if err := store.UpsertPeerTarget(ctx, atypes.PeerTargetRecord{
		PeerID:       "node-b",
		TargetID:     "researcher",
		DisplayName:  "Remote researcher",
		Capabilities: []string{"research.summary"},
		Defaults:     map[string]any{"executor": "research"},
		Metadata:     map[string]any{},
		Status:       atypes.PeerTargetStatusAvailable,
		SyncedAt:     now,
	}); err != nil {
		t.Fatalf("upsert peer target: %v", err)
	}

	runtimes, err := NewService(store).ListRuntimes(ctx)
	if err != nil {
		t.Fatalf("list runtimes: %v", err)
	}
	if len(runtimes) != 1 {
		t.Fatalf("expected one remote runtime, got %d", len(runtimes))
	}
	remote := runtimes[0]
	if remote.TargetID != "researcher" || remote.Owner != atypes.TargetOwnerRemote || remote.PeerID != "node-b" {
		t.Fatalf("remote ownership was not exposed: %+v", remote)
	}
	if remote.PeerStatus != atypes.PeerStatusOffline {
		t.Fatalf("expected offline peer status to remain visible, got %q", remote.PeerStatus)
	}
}

func TestServiceResolveRuntimeReturnsRemotePeerTarget(t *testing.T) {
	tmp := t.TempDir()
	store, err := storage.Open(filepath.Join(tmp, "aethrolink.db"), filepath.Join(tmp, "artifacts"), "http://127.0.0.1:7777")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	now := time.Date(2026, 4, 24, 8, 0, 0, 0, time.UTC)
	if err := store.UpsertPeer(ctx, atypes.PeerRecord{
		PeerID:       "node-b",
		DisplayName:  "Research node",
		BaseURL:      "http://127.0.0.1:9092",
		Status:       atypes.PeerStatusOnline,
		Capabilities: []string{"node.targets"},
		Metadata:     map[string]any{},
		RegisteredAt: now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	}); err != nil {
		t.Fatalf("upsert peer: %v", err)
	}
	if err := store.UpsertPeerTarget(ctx, atypes.PeerTargetRecord{
		PeerID:       "node-b",
		TargetID:     "researcher",
		DisplayName:  "Remote researcher",
		Capabilities: []string{"research.summary"},
		Defaults:     map[string]any{},
		Metadata:     map[string]any{},
		Status:       atypes.PeerTargetStatusAvailable,
		SyncedAt:     now,
	}); err != nil {
		t.Fatalf("upsert peer target: %v", err)
	}

	runtime, err := NewService(store).ResolveRuntime(ctx, "researcher")
	if err != nil {
		t.Fatalf("resolve remote runtime: %v", err)
	}
	if runtime.Owner != atypes.TargetOwnerRemote || runtime.PeerID != "node-b" || runtime.PeerBaseURL == "" {
		t.Fatalf("remote runtime ownership was not resolved: %+v", runtime)
	}
}
