package storage

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	atypes "github.com/aethrolink/aethrolink-core/pkg/types"
)

func TestSQLiteStorePersistsTaskAndEvents(t *testing.T) {
	tmp := t.TempDir()
	store, err := Open(filepath.Join(tmp, "aethrolink.db"), filepath.Join(tmp, "artifacts"), "http://127.0.0.1:7777")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	task := atypes.TaskRecord{
		TaskID:          atypes.NewID(),
		ConversationID:  atypes.NewID(),
		Sender:          "local",
		Intent:          "code.patch",
		ResolvedAgentID: "hermes",
		RuntimeOptions:  map[string]any{"executor": "coder"},
		Status:          atypes.TaskStatusCreated,
		CreatedAt:       time.Now().UTC(),
		UpdatedAt:       time.Now().UTC(),
	}
	if err := store.InsertTask(ctx, task); err != nil {
		t.Fatalf("insert task: %v", err)
	}
	seq, err := store.NextSeq(ctx, task.TaskID)
	if err != nil {
		t.Fatalf("next seq: %v", err)
	}
	if seq != 1 {
		t.Fatalf("expected seq 1, got %d", seq)
	}
	event := atypes.TaskEvent{
		EventID:   atypes.NewID(),
		TaskID:    task.TaskID,
		Seq:       1,
		Kind:      atypes.TaskEventTaskCreated,
		State:     atypes.TaskStatusCreated,
		Source:    atypes.EventSourceCore,
		Message:   "Task created",
		Data:      map[string]any{"ok": true},
		CreatedAt: time.Now().UTC(),
	}
	if err := store.AppendEvent(ctx, event); err != nil {
		t.Fatalf("append event: %v", err)
	}
	loaded, err := store.GetTask(ctx, task.TaskID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if loaded.Intent != task.Intent {
		t.Fatalf("expected intent %s, got %s", task.Intent, loaded.Intent)
	}
	events, err := store.ListEvents(ctx, task.TaskID)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
}

func TestSQLiteStorePersistsSessionBinding(t *testing.T) {
	tmp := t.TempDir()
	store, err := Open(filepath.Join(tmp, "aethrolink.db"), filepath.Join(tmp, "artifacts"), "http://127.0.0.1:7777")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	binding := atypes.SessionBinding{
		TargetID:        "hermes",
		SubcontextKey:   "executor:coder",
		StickyKey:       "conversation:abc",
		Adapter:         "hermes",
		RemoteSessionID: "sess-1",
		Metadata:        map[string]any{"executor": "coder"},
		CreatedAt:       time.Now().UTC(),
		UpdatedAt:       time.Now().UTC(),
		LastUsedAt:      time.Now().UTC(),
		LastActivityAt:  time.Now().UTC(),
	}
	if err := store.UpsertSessionBinding(ctx, binding); err != nil {
		t.Fatalf("upsert session binding: %v", err)
	}
	loaded, err := store.GetSessionBinding(ctx, "hermes", "executor:coder", "conversation:abc")
	if err != nil {
		t.Fatalf("get session binding: %v", err)
	}
	if loaded.RemoteSessionID != "sess-1" {
		t.Fatalf("expected sess-1, got %q", loaded.RemoteSessionID)
	}
	if err := store.TouchSessionBindingActivity(ctx, "hermes", "executor:coder", "conversation:abc", time.Now().UTC().Add(1*time.Minute)); err != nil {
		t.Fatalf("touch session binding activity: %v", err)
	}
}

func TestSQLiteStoreAppendEventAndUpdateTaskMutatesTogether(t *testing.T) {
	tmp := t.TempDir()
	store, err := Open(filepath.Join(tmp, "aethrolink.db"), filepath.Join(tmp, "artifacts"), "http://127.0.0.1:7777")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	task := atypes.TaskRecord{
		TaskID:          atypes.NewID(),
		ConversationID:  atypes.NewID(),
		Sender:          "local",
		Intent:          "code.patch",
		ResolvedAgentID: "hermes",
		RuntimeOptions:  map[string]any{"executor": "coder"},
		Status:          atypes.TaskStatusLaunching,
		CreatedAt:       time.Now().UTC(),
		UpdatedAt:       time.Now().UTC(),
	}
	if err := store.InsertTask(ctx, task); err != nil {
		t.Fatalf("insert task: %v", err)
	}

	event := atypes.TaskEvent{
		EventID:   atypes.NewID(),
		TaskID:    task.TaskID,
		Seq:       1,
		Kind:      atypes.TaskEventTaskFailed,
		State:     atypes.TaskStatusFailed,
		Source:    atypes.EventSourceRuntime,
		Message:   "launch_failed",
		Data:      map[string]any{"detail": "boom"},
		CreatedAt: time.Now().UTC(),
	}
	taskErr := &atypes.TaskError{Reason: "launch_failed", Detail: "boom"}
	if err := store.AppendEventAndUpdateTask(ctx, event, nil, taskErr, ""); err != nil {
		t.Fatalf("append event and update task: %v", err)
	}

	loaded, err := store.GetTask(ctx, task.TaskID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if loaded.Status != atypes.TaskStatusFailed {
		t.Fatalf("expected failed task status, got %q", loaded.Status)
	}
	if loaded.LastError == nil || loaded.LastError.Reason != "launch_failed" {
		t.Fatalf("expected persisted last_error, got %#v", loaded.LastError)
	}

	events, err := store.ListEvents(ctx, task.TaskID)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(events) != 1 || events[0].State != atypes.TaskStatusFailed {
		t.Fatalf("expected one failed event, got %#v", events)
	}
}

func TestSQLiteStorePersistsAgentsAndHeartbeats(t *testing.T) {
	tmp := t.TempDir()
	store, err := Open(filepath.Join(tmp, "aethrolink.db"), filepath.Join(tmp, "artifacts"), "http://127.0.0.1:7777")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	now := time.Now().UTC()
	agent := atypes.AgentRecord{
		AgentID:        atypes.NewID(),
		DisplayName:    "hermes-dev",
		TransportKind:  "local_managed",
		Capabilities:   []string{"agent.runtime", "code.patch"},
		StickyMode:     "conversation",
		Metadata:       map[string]any{"profile": "aethrolink-agent"},
		Status:         atypes.AgentStatusOnline,
		RegisteredAt:   now,
		UpdatedAt:      now,
		LastSeenAt:     now,
		LeaseExpiresAt: now.Add(5 * time.Minute),
	}
	if err := store.UpsertAgent(ctx, agent); err != nil {
		t.Fatalf("upsert agent: %v", err)
	}

	loaded, err := store.GetAgent(ctx, agent.AgentID)
	if err != nil {
		t.Fatalf("get agent: %v", err)
	}
	if loaded.DisplayName != agent.DisplayName {
		t.Fatalf("expected display name %q, got %q", agent.DisplayName, loaded.DisplayName)
	}
	if len(loaded.Capabilities) != 2 {
		t.Fatalf("expected capabilities to persist, got %#v", loaded.Capabilities)
	}

	heartbeatAt := now.Add(2 * time.Minute)
	if err := store.TouchAgentLease(ctx, agent.AgentID, heartbeatAt, heartbeatAt.Add(5*time.Minute)); err != nil {
		t.Fatalf("touch agent lease: %v", err)
	}
	loaded, err = store.GetAgent(ctx, agent.AgentID)
	if err != nil {
		t.Fatalf("get agent after heartbeat: %v", err)
	}
	if loaded.LastSeenAt.Before(heartbeatAt) {
		t.Fatalf("expected last seen at to advance, got %s", loaded.LastSeenAt)
	}

	if err := store.MarkAgentOffline(ctx, agent.AgentID); err != nil {
		t.Fatalf("mark agent offline: %v", err)
	}
	loaded, err = store.GetAgent(ctx, agent.AgentID)
	if err != nil {
		t.Fatalf("get agent after offline: %v", err)
	}
	if loaded.Status != atypes.AgentStatusOffline {
		t.Fatalf("expected offline status, got %q", loaded.Status)
	}
}

func TestSQLiteStorePersistsThreadsAndTurnsAcrossRestart(t *testing.T) {
	tmp := t.TempDir()
	databasePath := filepath.Join(tmp, "aethrolink.db")
	artifactPath := filepath.Join(tmp, "artifacts")
	ctx := context.Background()
	now := time.Now().UTC()
	thread := atypes.ThreadRecord{
		ThreadID:          atypes.NewID(),
		AgentAID:          "core",
		AgentBID:          "openclaw_main",
		Status:            atypes.ThreadStatusActive,
		ContinuityKey:     "thread:core-openclaw",
		LastTaskID:        "task-2",
		LastActorAgentID:  "openclaw_main",
		LastTargetAgentID: "core",
		Metadata:          map[string]any{"purpose": "roundtrip"},
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	turnOne := atypes.ThreadTurn{
		ThreadID:          thread.ThreadID,
		TurnIndex:         1,
		TaskID:            "task-1",
		SenderAgentID:     "core",
		TargetAgentID:     "openclaw_main",
		RemoteSessionID:   "session-1",
		RemoteExecutionID: "exec-1",
		Status:            string(atypes.TaskStatusCompleted),
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	turnTwo := atypes.ThreadTurn{
		ThreadID:          thread.ThreadID,
		TurnIndex:         2,
		TaskID:            "task-2",
		SenderAgentID:     "openclaw_main",
		TargetAgentID:     "core",
		RemoteSessionID:   "session-1",
		RemoteExecutionID: "exec-2",
		Status:            string(atypes.TaskStatusCompleted),
		CreatedAt:         now.Add(1 * time.Minute),
		UpdatedAt:         now.Add(1 * time.Minute),
	}

	store, err := Open(databasePath, artifactPath, "http://127.0.0.1:7777")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := store.InsertThread(ctx, thread); err != nil {
		t.Fatalf("insert thread: %v", err)
	}
	if err := store.AppendThreadTurn(ctx, turnOne); err != nil {
		t.Fatalf("append turn one: %v", err)
	}
	if err := store.AppendThreadTurn(ctx, turnTwo); err != nil {
		t.Fatalf("append turn two: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	reopened, err := Open(databasePath, artifactPath, "http://127.0.0.1:7777")
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer reopened.Close()

	loadedThread, err := reopened.GetThread(ctx, thread.ThreadID)
	if err != nil {
		t.Fatalf("get thread: %v", err)
	}
	if loadedThread.AgentAID != thread.AgentAID || loadedThread.AgentBID != thread.AgentBID {
		t.Fatalf("expected persisted thread participants, got %#v", loadedThread)
	}
	if loadedThread.LastTaskID != "task-2" {
		t.Fatalf("expected last task to persist, got %q", loadedThread.LastTaskID)
	}
	if loadedThread.Metadata["purpose"] != "roundtrip" {
		t.Fatalf("expected metadata to persist, got %#v", loadedThread.Metadata)
	}

	turns, err := reopened.ListThreadTurns(ctx, thread.ThreadID)
	if err != nil {
		t.Fatalf("list thread turns: %v", err)
	}
	if len(turns) != 2 {
		t.Fatalf("expected 2 turns, got %d", len(turns))
	}
	if turns[0].TurnIndex != 1 || turns[0].TaskID != "task-1" {
		t.Fatalf("expected first persisted turn to stay first, got %#v", turns[0])
	}
	if turns[1].TurnIndex != 2 || turns[1].TaskID != "task-2" {
		t.Fatalf("expected second persisted turn to stay second, got %#v", turns[1])
	}
	if turns[0].RemoteSessionID != turns[1].RemoteSessionID {
		t.Fatalf("expected thread continuity session reuse, got %#v", turns)
	}
}

func TestSQLiteStorePersistsRemoteThreadTurnBinding(t *testing.T) {
	tmp := t.TempDir()
	store, err := Open(filepath.Join(tmp, "aethrolink.db"), filepath.Join(tmp, "artifacts"), "http://127.0.0.1:7777")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	now := time.Now().UTC()
	thread := atypes.ThreadRecord{ThreadID: atypes.NewID(), AgentAID: "local-reviewer", AgentBID: "remote-coder", Status: atypes.ThreadStatusActive, CreatedAt: now, UpdatedAt: now}
	if err := store.InsertThread(ctx, thread); err != nil {
		t.Fatalf("insert thread: %v", err)
	}
	turn := atypes.ThreadTurn{
		ThreadID:            thread.ThreadID,
		TaskID:              "origin-task-1",
		SenderAgentID:       "local-reviewer",
		TargetAgentID:       "remote-coder",
		TargetOwner:         atypes.TargetOwnerRemote,
		RemotePeerID:        "peer-b",
		DestinationNodeID:   "node-b",
		DestinationTaskID:   "destination-task-1",
		DestinationThreadID: "destination-thread-1",
		Status:              string(atypes.TaskStatusDispatching),
		CreatedAt:           now,
		UpdatedAt:           now,
	}
	if err := store.AppendThreadTurn(ctx, turn); err != nil {
		t.Fatalf("append remote turn: %v", err)
	}

	turns, err := store.ListThreadTurns(ctx, thread.ThreadID)
	if err != nil {
		t.Fatalf("list turns: %v", err)
	}
	if len(turns) != 1 {
		t.Fatalf("expected one turn, got %d", len(turns))
	}
	loaded := turns[0]
	if loaded.TargetOwner != atypes.TargetOwnerRemote || loaded.RemotePeerID != "peer-b" || loaded.DestinationNodeID != "node-b" || loaded.DestinationTaskID != "destination-task-1" || loaded.DestinationThreadID != "destination-thread-1" {
		t.Fatalf("remote turn binding did not persist: %+v", loaded)
	}
}

func TestSQLiteStoreAssignsNextThreadTurnIndexInsideMutationTransaction(t *testing.T) {
	tmp := t.TempDir()
	store, err := Open(filepath.Join(tmp, "aethrolink.db"), filepath.Join(tmp, "artifacts"), "http://127.0.0.1:7777")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	now := time.Now().UTC()
	thread := atypes.ThreadRecord{
		ThreadID:      atypes.NewID(),
		AgentAID:      "core",
		AgentBID:      "openclaw_main",
		Status:        atypes.ThreadStatusActive,
		ContinuityKey: "thread:atomic-index",
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := store.InsertThread(ctx, thread); err != nil {
		t.Fatalf("insert thread: %v", err)
	}
	firstTurn := atypes.ThreadTurn{
		ThreadID:      thread.ThreadID,
		TaskID:        "task-1",
		SenderAgentID: "core",
		TargetAgentID: "openclaw_main",
		Status:        string(atypes.TaskStatusCreated),
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	secondTurn := atypes.ThreadTurn{
		ThreadID:      thread.ThreadID,
		TaskID:        "task-2",
		SenderAgentID: "openclaw_main",
		TargetAgentID: "core",
		Status:        string(atypes.TaskStatusCreated),
		CreatedAt:     now.Add(1 * time.Second),
		UpdatedAt:     now.Add(1 * time.Second),
	}
	if err := store.AppendThreadTurnAndUpdateThread(ctx, thread.ThreadID, firstTurn, "task-1", "core", "openclaw_main", now); err != nil {
		t.Fatalf("append first thread turn transactionally: %v", err)
	}
	if err := store.AppendThreadTurnAndUpdateThread(ctx, thread.ThreadID, secondTurn, "task-2", "openclaw_main", "core", now.Add(1*time.Second)); err != nil {
		t.Fatalf("append second thread turn transactionally: %v", err)
	}
	turns, err := store.ListThreadTurns(ctx, thread.ThreadID)
	if err != nil {
		t.Fatalf("list thread turns: %v", err)
	}
	if len(turns) != 2 {
		t.Fatalf("expected 2 transactional turns, got %d", len(turns))
	}
	if turns[0].TurnIndex != 1 || turns[1].TurnIndex != 2 {
		t.Fatalf("expected transactional turn indices 1 then 2, got %#v", turns)
	}
}

func TestSQLiteStoreMigrateAddsThreadColumnBeforeCreatingTaskThreadIndex(t *testing.T) {
	tmp := t.TempDir()
	databasePath := filepath.Join(tmp, "legacy.db")
	artifactPath := filepath.Join(tmp, "artifacts")
	legacyStore, err := Open(databasePath, artifactPath, "http://127.0.0.1:7777")
	if err != nil {
		t.Fatalf("open store for legacy bootstrap: %v", err)
	}
	if _, err := legacyStore.db.Exec(`DROP INDEX IF EXISTS idx_tasks_thread`); err != nil {
		t.Fatalf("drop modern task-thread index: %v", err)
	}
	if _, err := legacyStore.db.Exec(`CREATE TABLE tasks_legacy (task_id TEXT PRIMARY KEY, conversation_id TEXT NOT NULL, sender TEXT NOT NULL, intent TEXT NOT NULL, requested_agent_id TEXT, resolved_agent_id TEXT, runtime_options_json TEXT NOT NULL, payload_artifact_id TEXT, status TEXT NOT NULL, remote_binding TEXT, remote_execution_id TEXT, remote_session_id TEXT, last_error_json TEXT, result_artifact_id TEXT, created_at TEXT NOT NULL, updated_at TEXT NOT NULL)`); err != nil {
		t.Fatalf("create legacy tasks shadow table: %v", err)
	}
	if _, err := legacyStore.db.Exec(`INSERT INTO tasks_legacy(task_id, conversation_id, sender, intent, requested_agent_id, resolved_agent_id, runtime_options_json, payload_artifact_id, status, remote_binding, remote_execution_id, remote_session_id, last_error_json, result_artifact_id, created_at, updated_at) SELECT task_id, conversation_id, sender, intent, requested_agent_id, resolved_agent_id, runtime_options_json, payload_artifact_id, status, remote_binding, remote_execution_id, remote_session_id, last_error_json, result_artifact_id, created_at, updated_at FROM tasks`); err != nil {
		t.Fatalf("copy tasks into legacy shape: %v", err)
	}
	if _, err := legacyStore.db.Exec(`DROP TABLE tasks`); err != nil {
		t.Fatalf("drop modern tasks table: %v", err)
	}
	if _, err := legacyStore.db.Exec(`ALTER TABLE tasks_legacy RENAME TO tasks`); err != nil {
		t.Fatalf("rename legacy tasks table: %v", err)
	}
	if err := legacyStore.Close(); err != nil {
		t.Fatalf("close legacy store: %v", err)
	}

	upgradedStore, err := Open(databasePath, artifactPath, "http://127.0.0.1:7777")
	if err != nil {
		t.Fatalf("reopen legacy store with migration: %v", err)
	}
	defer upgradedStore.Close()

	var threadColumnCount int
	if err := upgradedStore.db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('tasks') WHERE name = 'thread_id'`).Scan(&threadColumnCount); err != nil {
		t.Fatalf("inspect migrated tasks columns: %v", err)
	}
	if threadColumnCount != 1 {
		t.Fatalf("expected migrated tasks table to contain thread_id once, got %d", threadColumnCount)
	}
	var threadIndexCount int
	if err := upgradedStore.db.QueryRow(`SELECT COUNT(*) FROM pragma_index_list('tasks') WHERE name = 'idx_tasks_thread'`).Scan(&threadIndexCount); err != nil {
		t.Fatalf("inspect migrated task-thread indexes: %v", err)
	}
	if threadIndexCount != 1 {
		t.Fatalf("expected migrated task-thread index once, got %d", threadIndexCount)
	}
}

func TestSQLiteStoreMarksInterruptedThreadsOnRestart(t *testing.T) {
	tmp := t.TempDir()
	store, err := Open(filepath.Join(tmp, "aethrolink.db"), filepath.Join(tmp, "artifacts"), "http://127.0.0.1:7777")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	now := time.Now().UTC()
	thread := atypes.ThreadRecord{
		ThreadID:          atypes.NewID(),
		AgentAID:          "core",
		AgentBID:          "openclaw_main",
		Status:            atypes.ThreadStatusActive,
		ContinuityKey:     "thread:restart-reconcile",
		LastTaskID:        "task-awaiting",
		LastActorAgentID:  "core",
		LastTargetAgentID: "openclaw_main",
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	if err := store.InsertThread(ctx, thread); err != nil {
		t.Fatalf("insert thread: %v", err)
	}
	if err := store.InsertTask(ctx, atypes.TaskRecord{
		TaskID:          "task-awaiting",
		ThreadID:        thread.ThreadID,
		ConversationID:  thread.ThreadID,
		Sender:          "core",
		Intent:          "ui.review",
		ResolvedAgentID: "openclaw_main",
		RuntimeOptions:  map[string]any{"session_key": "main"},
		Status:          atypes.TaskStatusAwaitingInput,
		CreatedAt:       now,
		UpdatedAt:       now,
	}); err != nil {
		t.Fatalf("insert awaiting-input task: %v", err)
	}
	if err := store.MarkInterruptedThreadsOnRestart(ctx); err != nil {
		t.Fatalf("mark interrupted threads on restart: %v", err)
	}
	loaded, err := store.GetThread(ctx, thread.ThreadID)
	if err != nil {
		t.Fatalf("get thread: %v", err)
	}
	if loaded.Status != atypes.ThreadStatusInterrupted {
		t.Fatalf("expected interrupted thread status, got %q", loaded.Status)
	}
}
