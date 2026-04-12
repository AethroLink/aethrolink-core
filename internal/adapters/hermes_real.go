package adapters

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/aethrolink/aethrolink-core/internal/config"
	"github.com/aethrolink/aethrolink-core/internal/runtime"
	atypes "github.com/aethrolink/aethrolink-core/pkg/types"
)

type hermesRealRun struct {
	events chan atypes.TaskEvent
	errs   chan error
}

type HermesRealAdapter struct {
	registry *config.RegistryDiscovery
	runtime  *runtime.Manager
	mu       sync.Mutex
	runs     map[string]*hermesRealRun
}

func NewHermesRealAdapter(registry *config.RegistryDiscovery, runtimeManager *runtime.Manager) *HermesRealAdapter {
	return &HermesRealAdapter{registry: registry, runtime: runtimeManager, runs: map[string]*hermesRealRun{}}
}

func (a *HermesRealAdapter) AdapterName() string { return "hermes_real" }

func (a *HermesRealAdapter) Capabilities(context.Context) (map[string]any, error) {
	return map[string]any{"adapter": "hermes_real", "mode": "real_hermes_acp"}, nil
}

func (a *HermesRealAdapter) EnsureReady(ctx context.Context, runtimeID string, options map[string]any) (atypes.RuntimeLease, error) {
	spec, err := a.registry.ResolveRuntime(ctx, runtimeID)
	if err != nil {
		return atypes.RuntimeLease{}, err
	}
	profile, _ := options["profile"].(string)
	if profile == "" {
		profile = "aethrolink-agent"
	}
	command := spec.Launch.Commands[profile]
	if len(command) == 0 {
		return atypes.RuntimeLease{}, fmt.Errorf("missing real hermes command for profile %s", profile)
	}
	returnLease, _, err := a.runtime.EnsureStdioWorker(ctx, runtimeID, "profile:"+profile, command)
	return returnLease, err
}

func (a *HermesRealAdapter) Submit(ctx context.Context, task atypes.TaskEnvelope, lease atypes.RuntimeLease) (atypes.RemoteHandle, error) {
	profile := lease.SubcontextKey[len("profile:"):]
	worker := a.runtime.GetStdioWorker(task.TargetRuntime, lease.SubcontextKey)
	if worker == nil {
		return atypes.RemoteHandle{}, fmt.Errorf("missing real hermes worker")
	}
	if _, err := worker.RequestWithTimeout("initialize", map[string]any{"protocolVersion": 1}, 20*time.Second); err != nil {
		return atypes.RemoteHandle{}, err
	}
	cwd, _ := task.RuntimeOptions["cwd"].(string)
	if cwd == "" {
		cwd = "."
	}
	result, err := worker.RequestWithTimeout("session/new", map[string]any{"cwd": cwd, "mcpServers": []any{}}, 30*time.Second)
	if err != nil {
		return atypes.RemoteHandle{}, err
	}
	sessionID, _ := result["sessionId"].(string)
	if sessionID == "" {
		return atypes.RemoteHandle{}, fmt.Errorf("missing sessionId from real hermes")
	}
	run := &hermesRealRun{events: make(chan atypes.TaskEvent, 64), errs: make(chan error, 4)}
	a.mu.Lock()
	a.runs[task.TaskID] = run
	a.mu.Unlock()
	promptText := payloadText(task)
	if promptText == "" {
		promptText = "Say exactly OK"
	}
	sub, cancel := worker.Subscribe()
	go func() {
		defer cancel()
		defer close(run.events)
		defer close(run.errs)
		messageText := ""
		var msgMu sync.Mutex
		run.events <- atypes.TaskEvent{EventID: atypes.NewID(), TaskID: task.TaskID, Kind: atypes.TaskEventTaskRunning, State: atypes.TaskStatusRunning, Source: atypes.EventSourceAdapter, Message: "Real Hermes accepted the task", Data: map[string]any{"profile": profile, "session_id": sessionID}, CreatedAt: atypes.NowUTC()}
		go func() {
			_, err := worker.RequestWithTimeout("session/prompt", map[string]any{"sessionId": sessionID, "prompt": []map[string]any{{"type": "text", "text": promptText}}}, 60*time.Second)
			if err != nil {
				run.errs <- err
				return
			}
			msgMu.Lock()
			finalText := messageText
			msgMu.Unlock()
			run.events <- atypes.TaskEvent{EventID: atypes.NewID(), TaskID: task.TaskID, Kind: atypes.TaskEventTaskCompleted, State: atypes.TaskStatusCompleted, Source: atypes.EventSourceAdapter, Message: "Real Hermes completed the task", Data: map[string]any{"result": map[string]any{"text": finalText}}, CreatedAt: atypes.NowUTC()}
		}()
		idleDeadline := time.Now().Add(60 * time.Second)
		for {
			select {
			case <-ctx.Done():
				return
			case msg, ok := <-sub:
				if !ok {
					return
				}
				params, _ := msg["params"].(map[string]any)
				if params == nil {
					continue
				}
				sid, _ := params["sessionId"].(string)
				if sid != sessionID {
					continue
				}
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
				idleDeadline = time.Now().Add(60 * time.Second)
			case <-time.After(250 * time.Millisecond):
				if time.Now().After(idleDeadline) {
					return
				}
			}
		}
	}()
	return atypes.RemoteHandle{TaskID: task.TaskID, RuntimeID: task.TargetRuntime, Binding: "hermes_real_acp", RemoteExecutionID: sessionID, RemoteSessionID: sessionID, AdapterState: map[string]any{"profile": profile, "session_id": sessionID}}, nil
}

func (a *HermesRealAdapter) StreamEvents(ctx context.Context, handle atypes.RemoteHandle) (<-chan atypes.TaskEvent, <-chan error) {
	a.mu.Lock()
	run := a.runs[handle.TaskID]
	a.mu.Unlock()
	if run == nil {
		events := make(chan atypes.TaskEvent)
		errs := make(chan error, 1)
		close(events)
		errs <- fmt.Errorf("missing real hermes run")
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
			}
		}
	}()
	return out, errOut
}

func (a *HermesRealAdapter) Resume(ctx context.Context, handle atypes.RemoteHandle, payload map[string]any) error {
	return fmt.Errorf("real hermes adapter does not support resume in this thin validation slice")
}

func (a *HermesRealAdapter) Cancel(ctx context.Context, handle atypes.RemoteHandle) error {
	worker := a.runtime.GetStdioWorker(handle.RuntimeID, "profile:"+asString(handle.AdapterState, "profile"))
	if worker == nil {
		return fmt.Errorf("missing real hermes worker")
	}
	_, err := worker.Request("session/cancel", map[string]any{"sessionId": handle.RemoteSessionID})
	return err
}

func (a *HermesRealAdapter) Health(ctx context.Context, runtimeID string, options map[string]any) (map[string]any, error) {
	profile, _ := options["profile"].(string)
	if profile == "" {
		profile = "aethrolink-agent"
	}
	return a.runtime.Health(runtimeID, "profile:"+profile), nil
}

func payloadText(task atypes.TaskEnvelope) string {
	if text, ok := task.Payload["text"].(string); ok && text != "" {
		return text
	}
	if prompt, ok := task.Payload["prompt"].(string); ok && prompt != "" {
		return prompt
	}
	return ""
}
