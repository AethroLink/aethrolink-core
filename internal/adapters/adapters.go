package adapters

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/aethrolink/aethrolink-core/internal/config"
	"github.com/aethrolink/aethrolink-core/internal/drivers"
	"github.com/aethrolink/aethrolink-core/internal/runtime"
	atypes "github.com/aethrolink/aethrolink-core/pkg/types"
)

type Registry struct {
	mu    sync.RWMutex
	items map[string]atypes.RuntimeAdapter
}

func NewRegistry() *Registry {
	return &Registry{items: map[string]atypes.RuntimeAdapter{}}
}

func (r *Registry) Register(kind string, adapter atypes.RuntimeAdapter) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.items[kind] = adapter
}

func (r *Registry) Get(kind string) (atypes.RuntimeAdapter, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	adapter, ok := r.items[kind]
	return adapter, ok
}

func eventFromACP(taskID string, raw map[string]any) atypes.TaskEvent {
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
	default:
		status = atypes.TaskStatusRunning
		eventKind = atypes.TaskEventTaskRunning
	}
	return atypes.TaskEvent{EventID: atypes.NewID(), TaskID: taskID, Kind: eventKind, State: status, Source: atypes.EventSourceAdapter, Message: message, Data: data, CreatedAt: atypes.NowUTC()}
}

type HermesAdapter struct {
	registry *config.RegistryDiscovery
	runtime  *runtime.Manager
}

func NewHermesAdapter(registry *config.RegistryDiscovery, runtimeManager *runtime.Manager) *HermesAdapter {
	return &HermesAdapter{registry: registry, runtime: runtimeManager}
}

func (a *HermesAdapter) AdapterName() string { return "hermes" }
func (a *HermesAdapter) Capabilities(context.Context) (map[string]any, error) {
	return map[string]any{"adapter": "hermes"}, nil
}
func (a *HermesAdapter) EnsureReady(ctx context.Context, runtimeID string, options map[string]any) (atypes.RuntimeLease, error) {
	spec, err := a.registry.ResolveRuntime(ctx, runtimeID)
	if err != nil {
		return atypes.RuntimeLease{}, err
	}
	profile, _ := options["profile"].(string)
	if profile == "" {
		profile = "coder"
	}
	command := spec.Launch.Commands[profile]
	returnLease, _, err := a.runtime.EnsureStdioWorker(ctx, runtimeID, "profile:"+profile, command)
	return returnLease, err
}
func (a *HermesAdapter) Submit(ctx context.Context, task atypes.TaskEnvelope, lease atypes.RuntimeLease) (atypes.RemoteHandle, error) {
	profile := lease.SubcontextKey[len("profile:"):]
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
	result, err := driver.SessionPrompt(sessionID, map[string]any{"task_id": task.TaskID, "intent": task.Intent, "payload": task.Payload, "runtime_options": task.RuntimeOptions, "metadata": task.Metadata, "profile": profile})
	if err != nil {
		return atypes.RemoteHandle{}, err
	}
	remoteExec, _ := result["remote_execution_id"].(string)
	return atypes.RemoteHandle{TaskID: task.TaskID, RuntimeID: task.TargetRuntime, Binding: "acp_client_stdio", RemoteExecutionID: remoteExec, RemoteSessionID: sessionID, AdapterState: map[string]any{"profile": profile, "session_id": sessionID}}, nil
}
func (a *HermesAdapter) StreamEvents(ctx context.Context, handle atypes.RemoteHandle) (<-chan atypes.TaskEvent, <-chan error) {
	worker := a.runtime.GetStdioWorker(handle.RuntimeID, "profile:"+asString(handle.AdapterState, "profile"))
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
	go func() {
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
				event := eventFromACP(handle.TaskID, raw)
				out <- event
				if event.State.IsTerminal() {
					return
				}
			}
		}
	}()
	return out, errCh
}
func (a *HermesAdapter) Resume(ctx context.Context, handle atypes.RemoteHandle, payload map[string]any) error {
	worker := a.runtime.GetStdioWorker(handle.RuntimeID, "profile:"+asString(handle.AdapterState, "profile"))
	if worker == nil {
		return fmt.Errorf("missing stdio worker")
	}
	return drivers.NewACPClientDriver(worker).SessionResume(handle.RemoteSessionID, payload)
}
func (a *HermesAdapter) Cancel(ctx context.Context, handle atypes.RemoteHandle) error {
	worker := a.runtime.GetStdioWorker(handle.RuntimeID, "profile:"+asString(handle.AdapterState, "profile"))
	if worker == nil {
		return fmt.Errorf("missing stdio worker")
	}
	return drivers.NewACPClientDriver(worker).SessionCancel(handle.RemoteSessionID)
}
func (a *HermesAdapter) Health(ctx context.Context, runtimeID string, options map[string]any) (map[string]any, error) {
	profile, _ := options["profile"].(string)
	if profile == "" {
		profile = "coder"
	}
	return a.runtime.Health(runtimeID, "profile:"+profile), nil
}

