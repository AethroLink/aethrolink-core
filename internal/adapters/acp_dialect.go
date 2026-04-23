package adapters

import (
	"fmt"

	atypes "github.com/aethrolink/aethrolink-core/pkg/types"
)

// acpDialect isolates runtime-specific ACP behavior behind a stable local-first
// dialect boundary while the ACP transport stays generic.
type acpDialect interface {
	atypes.LocalRuntimeDialect
	Command(spec atypes.RuntimeSpec, options map[string]any) ([]string, error)
	BindingName() string
	BindingMetadata(options map[string]any, binding atypes.WorkspaceBinding) map[string]any
	OpenSessionPayload(binding atypes.WorkspaceBinding, options map[string]any) map[string]any
	LoadSessionPayload(sessionID string, binding atypes.WorkspaceBinding, options map[string]any) map[string]any
	PromptText(task atypes.TaskEnvelope) string
	PromptPayload(sessionID, promptText string, binding atypes.WorkspaceBinding, options map[string]any) map[string]any
	ResumePayload(sessionID string, payload map[string]any) (map[string]any, error)
	AcceptedEvent(task atypes.TaskEnvelope, sessionID string) atypes.LocalRuntimeEvent
	CompletionEvent(task atypes.TaskEnvelope, finalText string) atypes.LocalRuntimeEvent
	AdapterState(task atypes.TaskEnvelope, sessionID string) map[string]any
	RehydrateState(task atypes.TaskRecord) map[string]any
	ShouldRecoverFinalText(finalText string, sawStructured bool) bool
	ShouldEmitSyntheticCompletion(finalText string, sawStructured bool, sawTerminal bool) bool
	HandleNotification(params map[string]any, tracker *acpNotificationTracker) ([]atypes.LocalRuntimeEvent, bool)
}

func defaultACPDialects() map[string]acpDialect {
	items := map[string]acpDialect{}
	// Keep the ACP transport generic while runtime-specific request/event quirks
	// stay behind small dialect implementations.
	for _, dialect := range []acpDialect{newHermesACPDialect(), newOpenClawACPDialect(), newGooseACPDialect()} {
		items[dialect.Name()] = dialect
	}
	return items
}

func (a *ACPAdapter) resolveACPDialect(spec atypes.RuntimeSpec) (acpDialect, error) {
	name := spec.Dialect
	if name == "" {
		name = spec.Adapter
	}
	dialect, ok := a.dialects[name]
	if !ok {
		return nil, fmt.Errorf("unsupported acp dialect for target %s: %s", spec.TargetID, name)
	}
	return dialect, nil
}

func (a *ACPAdapter) dialectFromHandle(handle atypes.RemoteHandle) (acpDialect, error) {
	name := asString(handle.AdapterState, "dialect")
	if name == "" {
		return nil, fmt.Errorf("missing dialect in adapter state")
	}
	dialect, ok := a.dialects[name]
	if !ok {
		return nil, fmt.Errorf("unsupported acp dialect in adapter state: %s", name)
	}
	return dialect, nil
}
