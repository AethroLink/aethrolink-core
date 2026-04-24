package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	_ "modernc.org/sqlite"

	atypes "github.com/aethrolink/aethrolink-core/pkg/types"
)

// UpsertPeer stores static peer identity and liveness without touching target exports.
func (s *SQLiteStore) UpsertPeer(ctx context.Context, peer atypes.PeerRecord) error {
	capabilitiesJSON, err := json.Marshal(peer.Capabilities)
	if err != nil {
		return fmt.Errorf("marshal peer capabilities: %w", err)
	}
	metadataJSON, err := json.Marshal(peer.Metadata)
	if err != nil {
		return fmt.Errorf("marshal peer metadata: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO peers(peer_id, display_name, base_url, status, capabilities_json, metadata_json, registered_at, updated_at, last_seen_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(peer_id) DO UPDATE SET
			display_name = excluded.display_name,
			base_url = excluded.base_url,
			status = excluded.status,
			capabilities_json = excluded.capabilities_json,
			metadata_json = excluded.metadata_json,
			updated_at = excluded.updated_at,
			last_seen_at = excluded.last_seen_at
	`, peer.PeerID, peer.DisplayName, peer.BaseURL, string(peer.Status), string(capabilitiesJSON), string(metadataJSON), peer.RegisteredAt.Format(time.RFC3339Nano), peer.UpdatedAt.Format(time.RFC3339Nano), peer.LastSeenAt.Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("upsert peer: %w", err)
	}
	return nil
}

// GetPeer returns one persisted peer record by durable peer id.
func (s *SQLiteStore) GetPeer(ctx context.Context, peerID string) (atypes.PeerRecord, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT peer_id, display_name, base_url, status, capabilities_json, metadata_json, registered_at, updated_at, last_seen_at
		FROM peers WHERE peer_id = ?
	`, peerID)
	return scanPeer(row)
}

// ListPeers returns static peers ordered for stable CLI/API output.
func (s *SQLiteStore) ListPeers(ctx context.Context) ([]atypes.PeerRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT peer_id, display_name, base_url, status, capabilities_json, metadata_json, registered_at, updated_at, last_seen_at
		FROM peers ORDER BY display_name ASC, peer_id ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("list peers: %w", err)
	}
	defer rows.Close()
	var peers []atypes.PeerRecord
	for rows.Next() {
		peer, err := scanPeer(rows)
		if err != nil {
			return nil, err
		}
		peers = append(peers, peer)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate peers: %w", err)
	}
	return peers, nil
}

