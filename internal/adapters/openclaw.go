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

type openClawRun struct {
	events chan atypes.TaskEvent
	errs   chan error
}

type OpenClawAdapter struct {
	registry *config.RegistryDiscovery
	runtime  *runtime.Manager
	sessions *adaptersupport.SessionCoordinator
	mu       sync.Mutex
	runs     map[string]*openClawRun
}

func NewOpenClawAdapter(registry *config.RegistryDiscovery, runtimeManager *runtime.Manager) *OpenClawAdapter {
	return &OpenClawAdapter{
		registry: registry,
		runtime:  runtimeManager,
		sessions: adaptersupport.NewSessionCoordinator(runtimeManager.Store()),
		runs:     map[string]*openClawRun{},
	}
}

func (a *OpenClawAdapter) AdapterName() string { return "openclaw" }

func (a *OpenClawAdapter) Capabilities(context.Context) (map[string]any, error) {
	return map[string]any{"adapter": "openclaw", "mode": "acp_bridge"}, nil
}

func (a *OpenClawAdapter) EnsureReady(ctx context.Context, runtimeID string, options map[string]any) (atypes.RuntimeLease, error) {
	spec, err := a.registry.ResolveRuntime(ctx, runtimeID)
	if err != nil {
		return atypes.RuntimeLease{}, err
	}
	returnLease, _, err := a.runtime.EnsureStdioWorker(ctx, runtimeID, a.SubcontextKey(spec, options), spec.Launch.Command)
	return returnLease, err
}

func (a *OpenClawAdapter) Submit(ctx context.Context, task atypes.TaskEnvelope, lease atypes.RuntimeLease) (atypes.RemoteHandle, error) {
	return a.submitOpenClawPrompt(ctx, task, lease)
}

func (a *OpenClawAdapter) submitOpenClawPrompt(ctx context.Context, task atypes.TaskEnvelope, lease atypes.RuntimeLease) (atypes.RemoteHandle, error) {
	sessionKey := a.stickyKey(task)
	scope := adaptersupport.SessionScope(task.TargetRuntime, lease.SubcontextKey, sessionKey)
	if err := a.sessions.TryAcquire(scope, task.TaskID); err != nil {
		return atypes.RemoteHandle{}, err
	}
	worker := a.runtime.GetStdioWorker(task.TargetRuntime, lease.SubcontextKey)
	if worker == nil {
		a.sessions.Release(scope, task.TaskID)
		return atypes.RemoteHandle{}, fmt.Errorf("missing stdio worker")
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
	binding, exists, err := a.sessions.LoadBinding(ctx, task.TargetRuntime, lease.SubcontextKey, sessionKey)
	if err != nil {
		a.sessions.Release(scope, task.TaskID)
		return atypes.RemoteHandle{}, err
	}
	cwd, _ := task.RuntimeOptions["cwd"].(string)
	if cwd == "" {
		cwd = "."
	}
	if exists && !adaptersupport.SessionBindingStale(binding, idleTimeout, now) {
		result, loadErr := worker.RequestWithTimeout("session/load", map[string]any{"sessionId": binding.RemoteSessionID, "cwd": cwd, "mcpServers": []any{}}, 30*time.Second)
		if loadErr == nil {
			sessionID, _ = result["sessionId"].(string)
			if sessionID == "" {
				sessionID = binding.RemoteSessionID
			}
		}
	}
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
			return atypes.RemoteHandle{}, fmt.Errorf("missing sessionId from openclaw")
		}
	}
	metadata := map[string]any{"session_key": sessionKey, "cwd": cwd}
	if err := a.sessions.PersistBinding(ctx, task.TargetRuntime, lease.SubcontextKey, sessionKey, a.AdapterName(), sessionID, metadata, now); err != nil {
		a.sessions.Release(scope, task.TaskID)
		return atypes.RemoteHandle{}, err
	}
	run := &openClawRun{events: make(chan atypes.TaskEvent, 64), errs: make(chan error, 4)}
	a.mu.Lock()
	a.runs[task.TaskID] = run
	a.mu.Unlock()
	promptText := extractPromptTextFromPayload(task)
	if promptText == "" && len(task.Payload) > 0 {
		promptText, _ = marshalPayloadJSON(task.Payload)
	}
	if promptText == "" {
		promptText = "Say exactly OPENCLAW OK"
	}
	sub, cancel := worker.Subscribe()
	go func() {
		defer cancel()
		defer close(run.events)
		defer close(run.errs)
		messageText := ""
		sawStructuredEvents := false
		var msgMu sync.Mutex
		lastActivity := time.Now()
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		promptDone := make(chan struct{})
		promptFinished := false
		drainUntil := time.Time{}
		run.events <- atypes.TaskEvent{EventID: atypes.NewID(), TaskID: task.TaskID, Kind: atypes.TaskEventTaskRunning, State: atypes.TaskStatusRunning, Source: atypes.EventSourceAdapter, Message: "OpenClaw accepted the task", Data: map[string]any{"session_key": sessionKey, "session_id": sessionID}, CreatedAt: atypes.NowUTC()}
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
			structured := sawStructuredEvents
			msgMu.Unlock()
			if structured && finalText == "" {
				finalText = a.recoverFinalText(context.Background(), task.TargetRuntime, sessionKey, sessionID, cwd)
				if finalText == "" {
					return
				}
			}
			a.sessions.Release(scope, task.TaskID)
			run.events <- atypes.TaskEvent{EventID: atypes.NewID(), TaskID: task.TaskID, Kind: atypes.TaskEventTaskCompleted, State: atypes.TaskStatusCompleted, Source: atypes.EventSourceAdapter, Message: "OpenClaw completed the task", Data: map[string]any{"result": map[string]any{"text": finalText}}, CreatedAt: atypes.NowUTC()}
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
					a.sessions.Release(scope, task.TaskID)
					run.errs <- fmt.Errorf("openclaw session idle timeout exceeded")
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
				_ = a.sessions.TouchActivity(context.Background(), task.TargetRuntime, lease.SubcontextKey, sessionKey, lastActivity.UTC())
				if update, ok := params["update"].(map[string]any); ok && update != nil {
					if updateType, _ := update["sessionUpdate"].(string); updateType == "agent_message_chunk" {
						if content, ok := update["content"].(map[string]any); ok {
							if text, ok := content["text"].(string); ok {
								msgMu.Lock()
								messageText += text
								msgMu.Unlock()
							}
						}
					}
					continue
				}
				if event, ok := params["event"].(map[string]any); ok && event != nil {
					msgMu.Lock()
					sawStructuredEvents = true
					msgMu.Unlock()
					mapped := mapACPEventToTaskEvent(task.TaskID, event)
					outEvent := mapped
					if mapped.State.IsTerminal() {
						a.sessions.Release(scope, task.TaskID)
					}
					run.events <- outEvent
					if mapped.State.IsTerminal() {
						return
					}
				}
			}
		}
	}()
	return atypes.RemoteHandle{TaskID: task.TaskID, RuntimeID: task.TargetRuntime, Binding: "acp_client_stdio", RemoteExecutionID: sessionID, RemoteSessionID: sessionID, AdapterState: map[string]any{"session_key": sessionKey, "sticky_key": sessionKey, "session_idle_timeout_ms": task.RuntimeOptions["session_idle_timeout_ms"]}}, nil
}

