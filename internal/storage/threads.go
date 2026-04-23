package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	atypes "github.com/aethrolink/aethrolink-core/pkg/types"
)

// GetThread loads one persisted thread by its durable thread identifier.
func (s *SQLiteStore) GetThread(ctx context.Context, threadID string) (atypes.ThreadRecord, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT thread_id, agent_a_id, agent_b_id, status, continuity_key, last_task_id, last_actor_agent_id, last_target_agent_id, metadata_json, created_at, updated_at
		FROM threads WHERE thread_id = ?
	`, threadID)
	return scanThread(row)
}

// MarkInterruptedThreadsOnRestart makes non-terminal threads explicit after node restart.
func (s *SQLiteStore) MarkInterruptedThreadsOnRestart(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE threads
		SET status = ?, updated_at = ?
		WHERE status = ?
		  AND last_task_id IS NOT NULL
		  AND EXISTS (
			SELECT 1 FROM tasks
			WHERE tasks.task_id = threads.last_task_id
			  AND tasks.status NOT IN (?, ?, ?)
		  )
	`, string(atypes.ThreadStatusInterrupted), atypes.NowUTC().Format(time.RFC3339Nano), string(atypes.ThreadStatusActive), string(atypes.TaskStatusCompleted), string(atypes.TaskStatusFailed), string(atypes.TaskStatusCancelled))
	if err != nil {
		return fmt.Errorf("mark interrupted threads on restart: %w", err)
	}
	return nil
}

// AppendThreadTurn adds one ordered turn record to a persisted thread history.
func (s *SQLiteStore) AppendThreadTurn(ctx context.Context, turn atypes.ThreadTurn) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO thread_turns(thread_id, turn_index, task_id, sender_agent_id, target_agent_id, remote_session_id, remote_execution_id, status, created_at, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, turn.ThreadID, turn.TurnIndex, nullString(turn.TaskID), turn.SenderAgentID, turn.TargetAgentID, nullString(turn.RemoteSessionID), nullString(turn.RemoteExecutionID), turn.Status, turn.CreatedAt.Format(time.RFC3339Nano), turn.UpdatedAt.Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("append thread turn: %w", err)
	}
	return nil
}

// ListThreadTurns returns the persisted turn log in conversation order.
func (s *SQLiteStore) ListThreadTurns(ctx context.Context, threadID string) ([]atypes.ThreadTurn, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT thread_id, turn_index, task_id, sender_agent_id, target_agent_id, remote_session_id, remote_execution_id, status, created_at, updated_at
		FROM thread_turns WHERE thread_id = ? ORDER BY turn_index ASC
	`, threadID)
	if err != nil {
		return nil, fmt.Errorf("query thread turns: %w", err)
	}
	defer rows.Close()
	var turns []atypes.ThreadTurn
	for rows.Next() {
		turn, err := scanThreadTurn(rows)
		if err != nil {
			return nil, err
		}
		turns = append(turns, turn)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate thread turns: %w", err)
	}
	return turns, nil
}

// AppendThreadTurnAndUpdateThread keeps thread state and ordered turn history coherent.
func (s *SQLiteStore) AppendThreadTurnAndUpdateThread(ctx context.Context, threadID string, turn atypes.ThreadTurn, lastTaskID, lastActorAgentID, lastTargetAgentID string, updatedAt time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin thread mutation tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if _, err := tx.ExecContext(ctx, `
		UPDATE threads
		SET last_task_id = ?, last_actor_agent_id = ?, last_target_agent_id = ?, status = ?, updated_at = ?
		WHERE thread_id = ?
	`, nullString(lastTaskID), nullString(lastActorAgentID), nullString(lastTargetAgentID), string(atypes.ThreadStatusActive), updatedAt.Format(time.RFC3339Nano), threadID); err != nil {
		return fmt.Errorf("update thread progress: %w", err)
	}
	var nextTurnIndex int64
	// Allocate turn order inside the same transaction as the insert so two
	// concurrent continuations cannot claim the same slot for one thread.
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(turn_index), 0) + 1 FROM thread_turns WHERE thread_id = ?
	`, threadID).Scan(&nextTurnIndex); err != nil {
		return fmt.Errorf("select next thread turn index: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO thread_turns(thread_id, turn_index, task_id, sender_agent_id, target_agent_id, remote_session_id, remote_execution_id, status, created_at, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, turn.ThreadID, nextTurnIndex, nullString(turn.TaskID), turn.SenderAgentID, turn.TargetAgentID, nullString(turn.RemoteSessionID), nullString(turn.RemoteExecutionID), turn.Status, turn.CreatedAt.Format(time.RFC3339Nano), turn.UpdatedAt.Format(time.RFC3339Nano)); err != nil {
		return fmt.Errorf("append thread turn: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit thread mutation tx: %w", err)
	}
	committed = true
	return nil
}

