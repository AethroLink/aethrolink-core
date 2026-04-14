package mockadapters

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/aethrolink/aethrolink-core/internal/adapters"
	"github.com/aethrolink/aethrolink-core/internal/config"
	"github.com/aethrolink/aethrolink-core/internal/drivers"
	"github.com/aethrolink/aethrolink-core/internal/runtime"
	atypes "github.com/aethrolink/aethrolink-core/pkg/types"
)

// RegisterAll wires the fake adapters used by tests into a registry without
// exposing them through the production node composition root.
func RegisterAll(reg *adapters.Registry, registry *config.RegistryDiscovery, runtimeManager *runtime.Manager) {
	reg.Register("mock_hermes", NewHermes(registry, runtimeManager))
	reg.Register("mock_openclaw", NewOpenClaw(registry, runtimeManager))
	reg.Register("mock_acp_comm_http", NewACPHTTP(registry, runtimeManager))
}

type Hermes struct {
	registry *config.RegistryDiscovery
	runtime  *runtime.Manager
}

func NewHermes(registry *config.RegistryDiscovery, runtimeManager *runtime.Manager) *Hermes {
	return &Hermes{registry: registry, runtime: runtimeManager}
}

func (a *Hermes) AdapterName() string { return "mock_hermes" }
func (a *Hermes) Capabilities(context.Context) (map[string]any, error) {
	return map[string]any{"adapter": "mock_hermes", "mode": "testsupport"}, nil
}

func (a *Hermes) EnsureReady(ctx context.Context, runtimeID string, options map[string]any) (atypes.RuntimeLease, error) {
	spec, err := a.registry.ResolveRuntime(ctx, runtimeID)
	if err != nil {
		return atypes.RuntimeLease{}, err
	}
	executor := stringOption(options, "executor", "coder")
	command := spec.Launch.Commands[executor]
	lease, _, err := a.runtime.EnsureStdioWorker(ctx, runtimeID, "executor:"+executor, command)
	return lease, err
}

func (a *Hermes) Submit(ctx context.Context, task atypes.TaskEnvelope, lease atypes.RuntimeLease) (atypes.RemoteHandle, error) {
	executor := lease.SubcontextKey[len("executor:"):]
	worker := a.runtime.GetStdioWorker(task.TargetRuntime, lease.SubcontextKey)
	if worker == nil {
		return atypes.RemoteHandle{}, fmt.Errorf("missing stdio worker")
	}
	driver := drivers.NewACPClientDriver(worker)
	if err := driver.Initialize(); err != nil {
		return atypes.RemoteHandle{}, err
	}
	sessionID, err := driver.SessionNew("")
	if err != nil {
		return atypes.RemoteHandle{}, err
	}
	result, err := driver.SessionPrompt(sessionID, map[string]any{
		"task_id":         task.TaskID,
		"intent":          task.Intent,
		"payload":         task.Payload,
		"runtime_options": task.RuntimeOptions,
		"metadata":        task.Metadata,
		"executor":        executor,
	})
	if err != nil {
		return atypes.RemoteHandle{}, err
	}
	remoteExec, _ := result["remote_execution_id"].(string)
	return atypes.RemoteHandle{
		TaskID:            task.TaskID,
		RuntimeID:         task.TargetRuntime,
		Binding:           "acp_client_stdio",
		RemoteExecutionID: remoteExec,
		RemoteSessionID:   sessionID,
		AdapterState:      map[string]any{"executor": executor, "session_id": sessionID},
	}, nil
}

func (a *Hermes) StreamEvents(ctx context.Context, handle atypes.RemoteHandle) (<-chan atypes.TaskEvent, <-chan error) {
	worker := a.runtime.GetStdioWorker(handle.RuntimeID, "executor:"+stringOption(handle.AdapterState, "executor", "coder"))
	out := make(chan atypes.TaskEvent, 64)
	errCh := make(chan error, 1)
	if worker == nil {
		close(out)
		errCh <- fmt.Errorf("missing stdio worker")
		close(errCh)
		return out, errCh
	}
	driver := drivers.NewACPClientDriver(worker)
	rawCh, cancel := driver.EventStream(handle.RemoteSessionID)
	go streamACPEvents(ctx, handle.TaskID, rawCh, cancel, out, errCh)
	return out, errCh
}