func (a *OpenClawAdapter) StreamEvents(ctx context.Context, handle atypes.RemoteHandle) (<-chan atypes.TaskEvent, <-chan error) {
	a.mu.Lock()
	run := a.runs[handle.TaskID]
	a.mu.Unlock()
	if run == nil {
		events := make(chan atypes.TaskEvent)
		errs := make(chan error, 1)
		close(events)
		errs <- fmt.Errorf("missing openclaw run")
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

func (a *OpenClawAdapter) Resume(ctx context.Context, handle atypes.RemoteHandle, payload map[string]any) error {
	return fmt.Errorf("openclaw adapter does not support resume in this real ACP bridge slice")
}

func (a *OpenClawAdapter) Cancel(ctx context.Context, handle atypes.RemoteHandle) error {
	worker := a.runtime.GetStdioWorker(handle.RuntimeID, "session:"+asString(handle.AdapterState, "session_key"))
	if worker == nil {
		return fmt.Errorf("missing stdio worker")
	}
	_, err := worker.Request("session/cancel", map[string]any{"sessionId": handle.RemoteSessionID})
	return err
}

func (a *OpenClawAdapter) Health(ctx context.Context, runtimeID string, options map[string]any) (map[string]any, error) {
	return a.runtime.Health(runtimeID, a.SubcontextKey(atypes.RuntimeSpec{}, options)), nil
}

func (a *OpenClawAdapter) RehydrateHandle(task atypes.TaskRecord, spec atypes.RuntimeSpec) (atypes.RemoteHandle, error) {
	handle := atypes.RemoteHandle{TaskID: task.TaskID, RuntimeID: task.ResolvedRuntime}
	if task.Remote != nil {
		handle.Binding = task.Remote.Binding
		handle.RemoteExecutionID = task.Remote.RemoteExecutionID
		handle.RemoteSessionID = task.Remote.RemoteSessionID
	}
	sessionKey := a.sessionKey(task.RuntimeOptions)
	handle.AdapterState = map[string]any{"session_key": sessionKey, "sticky_key": sessionKey}
	return handle, nil
}

func (a *OpenClawAdapter) SubcontextKey(spec atypes.RuntimeSpec, runtimeOptions map[string]any) string {
	return "session:" + a.sessionKey(runtimeOptions)
}

func (a *OpenClawAdapter) sessionKey(runtimeOptions map[string]any) string {
	sessionKey := asString(runtimeOptions, "session_key")
	if sessionKey == "" {
		sessionKey = "main"
	}
	return sessionKey
}

func (a *OpenClawAdapter) stickyKey(task atypes.TaskEnvelope) string {
	return a.sessionKey(task.RuntimeOptions)
}

// recoverFinalText asks the OpenClaw bridge to replay the session through
// session/load and extracts the last assistant message from the replay stream.
// If the bridge cannot replay the session, recovery is treated as unsupported.
func (a *OpenClawAdapter) recoverFinalText(ctx context.Context, runtimeID, sessionKey, sessionID, cwd string) string {
	if sessionKey == "" || sessionID == "" {
		return ""
	}
	if cwd == "" {
		cwd = "."
	}
	return adaptersupport.RecoverAssistantTextFromSessionLoad(
		ctx,
		a.runtime.GetStdioWorker(runtimeID, "session:"+sessionKey),
		sessionID,
		map[string]any{"sessionId": sessionID, "cwd": cwd, "mcpServers": []any{}},
	)
}