// UpdateThreadTurnStatusByTaskID mirrors task lifecycle changes onto thread history.
func (s *SQLiteStore) UpdateThreadTurnStatusByTaskID(ctx context.Context, taskID string, status string, remote *atypes.RemoteHandle) error {
	var remoteSessionID, remoteExecutionID any
	if remote != nil {
		remoteSessionID = nullString(remote.RemoteSessionID)
		remoteExecutionID = nullString(remote.RemoteExecutionID)
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE thread_turns
		SET status = ?,
		    remote_session_id = COALESCE(?, remote_session_id),
		    remote_execution_id = COALESCE(?, remote_execution_id),
		    updated_at = ?
		WHERE task_id = ?
	`, status, remoteSessionID, remoteExecutionID, atypes.NowUTC().Format(time.RFC3339Nano), taskID)
	if err != nil {
		return fmt.Errorf("update thread turn status: %w", err)
	}
	return nil
}

// scanThread reconstructs one persisted thread row into its typed record form.
func scanThread(scanner interface{ Scan(dest ...any) error }) (atypes.ThreadRecord, error) {
	var (
		thread            atypes.ThreadRecord
		status            string
		lastTaskID        sql.NullString
		lastActorAgentID  sql.NullString
		lastTargetAgentID sql.NullString
		metadataJSON      string
		createdAt         string
		updatedAt         string
	)
	if err := scanner.Scan(&thread.ThreadID, &thread.AgentAID, &thread.AgentBID, &status, &thread.ContinuityKey, &lastTaskID, &lastActorAgentID, &lastTargetAgentID, &metadataJSON, &createdAt, &updatedAt); err != nil {
		return atypes.ThreadRecord{}, err
	}
	thread.Status = atypes.ThreadStatus(status)
	thread.LastTaskID = lastTaskID.String
	thread.LastActorAgentID = lastActorAgentID.String
	thread.LastTargetAgentID = lastTargetAgentID.String
	if err := json.Unmarshal([]byte(metadataJSON), &thread.Metadata); err != nil {
		return atypes.ThreadRecord{}, fmt.Errorf("unmarshal thread metadata: %w", err)
	}
	var err error
	thread.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return atypes.ThreadRecord{}, fmt.Errorf("parse thread created_at: %w", err)
	}
	thread.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return atypes.ThreadRecord{}, fmt.Errorf("parse thread updated_at: %w", err)
	}
	return thread, nil
}

// scanThreadTurn reconstructs one ordered turn row for thread history queries.
func scanThreadTurn(scanner interface{ Scan(dest ...any) error }) (atypes.ThreadTurn, error) {
	var (
		turn              atypes.ThreadTurn
		taskID            sql.NullString
		remoteSessionID   sql.NullString
		remoteExecutionID sql.NullString
		createdAt         string
		updatedAt         string
	)
	if err := scanner.Scan(&turn.ThreadID, &turn.TurnIndex, &taskID, &turn.SenderAgentID, &turn.TargetAgentID, &remoteSessionID, &remoteExecutionID, &turn.Status, &createdAt, &updatedAt); err != nil {
		return atypes.ThreadTurn{}, err
	}
	turn.TaskID = taskID.String
	turn.RemoteSessionID = remoteSessionID.String
	turn.RemoteExecutionID = remoteExecutionID.String
	var err error
	turn.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return atypes.ThreadTurn{}, fmt.Errorf("parse thread turn created_at: %w", err)
	}
	turn.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return atypes.ThreadTurn{}, fmt.Errorf("parse thread turn updated_at: %w", err)
	}
	return turn, nil
}