// UpsertPeerTarget caches a peer-exported target for local discovery.
func (s *SQLiteStore) UpsertPeerTarget(ctx context.Context, target atypes.PeerTargetRecord) error {
	capabilitiesJSON, err := json.Marshal(target.Capabilities)
	if err != nil {
		return fmt.Errorf("marshal peer target capabilities: %w", err)
	}
	defaultsJSON, err := json.Marshal(target.Defaults)
	if err != nil {
		return fmt.Errorf("marshal peer target defaults: %w", err)
	}
	metadataJSON, err := json.Marshal(target.Metadata)
	if err != nil {
		return fmt.Errorf("marshal peer target metadata: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO peer_targets(peer_id, target_id, display_name, capabilities_json, defaults_json, metadata_json, status, synced_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(peer_id, target_id) DO UPDATE SET
			display_name = excluded.display_name,
			capabilities_json = excluded.capabilities_json,
			defaults_json = excluded.defaults_json,
			metadata_json = excluded.metadata_json,
			status = excluded.status,
			synced_at = excluded.synced_at
	`, target.PeerID, target.TargetID, target.DisplayName, string(capabilitiesJSON), string(defaultsJSON), string(metadataJSON), string(target.Status), target.SyncedAt.Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("upsert peer target: %w", err)
	}
	return nil
}

// ListPeerTargets returns cached remote targets even when their peer is offline.
func (s *SQLiteStore) ListPeerTargets(ctx context.Context) ([]atypes.PeerTargetRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT peer_id, target_id, display_name, capabilities_json, defaults_json, metadata_json, status, synced_at
		FROM peer_targets ORDER BY target_id ASC, peer_id ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("list peer targets: %w", err)
	}
	defer rows.Close()
	var targets []atypes.PeerTargetRecord
	for rows.Next() {
		target, err := scanPeerTarget(rows)
		if err != nil {
			return nil, err
		}
		targets = append(targets, target)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate peer targets: %w", err)
	}
	return targets, nil
}

// UpsertRemoteTaskBinding persists the origin-to-destination execution mapping.
func (s *SQLiteStore) UpsertRemoteTaskBinding(ctx context.Context, binding atypes.RemoteTaskBinding) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO remote_task_bindings(local_task_id, remote_peer_id, destination_node_id, destination_task_id, destination_thread_id, status, created_at, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(local_task_id) DO UPDATE SET
			remote_peer_id = excluded.remote_peer_id,
			destination_node_id = excluded.destination_node_id,
			destination_task_id = excluded.destination_task_id,
			destination_thread_id = excluded.destination_thread_id,
			status = excluded.status,
			updated_at = excluded.updated_at
	`, binding.LocalTaskID, binding.RemotePeerID, binding.DestinationNodeID, binding.DestinationTaskID, nullString(binding.DestinationThreadID), binding.Status, binding.CreatedAt.Format(time.RFC3339Nano), binding.UpdatedAt.Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("upsert remote task binding: %w", err)
	}
	return nil
}

// GetRemoteTaskBinding returns the destination execution identity for a local proxy task.
func (s *SQLiteStore) GetRemoteTaskBinding(ctx context.Context, localTaskID string) (atypes.RemoteTaskBinding, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT local_task_id, remote_peer_id, destination_node_id, destination_task_id, COALESCE(destination_thread_id, ''), status, created_at, updated_at
		FROM remote_task_bindings WHERE local_task_id = ?
	`, localTaskID)
	return scanRemoteTaskBinding(row)
}

// UpdateRemoteTaskBindingStatus records relay progress without changing task ownership ids.
func (s *SQLiteStore) UpdateRemoteTaskBindingStatus(ctx context.Context, localTaskID string, status string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE remote_task_bindings SET status = ?, updated_at = ? WHERE local_task_id = ?`, status, atypes.NowUTC().Format(time.RFC3339Nano), localTaskID)
	if err != nil {
		return fmt.Errorf("update remote task binding status: %w", err)
	}
	return nil
}

