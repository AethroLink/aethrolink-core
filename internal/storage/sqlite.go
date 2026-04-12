package storage

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	atypes "github.com/aethrolink/aethrolink-core/pkg/types"
)

type SQLiteStore struct {
	db              *sql.DB
	artifactDir     string
	artifactBaseURL string
}

// Open initializes the durable local state for runtimes, tasks, events,
// leases, and artifacts. This store is the source of truth for
// restart-visible history.
func Open(databaseURL, artifactDir, artifactBaseURL string) (*SQLiteStore, error) {
	driverURL := normalizeSQLiteURL(databaseURL)
	db, err := sql.Open("sqlite", driverURL)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	store := &SQLiteStore{
		db:              db,
		artifactDir:     artifactDir,
		artifactBaseURL: strings.TrimRight(artifactBaseURL, "/"),
	}
	if err := store.migrate(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("create artifact dir: %w", err)
	}
	return store, nil
}

func normalizeSQLiteURL(databaseURL string) string {
	if databaseURL == "sqlite::memory:" || databaseURL == ":memory:" {
		return ":memory:"
	}
	if strings.HasPrefix(databaseURL, "sqlite://") {
		trimmed := strings.TrimPrefix(databaseURL, "sqlite://")
		if strings.HasPrefix(trimmed, "./") || strings.HasPrefix(trimmed, "/") {
			return trimmed
		}
		return "./" + trimmed
	}
	return databaseURL
}

func (s *SQLiteStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *SQLiteStore) migrate(ctx context.Context) error {
	// Keep schema setup explicit and append-only for now. The v0.1 surface is
	// small enough that a single bootstrap migration is easier to audit.
	stmts := []string{
		`PRAGMA journal_mode=WAL;`,
		`CREATE TABLE IF NOT EXISTS runtimes (runtime_id TEXT PRIMARY KEY, adapter_kind TEXT NOT NULL, spec_json TEXT NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS runtime_leases (lease_id TEXT PRIMARY KEY, runtime_id TEXT NOT NULL, subcontext_key TEXT, process_id TEXT, metadata_json TEXT NOT NULL, created_at TEXT NOT NULL, released_at TEXT)`,
		`CREATE INDEX IF NOT EXISTS idx_runtime_leases_runtime_subcontext_released ON runtime_leases(runtime_id, subcontext_key, released_at)`,
		`CREATE TABLE IF NOT EXISTS tasks (task_id TEXT PRIMARY KEY, conversation_id TEXT NOT NULL, sender TEXT NOT NULL, intent TEXT NOT NULL, requested_runtime TEXT, resolved_runtime TEXT, runtime_options_json TEXT NOT NULL, payload_artifact_id TEXT, status TEXT NOT NULL, remote_binding TEXT, remote_execution_id TEXT, remote_session_id TEXT, last_error_json TEXT, result_artifact_id TEXT, created_at TEXT NOT NULL, updated_at TEXT NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS idx_tasks_conversation ON tasks(conversation_id)`,
		`CREATE INDEX IF NOT EXISTS idx_tasks_status ON tasks(status)`,
		`CREATE INDEX IF NOT EXISTS idx_tasks_runtime_status ON tasks(resolved_runtime, status)`,
		`CREATE TABLE IF NOT EXISTS task_events (event_id TEXT PRIMARY KEY, task_id TEXT NOT NULL, seq INTEGER NOT NULL, kind TEXT NOT NULL, state TEXT NOT NULL, source TEXT NOT NULL, message TEXT, data_json TEXT NOT NULL, created_at TEXT NOT NULL, UNIQUE(task_id, seq))`,
		`CREATE INDEX IF NOT EXISTS idx_task_events_task_seq ON task_events(task_id, seq)`,
		`CREATE TABLE IF NOT EXISTS artifacts (artifact_id TEXT PRIMARY KEY, media_type TEXT NOT NULL, relative_path TEXT NOT NULL, size_bytes INTEGER NOT NULL, sha256 TEXT NOT NULL, created_at TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS launch_history (launch_id TEXT PRIMARY KEY, runtime_id TEXT NOT NULL, subcontext_key TEXT, command_json TEXT NOT NULL, pid TEXT, status TEXT NOT NULL, error_text TEXT, started_at TEXT NOT NULL, ended_at TEXT)`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("migrate sqlite: %w", err)
		}
	}
	return nil
}

