package adapters

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/aethrolink/aethrolink-core/internal/adaptersupport"
	"github.com/aethrolink/aethrolink-core/internal/config"
	"github.com/aethrolink/aethrolink-core/internal/runtime"
	atypes "github.com/aethrolink/aethrolink-core/pkg/types"
)

type hermesRun struct {
	events chan atypes.TaskEvent
	errs   chan error
}

type HermesAdapter struct {
	registry *config.RegistryDiscovery
	runtime  *runtime.Manager
	sessions *adaptersupport.SessionCoordinator
	mu       sync.Mutex
	runs     map[string]*hermesRun
}

func NewHermesAdapter(registry *config.RegistryDiscovery, runtimeManager *runtime.Manager) *HermesAdapter {
	return &HermesAdapter{registry: registry, runtime: runtimeManager, sessions: adaptersupport.NewSessionCoordinator(runtimeManager.Store()), runs: map[string]*hermesRun{}}
}

func (a *HermesAdapter) AdapterName() string { return "hermes" }

func (a *HermesAdapter) Capabilities(context.Context) (map[string]any, error) {
	return map[string]any{"adapter": "hermes", "mode": "real_hermes_acp"}, nil
}

func (a *HermesAdapter) EnsureReady(ctx context.Context, runtimeID string, options map[string]any) (atypes.RuntimeLease, error) {
	spec, err := a.registry.ResolveRuntime(ctx, runtimeID)
	if err != nil {
		return atypes.RuntimeLease{}, err
	}
	profile := a.profile(options)
	command := spec.Launch.Commands[profile]
	if len(command) == 0 {
		return atypes.RuntimeLease{}, fmt.Errorf("missing hermes command for profile %s", profile)
	}
	returnLease, _, err := a.runtime.EnsureStdioWorker(ctx, runtimeID, "profile:"+profile, command)
	return returnLease, err
}

func (a *HermesAdapter) Submit(ctx context.Context, task atypes.TaskEnvelope, lease atypes.RuntimeLease) (atypes.RemoteHandle, error) {
	return a.submitHermesPrompt(ctx, task, lease)
}

