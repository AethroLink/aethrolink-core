package agents

import (
	"context"
	"path/filepath"
	"strings"
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

func TestServiceResolveRuntimeRejectsUndispatchableRemotePeerTarget(t *testing.T) {
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

	_, err = NewService(store).ResolveRuntime(ctx, "researcher")
	if err == nil {
		t.Fatal("expected remote runtime resolution to be rejected before relay transport exists")
	}
	if !strings.Contains(err.Error(), "remote target is not dispatchable") {
		t.Fatalf("expected non-dispatchable remote target error, got %v", err)
	}
}

func TestServiceResolveRuntimeRejectsAmbiguousRemoteTargetID(t *testing.T) {
	tmp := t.TempDir()
	store, err := storage.Open(filepath.Join(tmp, "aethrolink.db"), filepath.Join(tmp, "artifacts"), "http://127.0.0.1:7777")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	now := time.Date(2026, 4, 24, 8, 0, 0, 0, time.UTC)
	for _, peerID := range []string{"node-b", "node-c"} {
		if err := store.UpsertPeer(ctx, atypes.PeerRecord{
			PeerID:       peerID,
			DisplayName:  peerID,
			BaseURL:      "http://127.0.0.1:9092",
			Status:       atypes.PeerStatusOnline,
			Capabilities: []string{"node.targets"},
			Metadata:     map[string]any{},
			RegisteredAt: now,
			UpdatedAt:    now,
			LastSeenAt:   now,
		}); err != nil {
			t.Fatalf("upsert peer %s: %v", peerID, err)
		}
		if err := store.UpsertPeerTarget(ctx, atypes.PeerTargetRecord{
			PeerID:       peerID,
			TargetID:     "researcher",
			DisplayName:  "Remote researcher",
			Capabilities: []string{"research.summary"},
			Defaults:     map[string]any{},
			Metadata:     map[string]any{},
			Status:       atypes.PeerTargetStatusAvailable,
			SyncedAt:     now,
		}); err != nil {
			t.Fatalf("upsert peer target %s: %v", peerID, err)
		}
	}

	_, err = NewService(store).ResolveRuntime(ctx, "researcher")
	if err == nil {
		t.Fatal("expected duplicate peer target IDs to be rejected")
	}
	if !strings.Contains(err.Error(), "ambiguous remote target") {
		t.Fatalf("expected ambiguous remote target error, got %v", err)
	}
}