func (a *Hermes) Resume(ctx context.Context, handle atypes.RemoteHandle, payload map[string]any) error {
	worker := a.runtime.GetStdioWorker(handle.RuntimeID, "executor:"+stringOption(handle.AdapterState, "executor", "coder"))
	if worker == nil {
		return fmt.Errorf("missing stdio worker")
	}
	return drivers.NewACPClientDriver(worker).SessionResume(handle.RemoteSessionID, payload)
}

func (a *Hermes) Cancel(ctx context.Context, handle atypes.RemoteHandle) error {
	worker := a.runtime.GetStdioWorker(handle.RuntimeID, "executor:"+stringOption(handle.AdapterState, "executor", "coder"))
	if worker == nil {
		return fmt.Errorf("missing stdio worker")
	}
	return drivers.NewACPClientDriver(worker).SessionCancel(handle.RemoteSessionID)
}

func (a *Hermes) Health(ctx context.Context, runtimeID string, options map[string]any) (map[string]any, error) {
	return a.runtime.Health(runtimeID, a.SubcontextKey(atypes.RuntimeSpec{}, options)), nil
}

func (a *Hermes) RehydrateHandle(task atypes.TaskRecord, _ atypes.RuntimeSpec) (atypes.RemoteHandle, error) {
	handle := atypes.RemoteHandle{TaskID: task.TaskID, RuntimeID: task.ResolvedRuntime}
	if task.Remote != nil {
		handle.Binding = task.Remote.Binding
		handle.RemoteExecutionID = task.Remote.RemoteExecutionID
		handle.RemoteSessionID = task.Remote.RemoteSessionID
	}
	handle.AdapterState = map[string]any{"executor": stringOption(task.RuntimeOptions, "executor", "coder"), "session_id": handle.RemoteSessionID}
	return handle, nil
}

func (a *Hermes) SubcontextKey(_ atypes.RuntimeSpec, runtimeOptions map[string]any) string {
	return "executor:" + stringOption(runtimeOptions, "executor", "coder")
}

type OpenClaw struct {
	registry *config.RegistryDiscovery
	runtime  *runtime.Manager
	mu       sync.Mutex
	sessions map[string]string
}

func NewOpenClaw(registry *config.RegistryDiscovery, runtimeManager *runtime.Manager) *OpenClaw {
	return &OpenClaw{registry: registry, runtime: runtimeManager, sessions: map[string]string{}}
}

func (a *OpenClaw) AdapterName() string { return "mock_openclaw" }
func (a *OpenClaw) Capabilities(context.Context) (map[string]any, error) {
	return map[string]any{"adapter": "mock_openclaw", "mode": "testsupport"}, nil
}

func (a *OpenClaw) EnsureReady(ctx context.Context, runtimeID string, options map[string]any) (atypes.RuntimeLease, error) {
	spec, err := a.registry.ResolveRuntime(ctx, runtimeID)
	if err != nil {
		return atypes.RuntimeLease{}, err
	}
	sessionKey := stringOption(options, "session_key", "main")
	lease, _, err := a.runtime.EnsureStdioWorker(ctx, runtimeID, "session:"+sessionKey, spec.Launch.Command)
	return lease, err
}