func (s *SQLiteStore) UpsertRuntime(ctx context.Context, spec atypes.RuntimeSpec) error {
	specJSON, err := json.Marshal(spec)
	if err != nil {
		return fmt.Errorf("marshal runtime spec: %w", err)
	}
	now := atypes.NowUTC().Format(time.RFC3339Nano)
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO runtimes(runtime_id, adapter_kind, spec_json, created_at, updated_at)
		VALUES(?, ?, ?, ?, ?)
		ON CONFLICT(runtime_id) DO UPDATE SET
			adapter_kind=excluded.adapter_kind,
			spec_json=excluded.spec_json,
			updated_at=excluded.updated_at
	`, spec.RuntimeID, spec.Adapter, string(specJSON), now, now)
	if err != nil {
		return fmt.Errorf("upsert runtime: %w", err)
	}
	return nil
}

func (s *SQLiteStore) StoreJSONArtifact(ctx context.Context, value map[string]any) (atypes.ArtifactRef, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return atypes.ArtifactRef{}, fmt.Errorf("marshal artifact json: %w", err)
	}
	return s.StoreArtifactBytes(ctx, "application/json", payload)
}

func (s *SQLiteStore) StoreArtifactBytes(ctx context.Context, mediaType string, body []byte) (atypes.ArtifactRef, error) {
	// Artifacts are stored as files on disk; the database keeps lookup metadata
	// and integrity fields so tasks/events can reference them durably.
	artifactID := atypes.NewID()
	relativePath := artifactID + ".bin"
	fullPath := filepath.Join(s.artifactDir, relativePath)
	if err := os.WriteFile(fullPath, body, 0o644); err != nil {
		return atypes.ArtifactRef{}, fmt.Errorf("write artifact: %w", err)
	}
	hash := sha256.Sum256(body)
	sha := hex.EncodeToString(hash[:])
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO artifacts(artifact_id, media_type, relative_path, size_bytes, sha256, created_at)
		VALUES(?, ?, ?, ?, ?, ?)
	`, artifactID, mediaType, relativePath, len(body), sha, atypes.NowUTC().Format(time.RFC3339Nano))
	if err != nil {
		return atypes.ArtifactRef{}, fmt.Errorf("insert artifact: %w", err)
	}
	return atypes.ArtifactRef{
		ArtifactID: artifactID,
		MediaType:  mediaType,
		URL:        fmt.Sprintf("%s/artifacts/%s", s.artifactBaseURL, artifactID),
		SizeBytes:  int64(len(body)),
		SHA256:     sha,
	}, nil
}

func (s *SQLiteStore) LoadArtifactPath(ctx context.Context, artifactID string) (string, error) {
	var relativePath string
	if err := s.db.QueryRowContext(ctx, `SELECT relative_path FROM artifacts WHERE artifact_id = ?`, artifactID).Scan(&relativePath); err != nil {
		if errorsIsNoRows(err) {
			return "", sql.ErrNoRows
		}
		return "", fmt.Errorf("load artifact path: %w", err)
	}
	return filepath.Join(s.artifactDir, relativePath), nil
}