type OpenClawAdapter struct {
	registry *config.RegistryDiscovery
	runtime  *runtime.Manager
	mu       sync.Mutex
	sessions map[string]string
}

func NewOpenClawAdapter(registry *config.RegistryDiscovery, runtimeManager *runtime.Manager) *OpenClawAdapter {
	return &OpenClawAdapter{registry: registry, runtime: runtimeManager, sessions: map[string]string{}}
}

func (a *OpenClawAdapter) AdapterName() string { return "openclaw" }
func (a *OpenClawAdapter) Capabilities(context.Context) (map[string]any, error) {
	return map[string]any{"adapter": "openclaw"}, nil
}
func (a *OpenClawAdapter) EnsureReady(ctx context.Context, runtimeID string, options map[string]any) (atypes.RuntimeLease, error) {
	spec, err := a.registry.ResolveRuntime(ctx, runtimeID)
	if err != nil {
		return atypes.RuntimeLease{}, err
	}
	sessionKey, _ := options["session_key"].(string)
	if sessionKey == "" {
		sessionKey = "main"
	}
	returnLease, _, err := a.runtime.EnsureStdioWorker(ctx, runtimeID, "session:"+sessionKey, spec.Launch.Command)
	return returnLease, err
}
func (a *OpenClawAdapter) Submit(ctx context.Context, task atypes.TaskEnvelope, lease atypes.RuntimeLease) (atypes.RemoteHandle, error) {
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
	existing := a.sessions[sessionKey]
	a.mu.Unlock()
	sessionID := existing
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
	result, err := driver.SessionPrompt(sessionID, map[string]any{"task_id": task.TaskID, "intent": task.Intent, "payload": task.Payload, "runtime_options": task.RuntimeOptions, "session_key": sessionKey})
	if err != nil {
		return atypes.RemoteHandle{}, err
	}
	remoteExec, _ := result["remote_execution_id"].(string)
	return atypes.RemoteHandle{TaskID: task.TaskID, RuntimeID: task.TargetRuntime, Binding: "acp_client_stdio", RemoteExecutionID: remoteExec, RemoteSessionID: sessionID, AdapterState: map[string]any{"session_key": sessionKey, "session_id": sessionID}}, nil
}
func (a *OpenClawAdapter) StreamEvents(ctx context.Context, handle atypes.RemoteHandle) (<-chan atypes.TaskEvent, <-chan error) {
	worker := a.runtime.GetStdioWorker(handle.RuntimeID, "session:"+asString(handle.AdapterState, "session_key"))
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
	go func() {
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
				event := eventFromACP(handle.TaskID, raw)
				out <- event
				if event.State.IsTerminal() {
					return
				}
			}
		}
	}()
	return out, errCh
}
func (a *OpenClawAdapter) Resume(ctx context.Context, handle atypes.RemoteHandle, payload map[string]any) error {
	worker := a.runtime.GetStdioWorker(handle.RuntimeID, "session:"+asString(handle.AdapterState, "session_key"))
	if worker == nil {
		return fmt.Errorf("missing stdio worker")
	}
	return drivers.NewACPClientDriver(worker).SessionResume(handle.RemoteSessionID, payload)
}
func (a *OpenClawAdapter) Cancel(ctx context.Context, handle atypes.RemoteHandle) error {
	worker := a.runtime.GetStdioWorker(handle.RuntimeID, "session:"+asString(handle.AdapterState, "session_key"))
	if worker == nil {
		return fmt.Errorf("missing stdio worker")
	}
	return drivers.NewACPClientDriver(worker).SessionCancel(handle.RemoteSessionID)
}
func (a *OpenClawAdapter) Health(ctx context.Context, runtimeID string, options map[string]any) (map[string]any, error) {
	sessionKey, _ := options["session_key"].(string)
	if sessionKey == "" {
		sessionKey = "main"
	}
	return a.runtime.Health(runtimeID, "session:"+sessionKey), nil
}

