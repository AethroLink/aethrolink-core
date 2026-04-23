package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	atypes "github.com/aethrolink/aethrolink-core/pkg/types"
)

func (s *SQLiteStore) UpsertSessionBinding(ctx context.Context, binding atypes.SessionBinding) error {
	metadataJSON, err := json.Marshal(binding.Metadata)
	if err != nil {
		return fmt.Errorf("marshal session binding metadata: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO session_bindings(target_id, subcontext_key, sticky_key, adapter, remote_session_id, metadata_json, created_at, updated_at, last_used_at, last_activity_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(target_id, subcontext_key, sticky_key) DO UPDATE SET
			adapter = excluded.adapter,
			remote_session_id = excluded.remote_session_id,
			metadata_json = excluded.metadata_json,
			updated_at = excluded.updated_at,
			last_used_at = excluded.last_used_at,
			last_activity_at = excluded.last_activity_at
	`, binding.TargetID, binding.SubcontextKey, binding.StickyKey, binding.Adapter, binding.RemoteSessionID, string(metadataJSON), binding.CreatedAt.Format(time.RFC3339Nano), binding.UpdatedAt.Format(time.RFC3339Nano), binding.LastUsedAt.Format(time.RFC3339Nano), binding.LastActivityAt.Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("upsert session binding: %w", err)
	}
	return nil
}

func (s *SQLiteStore) GetSessionBinding(ctx context.Context, targetID, subcontextKey, stickyKey string) (atypes.SessionBinding, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT target_id, subcontext_key, sticky_key, adapter, remote_session_id, metadata_json, created_at, updated_at, last_used_at, last_activity_at
		FROM session_bindings WHERE target_id = ? AND subcontext_key = ? AND sticky_key = ?
	`, targetID, subcontextKey, stickyKey)
	return scanSessionBinding(row)
}

// ListSessionBindingsByStickyKey exposes all reusable bindings for one continuity key.
func (s *SQLiteStore) ListSessionBindingsByStickyKey(ctx context.Context, stickyKey string) ([]atypes.SessionBinding, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT target_id, subcontext_key, sticky_key, adapter, remote_session_id, metadata_json, created_at, updated_at, last_used_at, last_activity_at
		FROM session_bindings
		WHERE sticky_key = ?
		ORDER BY target_id ASC, subcontext_key ASC
	`, stickyKey)
	if err != nil {
		return nil, fmt.Errorf("query session bindings by sticky key: %w", err)
	}
	defer rows.Close()
	var bindings []atypes.SessionBinding
	for rows.Next() {
		binding, err := scanSessionBinding(rows)
		if err != nil {
			return nil, err
		}
		bindings = append(bindings, binding)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate session bindings by sticky key: %w", err)
	}
	return bindings, nil
}

func (s *SQLiteStore) TouchSessionBindingActivity(ctx context.Context, targetID, subcontextKey, stickyKey string, touchedAt time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE session_bindings SET updated_at = ?, last_activity_at = ?
		WHERE target_id = ? AND subcontext_key = ? AND sticky_key = ?
	`, touchedAt.Format(time.RFC3339Nano), touchedAt.Format(time.RFC3339Nano), targetID, subcontextKey, stickyKey)
	if err != nil {
		return fmt.Errorf("touch session binding activity: %w", err)
	}
	return nil
}

func scanSessionBinding(scanner interface{ Scan(dest ...any) error }) (atypes.SessionBinding, error) {
	var (
		binding        atypes.SessionBinding
		metadataJSON   string
		createdAt      string
		updatedAt      string
		lastUsedAt     string
		lastActivityAt string
	)
	if err := scanner.Scan(&binding.TargetID, &binding.SubcontextKey, &binding.StickyKey, &binding.Adapter, &binding.RemoteSessionID, &metadataJSON, &createdAt, &updatedAt, &lastUsedAt, &lastActivityAt); err != nil {
		return atypes.SessionBinding{}, err
	}
	if err := json.Unmarshal([]byte(metadataJSON), &binding.Metadata); err != nil {
		return atypes.SessionBinding{}, fmt.Errorf("unmarshal session binding metadata: %w", err)
	}
	var err error
	binding.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return atypes.SessionBinding{}, fmt.Errorf("parse session binding created_at: %w", err)
	}
	binding.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return atypes.SessionBinding{}, fmt.Errorf("parse session binding updated_at: %w", err)
	}
	binding.LastUsedAt, err = time.Parse(time.RFC3339Nano, lastUsedAt)
	if err != nil {
		return atypes.SessionBinding{}, fmt.Errorf("parse session binding last_used_at: %w", err)
	}
	binding.LastActivityAt, err = time.Parse(time.RFC3339Nano, lastActivityAt)
	if err != nil {
		return atypes.SessionBinding{}, fmt.Errorf("parse session binding last_activity_at: %w", err)
	}
	return binding, nil
}