func (a *OpenClaw) Submit(ctx context.Context, task atypes.TaskEnvelope, lease atypes.RuntimeLease) (atypes.RemoteHandle, error) {
	sessionKey := lease.SubcontextKey[len("session:"):]
	worker := a.runtime.GetStdioWorker(task.TargetRuntime, lease.SubcontextKey)
	if worker == nil {
		return atypes.RemoteHandle{}, fmt.Errorf("missing stdio worker")
	}
	driver := drivers.NewACPClientDriver(worker)
	if err := driver.Initialize(); err != nil {
		return atypes.RemoteHandle{}, err
	}
	a.mu.Lock()
	sessionID := a.sessions[sessionKey]
	a.mu.Unlock()
	var err error
	if sessionID != "" {
		sessionID, err = driver.SessionLoad(sessionID)
	} else {
		sessionID, err = driver.SessionNew(sessionKey)
	}
	if err != nil {
		return atypes.RemoteHandle{}, err
	}
	a.mu.Lock()
	a.sessions[sessionKey] = sessionID
	a.mu.Unlock()
	result, err := driver.SessionPrompt(sessionID, map[string]any{
		"task_id":         task.TaskID,
		"intent":          task.Intent,
		"payload":         task.Payload,
		"runtime_options": task.RuntimeOptions,
		"session_key":     sessionKey,
	})
	if err != nil {
		return atypes.RemoteHandle{}, err
	}
	remoteExec, _ := result["remote_execution_id"].(string)
	return atypes.RemoteHandle{
		TaskID:            task.TaskID,
		RuntimeID:         task.TargetRuntime,
		Binding:           "acp_client_stdio",
		RemoteExecutionID: remoteExec,
		RemoteSessionID:   sessionID,
		AdapterState:      map[string]any{"session_key": sessionKey, "session_id": sessionID},
	}, nil
}

func (a *OpenClaw) StreamEvents(ctx context.Context, handle atypes.RemoteHandle) (<-chan atypes.TaskEvent, <-chan error) {
	worker := a.runtime.GetStdioWorker(handle.RuntimeID, "session:"+stringOption(handle.AdapterState, "session_key", "main"))
	out := make(chan atypes.TaskEvent, 64)
	errCh := make(chan error, 1)
	if worker == nil {
		close(out)
		errCh <- fmt.Errorf("missing stdio worker")
		close(errCh)
		return out, errCh
	}
	driver := drivers.NewACPClientDriver(worker)
	rawCh, cancel := driver.EventStream(handle.RemoteSessionID)
	go streamACPEvents(ctx, handle.TaskID, rawCh, cancel, out, errCh)
	return out, errCh
}

func (a *OpenClaw) Resume(ctx context.Context, handle atypes.RemoteHandle, payload map[string]any) error {
	worker := a.runtime.GetStdioWorker(handle.RuntimeID, "session:"+stringOption(handle.AdapterState, "session_key", "main"))
	if worker == nil {
		return fmt.Errorf("missing stdio worker")
	}
	return drivers.NewACPClientDriver(worker).SessionResume(handle.RemoteSessionID, payload)
}

func (a *OpenClaw) Cancel(ctx context.Context, handle atypes.RemoteHandle) error {
	worker := a.runtime.GetStdioWorker(handle.RuntimeID, "session:"+stringOption(handle.AdapterState, "session_key", "main"))
	if worker == nil {
		return fmt.Errorf("missing stdio worker")
	}
	return drivers.NewACPClientDriver(worker).SessionCancel(handle.RemoteSessionID)
}

func (a *OpenClaw) Health(ctx context.Context, runtimeID string, options map[string]any) (map[string]any, error) {
	return a.runtime.Health(runtimeID, a.SubcontextKey(atypes.RuntimeSpec{}, options)), nil
}

func (a *OpenClaw) RehydrateHandle(task atypes.TaskRecord, _ atypes.RuntimeSpec) (atypes.RemoteHandle, error) {
	handle := atypes.RemoteHandle{TaskID: task.TaskID, RuntimeID: task.ResolvedRuntime}
	if task.Remote != nil {
		handle.Binding = task.Remote.Binding
		handle.RemoteExecutionID = task.Remote.RemoteExecutionID
		handle.RemoteSessionID = task.Remote.RemoteSessionID
	}
	handle.AdapterState = map[string]any{"session_key": stringOption(task.RuntimeOptions, "session_key", "main"), "session_id": handle.RemoteSessionID}
	return handle, nil
}

func (a *OpenClaw) SubcontextKey(_ atypes.RuntimeSpec, runtimeOptions map[string]any) string {
	return "session:" + stringOption(runtimeOptions, "session_key", "main")
}

type ACPHTTP struct {
	registry *config.RegistryDiscovery
	runtime  *runtime.Manager
	driver   *drivers.HTTPACPDriver
}