func (a *HermesAdapter) submitHermesPrompt(ctx context.Context, task atypes.TaskEnvelope, lease atypes.RuntimeLease) (atypes.RemoteHandle, error) {
	profile := a.profile(task.RuntimeOptions)
	stickyKey := a.stickyKey(task)
	scope := adaptersupport.SessionScope(task.TargetRuntime, lease.SubcontextKey, stickyKey)
	if err := a.sessions.TryAcquire(scope, task.TaskID); err != nil {
		return atypes.RemoteHandle{}, err
	}
	worker := a.runtime.GetStdioWorker(task.TargetRuntime, lease.SubcontextKey)
	if worker == nil {
		a.sessions.Release(scope, task.TaskID)
		return atypes.RemoteHandle{}, fmt.Errorf("missing hermes worker")
	}
	if _, err := worker.RequestWithTimeout("initialize", map[string]any{"protocolVersion": 1}, 20*time.Second); err != nil {
		a.sessions.Release(scope, task.TaskID)
		return atypes.RemoteHandle{}, err
	}
	idleTimeout := adaptersupport.SessionIdleTimeout(task.RuntimeOptions)
	promptTimeout := idleTimeout
	if promptTimeout < time.Minute {
		promptTimeout = time.Minute
	}
	now := atypes.NowUTC()
	sessionID := ""
	binding, exists, err := a.sessions.LoadBinding(ctx, task.TargetRuntime, lease.SubcontextKey, stickyKey)
	if err != nil {
		a.sessions.Release(scope, task.TaskID)
		return atypes.RemoteHandle{}, err
	}
	if exists && !adaptersupport.SessionBindingStale(binding, idleTimeout, now) {
		result, loadErr := worker.RequestWithTimeout("session/load", map[string]any{"sessionId": binding.RemoteSessionID}, 30*time.Second)
		if loadErr == nil {
			sessionID, _ = result["sessionId"].(string)
			if sessionID == "" {
				sessionID = binding.RemoteSessionID
			}
		}
	}
	cwd, _ := task.RuntimeOptions["cwd"].(string)
	if cwd == "" {
		cwd = "."
	}
	if sessionID == "" {
		result, err := worker.RequestWithTimeout("session/new", map[string]any{"cwd": cwd, "mcpServers": []any{}}, 30*time.Second)
		if err != nil {
			a.sessions.Release(scope, task.TaskID)
			return atypes.RemoteHandle{}, err
		}
		sessionID, _ = result["sessionId"].(string)
		if sessionID == "" {
			a.sessions.Release(scope, task.TaskID)
			return atypes.RemoteHandle{}, fmt.Errorf("missing sessionId from hermes")
		}
	}
	if err := a.sessions.PersistBinding(ctx, task.TargetRuntime, lease.SubcontextKey, stickyKey, a.AdapterName(), sessionID, map[string]any{"profile": profile, "cwd": cwd}, now); err != nil {
		a.sessions.Release(scope, task.TaskID)
		return atypes.RemoteHandle{}, err
	}
	run := &hermesRun{events: make(chan atypes.TaskEvent, 64), errs: make(chan error, 4)}
	a.mu.Lock()
	a.runs[task.TaskID] = run
	a.mu.Unlock()
	promptText := extractPromptTextFromPayload(task)
	if promptText == "" {
		promptText = "Say exactly OK"
	}
	sub, cancel := worker.Subscribe()
	go func() {
		defer cancel()
		defer close(run.events)
		defer close(run.errs)
		defer a.sessions.Release(scope, task.TaskID)
		messageText := ""
		var msgMu sync.Mutex
		lastActivity := time.Now()
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		promptDone := make(chan struct{})
		promptFinished := false
		drainUntil := time.Time{}
		run.events <- atypes.TaskEvent{EventID: atypes.NewID(), TaskID: task.TaskID, Kind: atypes.TaskEventTaskRunning, State: atypes.TaskStatusRunning, Source: atypes.EventSourceAdapter, Message: "Hermes accepted the task", Data: map[string]any{"profile": profile, "session_id": sessionID}, CreatedAt: atypes.NowUTC()}
		go func() {
			defer close(promptDone)
			_, err := worker.RequestWithTimeout("session/prompt", map[string]any{"sessionId": sessionID, "prompt": []map[string]any{{"type": "text", "text": promptText}}}, promptTimeout)
			if err != nil {
				a.sessions.Release(scope, task.TaskID)
				run.errs <- err
				return
			}
			msgMu.Lock()
			finalText := strings.TrimSpace(messageText)
			msgMu.Unlock()
			if finalText == "" {
				finalText = a.recoverFinalText(context.Background(), task.TargetRuntime, profile, sessionID)
			}
			a.sessions.Release(scope, task.TaskID)
			run.events <- atypes.TaskEvent{EventID: atypes.NewID(), TaskID: task.TaskID, Kind: atypes.TaskEventTaskCompleted, State: atypes.TaskStatusCompleted, Source: atypes.EventSourceAdapter, Message: "Hermes completed the task", Data: map[string]any{"result": map[string]any{"text": finalText}}, CreatedAt: atypes.NowUTC()}
		}()
		for {
			select {
			case <-ctx.Done():
				return
			case <-promptDone:
				promptFinished = true
				drainUntil = time.Now().Add(500 * time.Millisecond)
				promptDone = nil
			case <-ticker.C:
				if promptFinished && !drainUntil.IsZero() && time.Now().After(drainUntil) {
					return
				}
				if idleTimeout > 0 && time.Since(lastActivity) > idleTimeout {
					run.errs <- fmt.Errorf("hermes session idle timeout exceeded")
					return
				}
			case msg, ok := <-sub:
				if !ok {
					return
				}
				params, _ := msg["params"].(map[string]any)
				if params == nil {
					continue
				}
				sid, _ := params["sessionId"].(string)
				if sid == "" {
					sid, _ = params["session_id"].(string)
				}
				if sid != sessionID {
					continue
				}
				lastActivity = time.Now()
				if promptFinished {
					drainUntil = time.Now().Add(250 * time.Millisecond)
				}
				_ = a.sessions.TouchActivity(context.Background(), task.TargetRuntime, lease.SubcontextKey, stickyKey, lastActivity.UTC())
				update, _ := params["update"].(map[string]any)
				if update == nil {
					continue
				}
				if updateType, _ := update["sessionUpdate"].(string); updateType == "agent_message_chunk" {
					if content, ok := update["content"].(map[string]any); ok {
						if text, ok := content["text"].(string); ok {
							msgMu.Lock()
							messageText += text
							msgMu.Unlock()
						}
					}
				}
			}
		}
	}()
	return atypes.RemoteHandle{TaskID: task.TaskID, RuntimeID: task.TargetRuntime, Binding: "hermes_acp", RemoteExecutionID: sessionID, RemoteSessionID: sessionID, AdapterState: map[string]any{"profile": profile, "session_id": sessionID, "sticky_key": stickyKey, "session_idle_timeout_ms": task.RuntimeOptions["session_idle_timeout_ms"]}}, nil
}