type ACPHTTPAdapter struct {
	registry *config.RegistryDiscovery
	runtime  *runtime.Manager
	driver   *drivers.HTTPACPDriver
}

func NewACPHTTPAdapter(registry *config.RegistryDiscovery, runtimeManager *runtime.Manager) *ACPHTTPAdapter {
	return &ACPHTTPAdapter{registry: registry, runtime: runtimeManager, driver: drivers.NewHTTPACPDriver()}
}

func (a *ACPHTTPAdapter) AdapterName() string { return "acp_comm_http" }
func (a *ACPHTTPAdapter) Capabilities(context.Context) (map[string]any, error) {
	return map[string]any{"adapter": "acp_comm_http"}, nil
}
func (a *ACPHTTPAdapter) EnsureReady(ctx context.Context, runtimeID string, options map[string]any) (atypes.RuntimeLease, error) {
	spec, err := a.registry.ResolveRuntime(ctx, runtimeID)
	if err != nil {
		return atypes.RuntimeLease{}, err
	}
	return a.runtime.EnsureProcess(ctx, runtimeID, "", spec.Launch.Command, spec.Healthcheck)
}
func (a *ACPHTTPAdapter) Submit(ctx context.Context, task atypes.TaskEnvelope, lease atypes.RuntimeLease) (atypes.RemoteHandle, error) {
	spec, err := a.registry.ResolveRuntime(ctx, task.TargetRuntime)
	if err != nil {
		return atypes.RemoteHandle{}, err
	}
	headers, _ := task.RuntimeOptions["headers"].(map[string]any)
	payloadJSON, _ := jsonMarshal(task.Payload)
	body := map[string]any{"messages": []map[string]any{{"role": "system", "content": "aethrolink_control"}, {"role": "user", "content": payloadJSON}}}
	result, err := a.driver.CreateRun(spec.Endpoint, body, headers)
	if err != nil {
		return atypes.RemoteHandle{}, err
	}
	runID, _ := result["run_id"].(string)
	sessionID, _ := result["session_id"].(string)
	return atypes.RemoteHandle{TaskID: task.TaskID, RuntimeID: task.TargetRuntime, Binding: "acp_comm_http", RemoteExecutionID: runID, RemoteSessionID: sessionID, AdapterState: map[string]any{"endpoint": spec.Endpoint}}, nil
}
func (a *ACPHTTPAdapter) StreamEvents(ctx context.Context, handle atypes.RemoteHandle) (<-chan atypes.TaskEvent, <-chan error) {
	endpoint := asString(handle.AdapterState, "endpoint")
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
				out <- eventFromHTTP(handle.TaskID, raw)
			}
		}
	}()
	return out, wrappedErr
}
func (a *ACPHTTPAdapter) Resume(ctx context.Context, handle atypes.RemoteHandle, payload map[string]any) error {
	return a.driver.ResumeRun(asString(handle.AdapterState, "endpoint"), handle.RemoteExecutionID, payload)
}
func (a *ACPHTTPAdapter) Cancel(ctx context.Context, handle atypes.RemoteHandle) error {
	return a.driver.CancelRun(asString(handle.AdapterState, "endpoint"), handle.RemoteExecutionID)
}
func (a *ACPHTTPAdapter) Health(ctx context.Context, runtimeID string, options map[string]any) (map[string]any, error) {
	return a.runtime.Health(runtimeID, ""), nil
}

func eventFromHTTP(taskID string, raw map[string]any) atypes.TaskEvent {
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
	return atypes.TaskEvent{EventID: atypes.NewID(), TaskID: taskID, Kind: kind, State: state, Source: atypes.EventSourceAdapter, Message: message, Data: data, CreatedAt: atypes.NowUTC()}
}

func asString(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	value, _ := m[key].(string)
	return value
}

func jsonMarshal(v map[string]any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