func NewACPHTTP(registry *config.RegistryDiscovery, runtimeManager *runtime.Manager) *ACPHTTP {
	return &ACPHTTP{registry: registry, runtime: runtimeManager, driver: drivers.NewHTTPACPDriver()}
}

func (a *ACPHTTP) AdapterName() string { return "mock_acp_comm_http" }
func (a *ACPHTTP) Capabilities(context.Context) (map[string]any, error) {
	return map[string]any{"adapter": "mock_acp_comm_http", "mode": "testsupport"}, nil
}

func (a *ACPHTTP) EnsureReady(ctx context.Context, runtimeID string, _ map[string]any) (atypes.RuntimeLease, error) {
	spec, err := a.registry.ResolveRuntime(ctx, runtimeID)
	if err != nil {
		return atypes.RuntimeLease{}, err
	}
	return a.runtime.EnsureProcess(ctx, runtimeID, "", spec.Launch.Command, spec.Healthcheck)
}

func (a *ACPHTTP) Submit(ctx context.Context, task atypes.TaskEnvelope, _ atypes.RuntimeLease) (atypes.RemoteHandle, error) {
	spec, err := a.registry.ResolveRuntime(ctx, task.TargetRuntime)
	if err != nil {
		return atypes.RemoteHandle{}, err
	}
	headers, _ := task.RuntimeOptions["headers"].(map[string]any)
	payloadJSON, _ := json.Marshal(task.Payload)
	body := map[string]any{"messages": []map[string]any{{"role": "system", "content": "aethrolink_control"}, {"role": "user", "content": string(payloadJSON)}}}
	result, err := a.driver.CreateRun(spec.Endpoint, body, headers)
	if err != nil {
		return atypes.RemoteHandle{}, err
	}
	runID, _ := result["run_id"].(string)
	sessionID, _ := result["session_id"].(string)
	return atypes.RemoteHandle{
		TaskID:            task.TaskID,
		RuntimeID:         task.TargetRuntime,
		Binding:           "acp_comm_http",
		RemoteExecutionID: runID,
		RemoteSessionID:   sessionID,
		AdapterState:      map[string]any{"endpoint": spec.Endpoint},
	}, nil
}

func (a *ACPHTTP) StreamEvents(ctx context.Context, handle atypes.RemoteHandle) (<-chan atypes.TaskEvent, <-chan error) {
	endpoint := stringOption(handle.AdapterState, "endpoint", "")
	rawCh, errCh := a.driver.PollRun(endpoint, handle.RemoteExecutionID)
	out := make(chan atypes.TaskEvent, 16)
	wrappedErr := make(chan error, 1)
	go func() {
		defer close(out)
		defer close(wrappedErr)
		for {
			select {
			case <-ctx.Done():
				return
			case err, ok := <-errCh:
				if ok && err != nil {
					wrappedErr <- err
				}
				return
			case raw, ok := <-rawCh:
				if !ok {
					return
				}
				out <- mapHTTPRunToTaskEvent(handle.TaskID, raw)
			}
		}
	}()
	return out, wrappedErr
}

func (a *ACPHTTP) Resume(ctx context.Context, handle atypes.RemoteHandle, payload map[string]any) error {
	return a.driver.ResumeRun(stringOption(handle.AdapterState, "endpoint", ""), handle.RemoteExecutionID, payload)
}

func (a *ACPHTTP) Cancel(ctx context.Context, handle atypes.RemoteHandle) error {
	return a.driver.CancelRun(stringOption(handle.AdapterState, "endpoint", ""), handle.RemoteExecutionID)
}

func (a *ACPHTTP) Health(ctx context.Context, runtimeID string, options map[string]any) (map[string]any, error) {
	return a.runtime.Health(runtimeID, a.SubcontextKey(atypes.RuntimeSpec{}, options)), nil
}