func (s *SQLiteStore) InsertTask(ctx context.Context, task atypes.TaskRecord) error {
	runtimeOptionsJSON, err := json.Marshal(task.RuntimeOptions)
	if err != nil {
		return fmt.Errorf("marshal runtime options: %w", err)
	}
	var remoteBinding, remoteExecutionID, remoteSessionID any
	if task.Remote != nil {
		remoteBinding = task.Remote.Binding
		remoteExecutionID = task.Remote.RemoteExecutionID
		remoteSessionID = task.Remote.RemoteSessionID
	}
	var lastErrorJSON any
	if task.LastError != nil {
		encoded, err := json.Marshal(task.LastError)
		if err != nil {
			return fmt.Errorf("marshal last error: %w", err)
		}
		lastErrorJSON = string(encoded)
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO tasks(task_id, conversation_id, sender, intent, requested_runtime, resolved_runtime, runtime_options_json, payload_artifact_id, status, remote_binding, remote_execution_id, remote_session_id, last_error_json, result_artifact_id, created_at, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, task.TaskID, task.ConversationID, task.Sender, task.Intent, nullString(task.RequestedRuntime), nullString(task.ResolvedRuntime), string(runtimeOptionsJSON), nullString(task.PayloadArtifactID), string(task.Status), remoteBinding, remoteExecutionID, remoteSessionID, lastErrorJSON, nullString(task.ResultArtifactID), task.CreatedAt.Format(time.RFC3339Nano), task.UpdatedAt.Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("insert task: %w", err)
	}
	return nil
}

func (s *SQLiteStore) GetTask(ctx context.Context, taskID string) (atypes.TaskRecord, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT task_id, conversation_id, sender, intent, requested_runtime, resolved_runtime, runtime_options_json, payload_artifact_id, status, remote_binding, remote_execution_id, remote_session_id, last_error_json, result_artifact_id, created_at, updated_at
		FROM tasks WHERE task_id = ?
	`, taskID)
	return scanTask(row)
}

func (s *SQLiteStore) UpdateTaskState(ctx context.Context, taskID string, status atypes.TaskStatus, remote *atypes.RemoteHandle, lastError *atypes.TaskError, resultArtifactID string) error {
	var remoteBinding, remoteExecutionID, remoteSessionID any
	if remote != nil {
		remoteBinding = remote.Binding
		remoteExecutionID = nullString(remote.RemoteExecutionID)
		remoteSessionID = nullString(remote.RemoteSessionID)
	}
	var lastErrorJSON any
	if lastError != nil {
		encoded, err := json.Marshal(lastError)
		if err != nil {
			return fmt.Errorf("marshal last error: %w", err)
		}
		lastErrorJSON = string(encoded)
	}
	// COALESCE preserves remote binding/session identifiers when a later state
	// transition only updates status or error information.
	_, err := s.db.ExecContext(ctx, `
		UPDATE tasks
		SET status = ?,
		    remote_binding = COALESCE(?, remote_binding),
		    remote_execution_id = COALESCE(?, remote_execution_id),
		    remote_session_id = COALESCE(?, remote_session_id),
		    last_error_json = ?,
		    result_artifact_id = COALESCE(?, result_artifact_id),
		    updated_at = ?
		WHERE task_id = ?
	`, string(status), remoteBinding, remoteExecutionID, remoteSessionID, lastErrorJSON, nullString(resultArtifactID), atypes.NowUTC().Format(time.RFC3339Nano), taskID)
	if err != nil {
		return fmt.Errorf("update task state: %w", err)
	}
	return nil
}

func (s *SQLiteStore) NextSeq(ctx context.Context, taskID string) (int64, error) {
	// Event ordering is task-local and monotonic. Persisted sequence numbers make
	// SSE replay and debugging reflect the exact lifecycle observed by the core.
	var seq int64
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq), 0) FROM task_events WHERE task_id = ?`, taskID).Scan(&seq); err != nil {
		return 0, fmt.Errorf("next seq: %w", err)
	}
	return seq + 1, nil
}

func (s *SQLiteStore) AppendEvent(ctx context.Context, event atypes.TaskEvent) error {
	dataJSON, err := json.Marshal(event.Data)
	if err != nil {
		return fmt.Errorf("marshal event data: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO task_events(event_id, task_id, seq, kind, state, source, message, data_json, created_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, event.EventID, event.TaskID, event.Seq, string(event.Kind), string(event.State), string(event.Source), nullString(event.Message), string(dataJSON), event.CreatedAt.Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("append event: %w", err)
	}
	return nil
}

func (s *SQLiteStore) ListEvents(ctx context.Context, taskID string) ([]atypes.TaskEvent, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT event_id, task_id, seq, kind, state, source, message, data_json, created_at
		FROM task_events WHERE task_id = ? ORDER BY seq ASC
	`, taskID)
	if err != nil {
		return nil, fmt.Errorf("query events: %w", err)
	}
	defer rows.Close()
	var events []atypes.TaskEvent
	for rows.Next() {
		event, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate events: %w", err)
	}
	return events, nil
}

func (s *SQLiteStore) InsertLease(ctx context.Context, lease atypes.RuntimeLease) error {
	metadataJSON, err := json.Marshal(lease.Metadata)
	if err != nil {
		return fmt.Errorf("marshal lease metadata: %w", err)
	}
	var releasedAt any
	if lease.ReleasedAt != nil {
		releasedAt = lease.ReleasedAt.Format(time.RFC3339Nano)
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO runtime_leases(lease_id, runtime_id, subcontext_key, process_id, metadata_json, created_at, released_at)
		VALUES(?, ?, ?, ?, ?, ?, ?)
	`, lease.LeaseID, lease.RuntimeID, nullString(lease.SubcontextKey), nullString(lease.ProcessID), string(metadataJSON), lease.CreatedAt.Format(time.RFC3339Nano), releasedAt)
	if err != nil {
		return fmt.Errorf("insert lease: %w", err)
	}
	return nil
}

