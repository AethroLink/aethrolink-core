package adapters

import (
	"context"
	"testing"

	atypes "github.com/aethrolink/aethrolink-core/pkg/types"
)

// TestACPDialectsPreferThreadIDForStickyContinuity keeps thread continuation
// above older conversation-level heuristics for Hermes and Goose.
func TestACPDialectsPreferThreadIDForStickyContinuity(t *testing.T) {
	tests := []struct {
		name       string
		dialectKey string
	}{
		{name: "hermes", dialectKey: "hermes"},
		{name: "goose", dialectKey: "goose"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dialect, ok := defaultACPDialects()[tt.dialectKey]
			if !ok {
				t.Fatalf("expected %s dialect", tt.dialectKey)
			}
			task := atypes.TaskEnvelope{
				ThreadID:       "thread-123",
				ConversationID: "conv-123",
				RuntimeOptions: map[string]any{"session_key": "manual-key"},
				TaskID:         "task-123",
			}
			if got := dialect.StickyKey(task); got != "thread-123" {
				t.Fatalf("expected thread sticky key, got %q", got)
			}
		})
	}
}

// TestOpenClawDialectMapsThreadIDOntoStableSessionKey keeps OpenClaw's native
// session_key control while allowing thread continuity to drive it when present.
func TestOpenClawDialectMapsThreadIDOntoStableSessionKey(t *testing.T) {
	dialect, ok := defaultACPDialects()["openclaw"]
	if !ok {
		t.Fatalf("expected openclaw dialect")
	}
	task := atypes.TaskEnvelope{
		ThreadID:       "thread-oc-123",
		ConversationID: "conv-oc-123",
		RuntimeOptions: map[string]any{},
		TaskID:         "task-oc-123",
	}
	if got := dialect.StickyKey(task); got != "thread-oc-123" {
		t.Fatalf("expected thread-driven session key, got %q", got)
	}
	state := dialect.AdapterState(task, "sess-1")
	if got, _ := state["session_key"].(string); got != "thread-oc-123" {
		t.Fatalf("expected thread-driven session key in adapter state, got %q", got)
	}
}

// TestACPAdapterReusesSameRemoteSessionAcrossThreadTurns proves thread_id, not
// conversation_id, is the continuity contract once a thread exists.
func TestACPAdapterReusesSameRemoteSessionAcrossThreadTurns(t *testing.T) {
	adapter := newHermesAdapterForRecoveryTest(t)
	lease, err := adapter.EnsureReady(context.Background(), "hermes_test", map[string]any{"executor": "mimoportal"})
	if err != nil {
		t.Fatalf("ensure ready: %v", err)
	}
	firstHandle, err := adapter.Submit(context.Background(), atypes.TaskEnvelope{
		TaskID:         atypes.NewID(),
		ThreadID:       "thread-keepalive",
		ConversationID: "conv-1",
		TargetAgentID:  "hermes_test",
		Intent:         "research.topic",
		Payload:        map[string]any{"text": "Say exactly FIRST"},
		RuntimeOptions: map[string]any{"executor": "mimoportal"},
		Delivery:       atypes.DefaultDeliveryPolicy(),
	}, lease)
	if err != nil {
		t.Fatalf("submit first thread turn: %v", err)
	}
	drainTerminalEvents(t, adapter, firstHandle)

	secondHandle, err := adapter.Submit(context.Background(), atypes.TaskEnvelope{
		TaskID:         atypes.NewID(),
		ThreadID:       "thread-keepalive",
		ConversationID: "conv-2",
		TargetAgentID:  "hermes_test",
		Intent:         "research.topic",
		Payload:        map[string]any{"text": "Say exactly SECOND"},
		RuntimeOptions: map[string]any{"executor": "mimoportal"},
		Delivery:       atypes.DefaultDeliveryPolicy(),
	}, lease)
	if err != nil {
		t.Fatalf("submit second thread turn: %v", err)
	}
	drainTerminalEvents(t, adapter, secondHandle)

	if firstHandle.RemoteSessionID == "" || secondHandle.RemoteSessionID == "" {
		t.Fatalf("expected persisted remote sessions, got %#v and %#v", firstHandle, secondHandle)
	}
	if firstHandle.RemoteSessionID != secondHandle.RemoteSessionID {
		t.Fatalf("expected same remote session for same thread, got %q then %q", firstHandle.RemoteSessionID, secondHandle.RemoteSessionID)
	}
}

func drainTerminalEvents(t *testing.T, adapter *ACPAdapter, handle atypes.RemoteHandle) {
	t.Helper()
	events, errs := adapter.StreamEvents(context.Background(), handle)
	for {
		select {
		case event, ok := <-events:
			if !ok {
				return
			}
			if event.State.IsTerminal() {
				for range events {
				}
				return
			}
		case err, ok := <-errs:
			if ok && err != nil {
				t.Fatalf("unexpected stream error: %v", err)
			}
			return
		}
	}
}
