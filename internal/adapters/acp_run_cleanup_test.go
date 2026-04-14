package adapters

import (
	"context"
	"testing"

	atypes "github.com/aethrolink/aethrolink-core/pkg/types"
)

func TestACPAdapterRemovesRunAfterTerminalStream(t *testing.T) {
	adapter := newHermesAdapterForRecoveryTest(t)
	lease, err := adapter.EnsureReady(context.Background(), "hermes_test", map[string]any{"executor": "mimoportal"})
	if err != nil {
		t.Fatalf("ensure ready: %v", err)
	}

	handle, err := adapter.Submit(context.Background(), atypes.TaskEnvelope{
		TaskID:         atypes.NewID(),
		ConversationID: atypes.NewID(),
		TargetRuntime:  "hermes_test",
		Intent:         "research.topic",
		Payload:        map[string]any{"text": "Say exactly CLEANUP"},
		RuntimeOptions: map[string]any{"executor": "mimoportal"},
		Delivery:       atypes.DefaultDeliveryPolicy(),
	}, lease)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}

	events, errs := adapter.StreamEvents(context.Background(), handle)
	terminalSeen := false
	for !terminalSeen {
		select {
		case event, ok := <-events:
			if !ok {
				t.Fatalf("expected terminal event before stream close")
			}
			if event.State.IsTerminal() {
				terminalSeen = true
			}
		case err, ok := <-errs:
			if ok && err != nil {
				t.Fatalf("unexpected stream error: %v", err)
			}
		}
	}

	for range events {
	}

	adapter.mu.Lock()
	got := len(adapter.runs)
	adapter.mu.Unlock()
	if got != 0 {
		t.Fatalf("expected completed run registry cleanup, got %d live runs", got)
	}
}