func (s *SQLiteStore) ReleaseLease(ctx context.Context, leaseID string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE runtime_leases SET released_at = ? WHERE lease_id = ?`, atypes.NowUTC().Format(time.RFC3339Nano), leaseID)
	if err != nil {
		return fmt.Errorf("release lease: %w", err)
	}
	return nil
}

func (s *SQLiteStore) InsertLaunchHistory(ctx context.Context, runtimeID, subcontextKey string, command []string, pid, status, errorText string) (string, error) {
	launchID := atypes.NewID()
	commandJSON, err := json.Marshal(command)
	if err != nil {
		return "", fmt.Errorf("marshal launch command: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO launch_history(launch_id, runtime_id, subcontext_key, command_json, pid, status, error_text, started_at, ended_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, NULL)
	`, launchID, runtimeID, nullString(subcontextKey), string(commandJSON), nullString(pid), status, nullString(errorText), atypes.NowUTC().Format(time.RFC3339Nano))
	if err != nil {
		return "", fmt.Errorf("insert launch history: %w", err)
	}
	return launchID, nil
}

func (s *SQLiteStore) FinishLaunchHistory(ctx context.Context, launchID, status, errorText string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE launch_history SET status = ?, error_text = ?, ended_at = ? WHERE launch_id = ?
	`, status, nullString(errorText), atypes.NowUTC().Format(time.RFC3339Nano), launchID)
	if err != nil {
		return fmt.Errorf("finish launch history: %w", err)
	}
	return nil
}

func scanTask(scanner interface{ Scan(dest ...any) error }) (atypes.TaskRecord, error) {
	var (
		task               atypes.TaskRecord
		runtimeOptionsJSON string
		requestedRuntime   sql.NullString
		resolvedRuntime    sql.NullString
		payloadArtifactID  sql.NullString
		status             string
		remoteBinding      sql.NullString
		remoteExecutionID  sql.NullString
		remoteSessionID    sql.NullString
		lastErrorJSON      sql.NullString
		resultArtifactID   sql.NullString
		createdAt          string
		updatedAt          string
	)
	if err := scanner.Scan(&task.TaskID, &task.ConversationID, &task.Sender, &task.Intent, &requestedRuntime, &resolvedRuntime, &runtimeOptionsJSON, &payloadArtifactID, &status, &remoteBinding, &remoteExecutionID, &remoteSessionID, &lastErrorJSON, &resultArtifactID, &createdAt, &updatedAt); err != nil {
		return atypes.TaskRecord{}, err
	}
	if err := json.Unmarshal([]byte(runtimeOptionsJSON), &task.RuntimeOptions); err != nil {
		return atypes.TaskRecord{}, fmt.Errorf("unmarshal runtime options: %w", err)
	}
	task.RequestedRuntime = requestedRuntime.String
	task.ResolvedRuntime = resolvedRuntime.String
	task.PayloadArtifactID = payloadArtifactID.String
	task.Status = atypes.TaskStatus(status)
	if remoteBinding.Valid {
		task.Remote = &atypes.RemoteRef{Binding: remoteBinding.String, RemoteExecutionID: remoteExecutionID.String, RemoteSessionID: remoteSessionID.String}
	}
	if lastErrorJSON.Valid {
		var taskErr atypes.TaskError
		if err := json.Unmarshal([]byte(lastErrorJSON.String), &taskErr); err != nil {
			return atypes.TaskRecord{}, fmt.Errorf("unmarshal last error: %w", err)
		}
		task.LastError = &taskErr
	}
	task.ResultArtifactID = resultArtifactID.String
	var err error
	task.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return atypes.TaskRecord{}, fmt.Errorf("parse created_at: %w", err)
	}
	task.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return atypes.TaskRecord{}, fmt.Errorf("parse updated_at: %w", err)
	}
	return task, nil
}

func scanEvent(scanner interface{ Scan(dest ...any) error }) (atypes.TaskEvent, error) {
	var event atypes.TaskEvent
	var kind, state, source, message, dataJSON, createdAt string
	if err := scanner.Scan(&event.EventID, &event.TaskID, &event.Seq, &kind, &state, &source, &message, &dataJSON, &createdAt); err != nil {
		return atypes.TaskEvent{}, fmt.Errorf("scan event: %w", err)
	}
	event.Kind = atypes.TaskEventKind(kind)
	event.State = atypes.TaskStatus(state)
	event.Source = atypes.EventSource(source)
	event.Message = message
	if err := json.Unmarshal([]byte(dataJSON), &event.Data); err != nil {
		return atypes.TaskEvent{}, fmt.Errorf("unmarshal event data: %w", err)
	}
	parsed, err := time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return atypes.TaskEvent{}, fmt.Errorf("parse event time: %w", err)
	}
	event.CreatedAt = parsed
	return event, nil
}

func nullString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func errorsIsNoRows(err error) bool {
	return err == sql.ErrNoRows
}
