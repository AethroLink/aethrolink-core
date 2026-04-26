package core

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"time"

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
		if errors.Is(err, sql.ErrNoRows) {
			return atypes.PeerSyncResponse{}, ErrPeerNotFound
		}
		return atypes.PeerSyncResponse{}, err
	}
	client := nodetransport.NewHTTPClient(peer.BaseURL, o.nodeID, nil)
	health, err := client.Health(ctx)
	if err != nil {
		if offlineErr := o.markPeerOfflineAfterSyncFailure(ctx, peer); offlineErr != nil {
			return atypes.PeerSyncResponse{}, offlineErr
		}
		return atypes.PeerSyncResponse{}, fmt.Errorf("peer health: %w", err)
	}
	targets, err := client.ListTargets(ctx)
	if err != nil {
		if offlineErr := o.markPeerOfflineAfterSyncFailure(ctx, peer); offlineErr != nil {
			return atypes.PeerSyncResponse{}, offlineErr
		}
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
	cachedIDs := make([]string, 0, len(targets))
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
		cachedIDs = append(cachedIDs, record.TargetID)
	}
	if err := o.store.DeletePeerTargetsExcept(ctx, peer.PeerID, cachedIDs); err != nil {
		return atypes.PeerSyncResponse{}, err
	}
	return atypes.PeerSyncResponse{Peer: peer, Targets: cached}, nil
}

// SyncAllPeerTargets refreshes every registered peer into the local discovery cache.
func (o *Orchestrator) SyncAllPeerTargets(ctx context.Context) ([]atypes.PeerSyncResponse, error) {
	peers, err := o.store.ListPeers(ctx)
	if err != nil {
		return nil, err
	}
	responses := make([]atypes.PeerSyncResponse, 0, len(peers))
	for _, peer := range peers {
		response, err := o.SyncPeerTargets(ctx, peer.PeerID)
		if err != nil {
			return responses, fmt.Errorf("sync peer %s: %w", peer.PeerID, err)
		}
		responses = append(responses, response)
	}
	return responses, nil
}

// StartPeerSyncLoop keeps cached peer target discovery warm without blocking plain reads.
func (o *Orchestrator) StartPeerSyncLoop(ctx context.Context, interval time.Duration, onError func(error)) func() {
	if interval <= 0 {
		return func() {}
	}
	loopCtx, cancel := context.WithCancel(ctx)
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-loopCtx.Done():
				return
			case <-ticker.C:
				if _, err := o.SyncAllPeerTargets(loopCtx); err != nil && onError != nil {
					onError(err)
				}
			}
		}
	}()
	return cancel
}

// markPeerOfflineAfterSyncFailure records failed peer probes for operator-visible liveness.
func (o *Orchestrator) markPeerOfflineAfterSyncFailure(ctx context.Context, peer atypes.PeerRecord) error {
	now := atypes.NowUTC()
	peer.Status = atypes.PeerStatusOffline
	peer.UpdatedAt = now
	return o.store.UpsertPeer(ctx, peer)
}

// normalizePeerBaseURL requires an absolute HTTP(S) address for node transport.
func normalizePeerBaseURL(raw string) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", ErrPeerBaseURLInvalid
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", ErrPeerBaseURLInvalid
	}
	return parsed.String(), nil
}