// MarkInterruptedRemoteRelayBindingsOnRestart makes unknown in-flight relays restart-visible.
func (s *SQLiteStore) MarkInterruptedRemoteRelayBindingsOnRestart(ctx context.Context) ([]atypes.RemoteTaskBinding, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT local_task_id, remote_peer_id, destination_node_id, destination_task_id, COALESCE(destination_thread_id, ''), status, created_at, updated_at
		FROM remote_task_bindings
		WHERE status NOT IN (?, ?, ?, ?)
	`, string(atypes.TaskStatusCompleted), string(atypes.TaskStatusFailed), string(atypes.TaskStatusCancelled), atypes.RemoteRelayStatusInterrupted)
	if err != nil {
		return nil, fmt.Errorf("list interrupted remote task bindings: %w", err)
	}
	defer rows.Close()

	var interrupted []atypes.RemoteTaskBinding
	for rows.Next() {
		binding, err := scanRemoteTaskBinding(rows)
		if err != nil {
			return nil, err
		}
		interrupted = append(interrupted, binding)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("scan interrupted remote task bindings: %w", err)
	}
	if len(interrupted) == 0 {
		return nil, nil
	}

	now := atypes.NowUTC().Format(time.RFC3339Nano)
	_, err = s.db.ExecContext(ctx, `
		UPDATE remote_task_bindings
		SET status = ?, updated_at = ?
		WHERE status NOT IN (?, ?, ?, ?)
	`, atypes.RemoteRelayStatusInterrupted, now, string(atypes.TaskStatusCompleted), string(atypes.TaskStatusFailed), string(atypes.TaskStatusCancelled), atypes.RemoteRelayStatusInterrupted)
	if err != nil {
		return nil, fmt.Errorf("mark interrupted remote task bindings on restart: %w", err)
	}
	return interrupted, nil
}

// scanPeer hydrates peer rows while preserving JSON extension fields.
func scanPeer(scanner interface{ Scan(dest ...any) error }) (atypes.PeerRecord, error) {
	var peer atypes.PeerRecord
	var status, capabilitiesJSON, metadataJSON, registeredAt, updatedAt, lastSeenAt string
	if err := scanner.Scan(&peer.PeerID, &peer.DisplayName, &peer.BaseURL, &status, &capabilitiesJSON, &metadataJSON, &registeredAt, &updatedAt, &lastSeenAt); err != nil {
		return atypes.PeerRecord{}, err
	}
	peer.Status = atypes.PeerStatus(status)
	if err := json.Unmarshal([]byte(capabilitiesJSON), &peer.Capabilities); err != nil {
		return atypes.PeerRecord{}, fmt.Errorf("unmarshal peer capabilities: %w", err)
	}
	if err := json.Unmarshal([]byte(metadataJSON), &peer.Metadata); err != nil {
		return atypes.PeerRecord{}, fmt.Errorf("unmarshal peer metadata: %w", err)
	}
	var err error
	if peer.RegisteredAt, err = time.Parse(time.RFC3339Nano, registeredAt); err != nil {
		return atypes.PeerRecord{}, fmt.Errorf("parse peer registered_at: %w", err)
	}
	if peer.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt); err != nil {
		return atypes.PeerRecord{}, fmt.Errorf("parse peer updated_at: %w", err)
	}
	if peer.LastSeenAt, err = time.Parse(time.RFC3339Nano, lastSeenAt); err != nil {
		return atypes.PeerRecord{}, fmt.Errorf("parse peer last_seen_at: %w", err)
	}
	return peer, nil
}

// scanPeerTarget hydrates one cached exported target row.
func scanPeerTarget(scanner interface{ Scan(dest ...any) error }) (atypes.PeerTargetRecord, error) {
	var target atypes.PeerTargetRecord
	var capabilitiesJSON, defaultsJSON, metadataJSON, status, syncedAt string
	if err := scanner.Scan(&target.PeerID, &target.TargetID, &target.DisplayName, &capabilitiesJSON, &defaultsJSON, &metadataJSON, &status, &syncedAt); err != nil {
		return atypes.PeerTargetRecord{}, err
	}
	if err := json.Unmarshal([]byte(capabilitiesJSON), &target.Capabilities); err != nil {
		return atypes.PeerTargetRecord{}, fmt.Errorf("unmarshal peer target capabilities: %w", err)
	}
	if err := json.Unmarshal([]byte(defaultsJSON), &target.Defaults); err != nil {
		return atypes.PeerTargetRecord{}, fmt.Errorf("unmarshal peer target defaults: %w", err)
	}
	if err := json.Unmarshal([]byte(metadataJSON), &target.Metadata); err != nil {
		return atypes.PeerTargetRecord{}, fmt.Errorf("unmarshal peer target metadata: %w", err)
	}
	target.Status = atypes.PeerTargetStatus(status)
	parsed, err := time.Parse(time.RFC3339Nano, syncedAt)
	if err != nil {
		return atypes.PeerTargetRecord{}, fmt.Errorf("parse peer target synced_at: %w", err)
	}
	target.SyncedAt = parsed
	return target, nil
}

// scanRemoteTaskBinding hydrates one durable proxy-to-destination mapping row.
func scanRemoteTaskBinding(scanner interface{ Scan(dest ...any) error }) (atypes.RemoteTaskBinding, error) {
	var binding atypes.RemoteTaskBinding
	var createdAt, updatedAt string
	if err := scanner.Scan(&binding.LocalTaskID, &binding.RemotePeerID, &binding.DestinationNodeID, &binding.DestinationTaskID, &binding.DestinationThreadID, &binding.Status, &createdAt, &updatedAt); err != nil {
		return atypes.RemoteTaskBinding{}, err
	}
	var err error
	if binding.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt); err != nil {
		return atypes.RemoteTaskBinding{}, fmt.Errorf("parse remote binding created_at: %w", err)
	}
	if binding.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt); err != nil {
		return atypes.RemoteTaskBinding{}, fmt.Errorf("parse remote binding updated_at: %w", err)
	}
	return binding, nil
}