func (a *ACPHTTP) RehydrateHandle(task atypes.TaskRecord, spec atypes.RuntimeSpec) (atypes.RemoteHandle, error) {
	handle := atypes.RemoteHandle{TaskID: task.TaskID, RuntimeID: task.ResolvedRuntime}
	if task.Remote != nil {
		handle.Binding = task.Remote.Binding
		handle.RemoteExecutionID = task.Remote.RemoteExecutionID
		handle.RemoteSessionID = task.Remote.RemoteSessionID
	}
	handle.AdapterState = map[string]any{"endpoint": spec.Endpoint}
	return handle, nil
}

func (a *ACPHTTP) SubcontextKey(_ atypes.RuntimeSpec, _ map[string]any) string { return "" }

func streamACPEvents(ctx context.Context, taskID string, rawCh <-chan map[string]any, cancel func(), out chan<- atypes.TaskEvent, errCh chan<- error) {
	defer cancel()
	defer close(out)
	defer close(errCh)
	for {
		select {
		case <-ctx.Done():
			return
		case raw, ok := <-rawCh:
			if !ok {
				return
			}
			event := mapACPEventToTaskEvent(taskID, raw)
			out <- event
			if event.State.IsTerminal() {
				return
			}
		}
	}
}

func mapACPEventToTaskEvent(taskID string, raw map[string]any) atypes.TaskEvent {
	kind, _ := raw["kind"].(string)
	message, _ := raw["message"].(string)
	data, _ := raw["data"].(map[string]any)
	status := atypes.TaskStatusRunning
	eventKind := atypes.TaskEventTaskRunning
	switch kind {
	case "task.awaiting_input":
		status = atypes.TaskStatusAwaitingInput
		eventKind = atypes.TaskEventTaskAwaitingInput
	case "task.completed":
		status = atypes.TaskStatusCompleted
		eventKind = atypes.TaskEventTaskCompleted
	case "task.failed":
		status = atypes.TaskStatusFailed
		eventKind = atypes.TaskEventTaskFailed
	case "task.cancelled":
		status = atypes.TaskStatusCancelled
		eventKind = atypes.TaskEventTaskCancelled
	}
	return atypes.TaskEvent{
		EventID:   atypes.NewID(),
		TaskID:    taskID,
		Kind:      eventKind,
		State:     status,
		Source:    atypes.EventSourceAdapter,
		Message:   message,
		Data:      cloneMap(data),
		CreatedAt: atypes.NowUTC(),
	}
}

func mapHTTPRunToTaskEvent(taskID string, raw map[string]any) atypes.TaskEvent {
	status, _ := raw["status"].(string)
	data := map[string]any{}
	if result, ok := raw["result"].(map[string]any); ok {
		data["result"] = result
	}
	if reason, ok := raw["reason"].(string); ok && reason != "" {
		data["reason"] = reason
	}
	kind := atypes.TaskEventTaskRunning
	state := atypes.TaskStatusRunning
	message := "Task running"
	switch status {
	case "awaiting_input":
		kind = atypes.TaskEventTaskAwaitingInput
		state = atypes.TaskStatusAwaitingInput
		message = "Runtime requires additional input"
	case "completed":
		kind = atypes.TaskEventTaskCompleted
		state = atypes.TaskStatusCompleted
		message = "Task completed"
	case "failed":
		kind = atypes.TaskEventTaskFailed
		state = atypes.TaskStatusFailed
		message = "Task failed"
	case "cancelled":
		kind = atypes.TaskEventTaskCancelled
		state = atypes.TaskStatusCancelled
		message = "Task cancelled"
	}
	return atypes.TaskEvent{
		EventID:   atypes.NewID(),
		TaskID:    taskID,
		Kind:      kind,
		State:     state,
		Source:    atypes.EventSourceAdapter,
		Message:   message,
		Data:      cloneMap(data),
		CreatedAt: atypes.NowUTC(),
	}
}

func cloneMap(in map[string]any) map[string]any {
	if in == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		if nested, ok := value.(map[string]any); ok {
			out[key] = cloneMap(nested)
			continue
		}
		out[key] = value
	}
	return out
}

func stringOption(m map[string]any, key, fallback string) string {
	if m == nil {
		return fallback
	}
	value, _ := m[key].(string)
	if value == "" {
		return fallback
	}
	return value
}
