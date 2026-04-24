package core

import (
	"context"
	"errors"
	"fmt"
	"net/url"

	"github.com/aethrolink/aethrolink-core/internal/nodetransport"
	atypes "github.com/aethrolink/aethrolink-core/pkg/types"
)

var (
	ErrPeerNotFound       = errors.New("peer not found")
	ErrPeerIDRequired     = errors.New("peer_id required")
	ErrPeerBaseURLInvalid = errors.New("peer base_url invalid")
)

// AddPeer stores an operator-provided static peer without contacting runtimes.
func (o *Orchestrator) AddPeer(ctx context.Context, req atypes.PeerUpsertRequest) (atypes.PeerRecord, error) {
	if req.PeerID == "" {
		return atypes.PeerRecord{}, ErrPeerIDRequired
	}
	baseURL, err := normalizePeerBaseURL(req.BaseURL)
	if err != nil {
		return atypes.PeerRecord{}, err
	}
	now := atypes.NowUTC()
	existing, err := o.store.GetPeer(ctx, req.PeerID)
	if err == nil && !existing.RegisteredAt.IsZero() {
		now = existing.RegisteredAt
	}
	updated := atypes.NowUTC()
	peer := atypes.PeerRecord{
		PeerID:       req.PeerID,
		DisplayName:  req.DisplayName,
		BaseURL:      baseURL,
		Status:       atypes.PeerStatusOffline,
		Capabilities: append([]string(nil), req.Capabilities...),
		Metadata:     cloneMap(req.Metadata),
		RegisteredAt: now,
		UpdatedAt:    updated,
		LastSeenAt:   updated,
	}
	if peer.DisplayName == "" {
		peer.DisplayName = req.PeerID
	}
	if err := o.store.UpsertPeer(ctx, peer); err != nil {
		return atypes.PeerRecord{}, err
	}
	return peer, nil
}

// ListPeers returns locally registered static peers for operator inspection.
func (o *Orchestrator) ListPeers(ctx context.Context) ([]atypes.PeerRecord, error) {
	return o.store.ListPeers(ctx)
}

// SyncPeerTargets refreshes one peer's exported local targets into the cache.
func (o *Orchestrator) SyncPeerTargets(ctx context.Context, peerID string) (atypes.PeerSyncResponse, error) {
	peer, err := o.store.GetPeer(ctx, peerID)
	if err != nil {
		return atypes.PeerSyncResponse{}, ErrPeerNotFound
	}
	client := nodetransport.NewHTTPClient(peer.BaseURL, o.nodeID, nil)
	health, err := client.Health(ctx)
	if err != nil {
		return atypes.PeerSyncResponse{}, fmt.Errorf("peer health: %w", err)
	}
	targets, err := client.ListTargets(ctx)
	if err != nil {
		return atypes.PeerSyncResponse{}, fmt.Errorf("peer targets: %w", err)
	}
	now := atypes.NowUTC()
	peer.Status = atypes.PeerStatusOnline
	peer.UpdatedAt = now
	peer.LastSeenAt = now
	if health.NodeID != "" {
		peer.Metadata = cloneMap(peer.Metadata)
		peer.Metadata["node_id"] = health.NodeID
	}
	if err := o.store.UpsertPeer(ctx, peer); err != nil {
		return atypes.PeerSyncResponse{}, err
	}
	cached := make([]atypes.PeerTargetRecord, 0, len(targets))
	for _, target := range targets {
		if target.Owner == atypes.TargetOwnerRemote {
			continue
		}
		record := atypes.PeerTargetRecord{
			PeerID:       peer.PeerID,
			TargetID:     target.TargetID,
			DisplayName:  target.TargetID,
			Capabilities: append([]string(nil), target.Capabilities...),
			Defaults:     cloneMap(target.Defaults),
			Metadata:     map[string]any{"source_node_id": health.NodeID},
			Status:       atypes.PeerTargetStatusAvailable,
			SyncedAt:     now,
		}
		if err := o.store.UpsertPeerTarget(ctx, record); err != nil {
			return atypes.PeerSyncResponse{}, err
		}
		cached = append(cached, record)
	}
	return atypes.PeerSyncResponse{Peer: peer, Targets: cached}, nil
}

// normalizePeerBaseURL requires an absolute HTTP(S) address for node transport.
func normalizePeerBaseURL(raw string) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", ErrPeerBaseURLInvalid
	}
	return parsed.String(), nil
}
