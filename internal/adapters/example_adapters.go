package adapters

import (
	"context"
	"fmt"
	"sync"

	"github.com/aethrolink/aethrolink-core/internal/config"
	"github.com/aethrolink/aethrolink-core/internal/drivers"
	"github.com/aethrolink/aethrolink-core/internal/runtime"
	atypes "github.com/aethrolink/aethrolink-core/pkg/types"
)

type MockHermesAdapter struct {
	registry *config.RegistryDiscovery
	runtime  *runtime.Manager
}

func NewMockHermesAdapter(registry *config.RegistryDiscovery, runtimeManager *runtime.Manager) *MockHermesAdapter {
	return &MockHermesAdapter{registry: registry, runtime: runtimeManager}
}

func (a *MockHermesAdapter) AdapterName() string { return "mock_hermes" }
func (a *MockHermesAdapter) Capabilities(context.Context) (map[string]any, error) {
	return map[string]any{"adapter": "mock_hermes", "mode": "example"}, nil
}
func (a *MockHermesAdapter) EnsureReady(ctx context.Context, runtimeID string, options map[string]any) (atypes.RuntimeLease, error) {
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
func (a *MockHermesAdapter) Submit(ctx context.Context, task atypes.TaskEnvelope, lease atypes.RuntimeLease) (atypes.RemoteHandle, error) {
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
func (a *MockHermesAdapter) StreamEvents(ctx context.Context, handle atypes.RemoteHandle) (<-chan atypes.TaskEvent, <-chan error) {
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
func (a *MockHermesAdapter) Resume(ctx context.Context, handle atypes.RemoteHandle, payload map[string]any) error {
	worker := a.runtime.GetStdioWorker(handle.RuntimeID, "profile:"+asString(handle.AdapterState, "profile"))
	if worker == nil {
		return fmt.Errorf("missing stdio worker")
	}
	return drivers.NewACPClientDriver(worker).SessionResume(handle.RemoteSessionID, payload)
}
func (a *MockHermesAdapter) Cancel(ctx context.Context, handle atypes.RemoteHandle) error {
	worker := a.runtime.GetStdioWorker(handle.RuntimeID, "profile:"+asString(handle.AdapterState, "profile"))
	if worker == nil {
		return fmt.Errorf("missing stdio worker")
	}
	return drivers.NewACPClientDriver(worker).SessionCancel(handle.RemoteSessionID)
}
func (a *MockHermesAdapter) Health(ctx context.Context, runtimeID string, options map[string]any) (map[string]any, error) {
	return a.runtime.Health(runtimeID, a.SubcontextKey(atypes.RuntimeSpec{}, options)), nil
}

func (a *MockHermesAdapter) RehydrateHandle(task atypes.TaskRecord, spec atypes.RuntimeSpec) (atypes.RemoteHandle, error) {
	handle := atypes.RemoteHandle{TaskID: task.TaskID, RuntimeID: task.ResolvedRuntime}
	if task.Remote != nil {
		handle.Binding = task.Remote.Binding
		handle.RemoteExecutionID = task.Remote.RemoteExecutionID
		handle.RemoteSessionID = task.Remote.RemoteSessionID
	}
	profile := asString(task.RuntimeOptions, "profile")
	if profile == "" {
		profile = "coder"
	}
	handle.AdapterState = map[string]any{"profile": profile, "session_id": handle.RemoteSessionID}
	return handle, nil
}

func (a *MockHermesAdapter) SubcontextKey(spec atypes.RuntimeSpec, runtimeOptions map[string]any) string {
	profile := asString(runtimeOptions, "profile")
	if profile == "" {
		profile = "coder"
	}
	return "profile:" + profile
}

type MockOpenClawAdapter struct {
	registry *config.RegistryDiscovery
	runtime  *runtime.Manager
	mu       sync.Mutex
	sessions map[string]string
}

func NewMockOpenClawAdapter(registry *config.RegistryDiscovery, runtimeManager *runtime.Manager) *MockOpenClawAdapter {
	return &MockOpenClawAdapter{registry: registry, runtime: runtimeManager, sessions: map[string]string{}}
}

func (a *MockOpenClawAdapter) AdapterName() string { return "mock_openclaw" }
func (a *MockOpenClawAdapter) Capabilities(context.Context) (map[string]any, error) {
	return map[string]any{"adapter": "mock_openclaw", "mode": "example"}, nil
}
func (a *MockOpenClawAdapter) EnsureReady(ctx context.Context, runtimeID string, options map[string]any) (atypes.RuntimeLease, error) {
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
func (a *MockOpenClawAdapter) Submit(ctx context.Context, task atypes.TaskEnvelope, lease atypes.RuntimeLease) (atypes.RemoteHandle, error) {
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
func (a *MockOpenClawAdapter) StreamEvents(ctx context.Context, handle atypes.RemoteHandle) (<-chan atypes.TaskEvent, <-chan error) {
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
func (a *MockOpenClawAdapter) Resume(ctx context.Context, handle atypes.RemoteHandle, payload map[string]any) error {
	worker := a.runtime.GetStdioWorker(handle.RuntimeID, "session:"+asString(handle.AdapterState, "session_key"))
	if worker == nil {
		return fmt.Errorf("missing stdio worker")
	}
	return drivers.NewACPClientDriver(worker).SessionResume(handle.RemoteSessionID, payload)
}
func (a *MockOpenClawAdapter) Cancel(ctx context.Context, handle atypes.RemoteHandle) error {
	worker := a.runtime.GetStdioWorker(handle.RuntimeID, "session:"+asString(handle.AdapterState, "session_key"))
	if worker == nil {
		return fmt.Errorf("missing stdio worker")
	}
	return drivers.NewACPClientDriver(worker).SessionCancel(handle.RemoteSessionID)
}
func (a *MockOpenClawAdapter) Health(ctx context.Context, runtimeID string, options map[string]any) (map[string]any, error) {
	return a.runtime.Health(runtimeID, a.SubcontextKey(atypes.RuntimeSpec{}, options)), nil
}

func (a *MockOpenClawAdapter) RehydrateHandle(task atypes.TaskRecord, spec atypes.RuntimeSpec) (atypes.RemoteHandle, error) {
	handle := atypes.RemoteHandle{TaskID: task.TaskID, RuntimeID: task.ResolvedRuntime}
	if task.Remote != nil {
		handle.Binding = task.Remote.Binding
		handle.RemoteExecutionID = task.Remote.RemoteExecutionID
		handle.RemoteSessionID = task.Remote.RemoteSessionID
	}
	sessionKey := asString(task.RuntimeOptions, "session_key")
	if sessionKey == "" {
		sessionKey = "main"
	}
	handle.AdapterState = map[string]any{"session_key": sessionKey, "session_id": handle.RemoteSessionID}
	return handle, nil
}

func (a *MockOpenClawAdapter) SubcontextKey(spec atypes.RuntimeSpec, runtimeOptions map[string]any) string {
	sessionKey := asString(runtimeOptions, "session_key")
	if sessionKey == "" {
		sessionKey = "main"
	}
	return "session:" + sessionKey
}

type MockACPHTTPAdapter struct {
	registry *config.RegistryDiscovery
	runtime  *runtime.Manager
	driver   *drivers.HTTPACPDriver
}

func NewMockACPHTTPAdapter(registry *config.RegistryDiscovery, runtimeManager *runtime.Manager) *MockACPHTTPAdapter {
	return &MockACPHTTPAdapter{registry: registry, runtime: runtimeManager, driver: drivers.NewHTTPACPDriver()}
}

func (a *MockACPHTTPAdapter) AdapterName() string { return "mock_acp_comm_http" }
func (a *MockACPHTTPAdapter) Capabilities(context.Context) (map[string]any, error) {
	return map[string]any{"adapter": "mock_acp_comm_http", "mode": "example"}, nil
}
func (a *MockACPHTTPAdapter) EnsureReady(ctx context.Context, runtimeID string, options map[string]any) (atypes.RuntimeLease, error) {
	spec, err := a.registry.ResolveRuntime(ctx, runtimeID)
	if err != nil {
		return atypes.RuntimeLease{}, err
	}
	return a.runtime.EnsureProcess(ctx, runtimeID, "", spec.Launch.Command, spec.Healthcheck)
}
func (a *MockACPHTTPAdapter) Submit(ctx context.Context, task atypes.TaskEnvelope, lease atypes.RuntimeLease) (atypes.RemoteHandle, error) {
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
func (a *MockACPHTTPAdapter) StreamEvents(ctx context.Context, handle atypes.RemoteHandle) (<-chan atypes.TaskEvent, <-chan error) {
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
func (a *MockACPHTTPAdapter) Resume(ctx context.Context, handle atypes.RemoteHandle, payload map[string]any) error {
	return a.driver.ResumeRun(asString(handle.AdapterState, "endpoint"), handle.RemoteExecutionID, payload)
}
func (a *MockACPHTTPAdapter) Cancel(ctx context.Context, handle atypes.RemoteHandle) error {
	return a.driver.CancelRun(asString(handle.AdapterState, "endpoint"), handle.RemoteExecutionID)
}
func (a *MockACPHTTPAdapter) Health(ctx context.Context, runtimeID string, options map[string]any) (map[string]any, error) {
	return a.runtime.Health(runtimeID, a.SubcontextKey(atypes.RuntimeSpec{}, options)), nil
}

func (a *MockACPHTTPAdapter) RehydrateHandle(task atypes.TaskRecord, spec atypes.RuntimeSpec) (atypes.RemoteHandle, error) {
	handle := atypes.RemoteHandle{TaskID: task.TaskID, RuntimeID: task.ResolvedRuntime}
	if task.Remote != nil {
		handle.Binding = task.Remote.Binding
		handle.RemoteExecutionID = task.Remote.RemoteExecutionID
		handle.RemoteSessionID = task.Remote.RemoteSessionID
	}
	handle.AdapterState = map[string]any{"endpoint": spec.Endpoint}
	return handle, nil
}

func (a *MockACPHTTPAdapter) SubcontextKey(spec atypes.RuntimeSpec, runtimeOptions map[string]any) string {
	return ""
}
