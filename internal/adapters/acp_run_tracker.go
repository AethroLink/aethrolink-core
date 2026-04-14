package adapters

import atypes "github.com/aethrolink/aethrolink-core/pkg/types"

// acpRunTracker is the single source of truth for prompt/run state observed by
// the ACP adapter loop.
type acpRunTracker struct {
	finalText           string
	sawStructuredEvents bool
	sawTerminalEvent    bool
}

type acpNotificationTracker struct {
	run *acpRunTracker
}

func (t *acpNotificationTracker) AppendChunk(text string) {
	t.run.finalText += text
}

func (t *acpNotificationTracker) MarkStructuredEvent() {
	t.run.sawStructuredEvents = true
}

func (r *acpRunTracker) FinalText() string {
	return r.finalText
}

func (r *acpRunTracker) SetFinalText(finalText string) {
	r.finalText = finalText
}

func (r *acpRunTracker) Record(events []atypes.LocalRuntimeEvent) {
	for _, event := range events {
		if event.Kind == atypes.LocalRuntimeEventTerminal || event.State.IsTerminal() {
			r.sawTerminalEvent = true
		}
	}
}

// syntheticCompletionDecision centralizes when the ACP bridge is allowed to
// synthesize a completion event after a prompt RPC returns.
func syntheticCompletionDecision(dialect acpDialect, tracker *acpRunTracker) bool {
	if tracker.sawTerminalEvent {
		return false
	}
	return dialect.ShouldEmitSyntheticCompletion(tracker.finalText, tracker.sawStructuredEvents, tracker.sawTerminalEvent)
}
