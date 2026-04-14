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
		ResolvedRuntime: "hermes",
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
		RuntimeID:       "hermes",
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
		ResolvedRuntime: "hermes",
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
		RuntimeKind:    "hermes",
		TransportKind:  "local_managed",
		RuntimeID:      "core",
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
