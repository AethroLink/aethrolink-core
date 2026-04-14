package adapters

import "testing"

func TestSyntheticCompletionPolicySuppressesAfterStructuredTerminal(t *testing.T) {
	decision := syntheticCompletionDecision(openClawACPDialect{}, &acpRunTracker{
		finalText:           "done",
		sawStructuredEvents: true,
		sawTerminalEvent:    true,
	})
	if decision {
		t.Fatalf("expected structured terminal event to suppress synthetic completion")
	}
}
