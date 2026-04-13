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
		RuntimeOptions:  map[string]any{"profile": "coder"},
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
		SubcontextKey:   "profile:coder",
		StickyKey:       "conversation:abc",
		Adapter:         "hermes",
		RemoteSessionID: "sess-1",
		Metadata:        map[string]any{"profile": "coder"},
		CreatedAt:       time.Now().UTC(),
		UpdatedAt:       time.Now().UTC(),
		LastUsedAt:      time.Now().UTC(),
		LastActivityAt:  time.Now().UTC(),
	}
	if err := store.UpsertSessionBinding(ctx, binding); err != nil {
		t.Fatalf("upsert session binding: %v", err)
	}
	loaded, err := store.GetSessionBinding(ctx, "hermes", "profile:coder", "conversation:abc")
	if err != nil {
		t.Fatalf("get session binding: %v", err)
	}
	if loaded.RemoteSessionID != "sess-1" {
		t.Fatalf("expected sess-1, got %q", loaded.RemoteSessionID)
	}
	if err := store.TouchSessionBindingActivity(ctx, "hermes", "profile:coder", "conversation:abc", time.Now().UTC().Add(1*time.Minute)); err != nil {
		t.Fatalf("touch session binding activity: %v", err)
	}
}