func (a *HermesAdapter) StreamEvents(ctx context.Context, handle atypes.RemoteHandle) (<-chan atypes.TaskEvent, <-chan error) {
	return a.streamHermesRunEvents(ctx, handle)
}

func (a *HermesAdapter) streamHermesRunEvents(ctx context.Context, handle atypes.RemoteHandle) (<-chan atypes.TaskEvent, <-chan error) {
	a.mu.Lock()
	run := a.runs[handle.TaskID]
	a.mu.Unlock()
	if run == nil {
		events := make(chan atypes.TaskEvent)
		errs := make(chan error, 1)
		close(events)
		errs <- fmt.Errorf("missing hermes run")
		close(errs)
		return events, errs
	}
	out := make(chan atypes.TaskEvent, 64)
	errOut := make(chan error, 4)
	go func() {
		defer close(out)
		defer close(errOut)
		for {
			select {
			case <-ctx.Done():
				return
			case event, ok := <-run.events:
				if !ok {
					return
				}
				out <- event
			case err, ok := <-run.errs:
				if ok && err != nil {
					errOut <- err
				}
				return
			}
		}
	}()
	return out, errOut
}

func (a *HermesAdapter) Resume(ctx context.Context, handle atypes.RemoteHandle, payload map[string]any) error {
	return fmt.Errorf("hermes adapter does not support resume in this thin real-runtime slice")
}

func (a *HermesAdapter) Cancel(ctx context.Context, handle atypes.RemoteHandle) error {
	worker := a.runtime.GetStdioWorker(handle.RuntimeID, "profile:"+asString(handle.AdapterState, "profile"))
	if worker == nil {
		return fmt.Errorf("missing hermes worker")
	}
	_, err := worker.Request("session/cancel", map[string]any{"sessionId": handle.RemoteSessionID})
	return err
}

func (a *HermesAdapter) Health(ctx context.Context, runtimeID string, options map[string]any) (map[string]any, error) {
	return a.runtime.Health(runtimeID, a.SubcontextKey(atypes.RuntimeSpec{}, options)), nil
}

func (a *HermesAdapter) RehydrateHandle(task atypes.TaskRecord, spec atypes.RuntimeSpec) (atypes.RemoteHandle, error) {
	handle := atypes.RemoteHandle{TaskID: task.TaskID, RuntimeID: task.ResolvedRuntime}
	if task.Remote != nil {
		handle.Binding = task.Remote.Binding
		handle.RemoteExecutionID = task.Remote.RemoteExecutionID
		handle.RemoteSessionID = task.Remote.RemoteSessionID
	}
	profile := a.profile(task.RuntimeOptions)
	handle.AdapterState = map[string]any{"profile": profile, "session_id": handle.RemoteSessionID, "sticky_key": a.stickyKey(atypes.TaskEnvelope{ConversationID: task.ConversationID, RuntimeOptions: task.RuntimeOptions, TaskID: task.TaskID})}
	return handle, nil
}

func (a *HermesAdapter) SubcontextKey(spec atypes.RuntimeSpec, runtimeOptions map[string]any) string {
	return "profile:" + a.profile(runtimeOptions)
}

func (a *HermesAdapter) profile(runtimeOptions map[string]any) string {
	profile := asString(runtimeOptions, "profile")
	if profile == "" {
		profile = "aethrolink-agent"
	}
	return profile
}

func (a *HermesAdapter) stickyKey(task atypes.TaskEnvelope) string {
	if key := asString(task.RuntimeOptions, "session_key"); key != "" {
		return key
	}
	if task.ConversationID != "" {
		return task.ConversationID
	}
	return task.TaskID
}

func extractPromptTextFromPayload(task atypes.TaskEnvelope) string {
	if text, ok := task.Payload["text"].(string); ok && text != "" {
		return text
	}
	if prompt, ok := task.Payload["prompt"].(string); ok && prompt != "" {
		return prompt
	}
	return ""
}

// recoverFinalText asks Hermes to replay the session through session/load and
// extracts the last assistant message from the replay stream. If Hermes cannot
// replay the session, recovery is treated as unsupported and returns empty.
func (a *HermesAdapter) recoverFinalText(ctx context.Context, runtimeID, profile, sessionID string) string {
	if profile == "" || sessionID == "" {
		return ""
	}
	return adaptersupport.RecoverAssistantTextFromSessionLoad(
		ctx,
		a.runtime.GetStdioWorker(runtimeID, "profile:"+profile),
		sessionID,
		map[string]any{"sessionId": sessionID},
	)
}
