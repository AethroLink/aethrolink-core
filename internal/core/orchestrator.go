package core

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"

	"github.com/aethrolink/aethrolink-core/internal/adapters"
	"github.com/aethrolink/aethrolink-core/internal/runtime"
	"github.com/aethrolink/aethrolink-core/internal/storage"
	atypes "github.com/aethrolink/aethrolink-core/pkg/types"
)

var (
	ErrTargetAgentNotFound       = errors.New("target agent not found")
	ErrRouteNotFound             = errors.New("route not found")
	ErrRouteAmbiguous            = errors.New("routing conflict")
	ErrTargetCannotSatisfyIntent = errors.New("target agent cannot satisfy intent")
	ErrTaskNotFound              = errors.New("task not found")
	ErrTaskNotAwaitable          = errors.New("task not awaitable")
	ErrThreadNotFound            = errors.New("thread not found")
	ErrThreadAgentPairInvalid    = errors.New("thread agent pair invalid")
)

type subscriber struct {
	taskID string
	ch     chan atypes.TaskEvent
}

type Orchestrator struct {
	discovery        atypes.DiscoveryProvider
	store            *storage.SQLiteStore
	runtime          *runtime.Manager
	adapters         *adapters.Registry
	mu               sync.RWMutex
	subscribers      map[int]subscriber
	nextSubscriberID int
}

func NewOrchestrator(discovery atypes.DiscoveryProvider, store *storage.SQLiteStore, runtimeManager *runtime.Manager, adapterRegistry *adapters.Registry) (*Orchestrator, error) {
	o := &Orchestrator{discovery: discovery, store: store, runtime: runtimeManager, adapters: adapterRegistry, subscribers: map[int]subscriber{}}
	// Restart reconciliation runs once at boot so persisted non-terminal threads
	// are marked honestly before operators continue them.
	if err := o.store.MarkInterruptedThreadsOnRestart(context.Background()); err != nil {
		return nil, fmt.Errorf("mark interrupted threads on restart: %w", err)
	}
	return o, nil
}

func mergeMaps(base, override map[string]any) map[string]any {
	out := map[string]any{}
	for key, value := range base {
		if nested, ok := value.(map[string]any); ok {
			out[key] = mergeMaps(nested, nil)
		} else {
			out[key] = value
		}
	}
	for key, value := range override {
		if value == nil {
			continue
		}
		if existing, ok := out[key].(map[string]any); ok {
			if nested, ok := value.(map[string]any); ok {
				out[key] = mergeMaps(existing, nested)
				continue
			}
		}
		out[key] = value
	}
	return out
}

func supportsIntent(spec atypes.RuntimeSpec, intent string) bool {
	for _, capability := range spec.Capabilities {
		if capability == intent {
			return true
		}
	}
	return false
}

// isDispatchableRuntime keeps discovery-only remote targets out of local adapter execution.
func isDispatchableRuntime(spec atypes.RuntimeSpec) bool {
	return spec.Owner != atypes.TargetOwnerRemote && spec.Adapter != ""
}

func (o *Orchestrator) routeRequest(ctx context.Context, req atypes.TaskCreateRequest) (string, map[string]any, error) {
	if err := atypes.ValidateIntent(req.Intent); err != nil {
		return "", nil, err
	}
	if req.TargetAgentID != "" {
		spec, err := o.discovery.ResolveRuntime(ctx, req.TargetAgentID)
		if err != nil {
			return "", nil, ErrTargetAgentNotFound
		}
		if !isDispatchableRuntime(spec) {
			return "", nil, ErrTargetAgentNotFound
		}
		if !supportsIntent(spec, req.Intent) {
			return "", nil, ErrTargetCannotSatisfyIntent
		}
		return req.TargetAgentID, mergeMaps(spec.Defaults, req.RuntimeOptions), nil
	}
	runtimes, err := o.discovery.ListRuntimes(ctx)
	if err != nil {
		return "", nil, err
	}
	var matches []atypes.RuntimeSpec
	for _, runtimeSpec := range runtimes {
		if isDispatchableRuntime(runtimeSpec) && supportsIntent(runtimeSpec, req.Intent) {
			matches = append(matches, runtimeSpec)
		}
	}
	switch len(matches) {
	case 0:
		return "", nil, ErrRouteNotFound
	case 1:
		return matches[0].TargetID, mergeMaps(matches[0].Defaults, req.RuntimeOptions), nil
	default:
		return "", nil, ErrRouteAmbiguous
	}
}

func (o *Orchestrator) appendEvent(ctx context.Context, taskID string, kind atypes.TaskEventKind, state atypes.TaskStatus, source atypes.EventSource, message string, data map[string]any, remote *atypes.RemoteHandle, taskErr *atypes.TaskError, resultArtifactID string) (atypes.TaskEvent, error) {
	seq, err := o.store.NextSeq(ctx, taskID)
	if err != nil {
		return atypes.TaskEvent{}, err
	}
	event := atypes.TaskEvent{EventID: atypes.NewID(), TaskID: taskID, Seq: seq, Kind: kind, State: state, Source: source, Message: message, Data: cloneMap(data), CreatedAt: atypes.NowUTC()}
	if err := o.store.AppendEventAndUpdateTask(ctx, event, remote, taskErr, resultArtifactID); err != nil {
		return atypes.TaskEvent{}, err
	}
	if err := o.store.UpdateThreadTurnStatusByTaskID(ctx, taskID, string(state), remote); err != nil {
		return atypes.TaskEvent{}, err
	}
	o.mu.RLock()
	defer o.mu.RUnlock()
	for _, sub := range o.subscribers {
		if sub.taskID == taskID {
			select {
			case sub.ch <- event:
			default:
			}
		}
	}
	return event, nil
}

func cloneMap(in map[string]any) map[string]any {
	if in == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		switch typed := value.(type) {
		case map[string]any:
			out[key] = cloneMap(typed)
		default:
			out[key] = typed
		}
	}
	return out
}

func (o *Orchestrator) CreateTask(ctx context.Context, req atypes.TaskCreateRequest) (atypes.TaskRecord, error) {
	conversationSpecified := req.ConversationID != ""
	req.Normalize()
	var thread *atypes.ThreadRecord
	if req.ThreadID != "" {
		loadedThread, err := o.getThreadRecord(ctx, req.ThreadID)
		if err != nil {
			return atypes.TaskRecord{}, err
		}
		sender, target, err := validateThreadParticipants(loadedThread, req.Sender, req.TargetAgentID)
		if err != nil {
			return atypes.TaskRecord{}, err
		}
		if !conversationSpecified {
			// Thread-bound tasks keep one stable conversation identity unless the
			// caller overrides it explicitly for a special-case recovery flow.
			req.ConversationID = loadedThread.ThreadID
		}
		req.Sender = sender
		req.TargetAgentID = target
		thread = &loadedThread
	}
	resolvedRuntime, runtimeOptions, err := o.routeRequest(ctx, req)
	if err != nil {
		return atypes.TaskRecord{}, err
	}
	payloadArtifact, err := o.store.StoreJSONArtifact(ctx, req.Payload)
	if err != nil {
		return atypes.TaskRecord{}, err
	}
	now := atypes.NowUTC()
	task := atypes.TaskRecord{TaskID: atypes.NewID(), ThreadID: req.ThreadID, ConversationID: req.ConversationID, Sender: req.Sender, Intent: req.Intent, RequestedAgentID: req.TargetAgentID, ResolvedAgentID: resolvedRuntime, RuntimeOptions: cloneMap(runtimeOptions), PayloadArtifactID: payloadArtifact.ArtifactID, Status: atypes.TaskStatusCreated, CreatedAt: now, UpdatedAt: now}
	if err := o.store.InsertTask(ctx, task); err != nil {
		return atypes.TaskRecord{}, err
	}
	if _, err := o.appendEvent(ctx, task.TaskID, atypes.TaskEventTaskCreated, atypes.TaskStatusCreated, atypes.EventSourceCore, "Task created", map[string]any{}, nil, nil, ""); err != nil {
		return atypes.TaskRecord{}, err
	}
	if _, err := o.appendEvent(ctx, task.TaskID, atypes.TaskEventTaskRouted, atypes.TaskStatusCreated, atypes.EventSourceCore, "Route resolved", map[string]any{"resolved_agent_id": resolvedRuntime, "runtime_options": runtimeOptions}, nil, nil, ""); err != nil {
		return atypes.TaskRecord{}, err
	}
	if thread != nil {
		if err := o.appendTaskToThread(ctx, *thread, task); err != nil {
			return atypes.TaskRecord{}, err
		}
	}
	go o.runTask(task.TaskID, *req.Delivery, cloneMap(req.Payload))
	return o.GetTask(ctx, task.TaskID)
}

// rehydrateRemoteHandle asks the adapter to rebuild any adapter-private
// handle state from the persisted task record.
func (o *Orchestrator) rehydrateRemoteHandle(task atypes.TaskRecord) atypes.RemoteHandle {
	spec, err := o.discovery.ResolveRuntime(context.Background(), task.ResolvedAgentID)
	if err != nil {
		return atypes.RemoteHandle{TaskID: task.TaskID, TargetID: task.ResolvedAgentID}
	}
	adapter, ok := o.adapters.Get(spec.Adapter)
	if !ok {
		return atypes.RemoteHandle{TaskID: task.TaskID, TargetID: task.ResolvedAgentID}
	}
	handle, err := adapter.RehydrateHandle(task, spec)
	if err != nil {
		return atypes.RemoteHandle{TaskID: task.TaskID, TargetID: task.ResolvedAgentID}
	}
	return handle
}

// runTask is the generic execution loop. It owns task lifecycle transitions,
// but delegates runtime-specific details to the selected adapter.
func (o *Orchestrator) runTask(taskID string, delivery atypes.DeliveryPolicy, payload map[string]any) {
	ctx := context.Background()
	task, err := o.store.GetTask(ctx, taskID)
	if err != nil {
		return
	}
	spec, err := o.discovery.ResolveRuntime(ctx, task.ResolvedAgentID)
	if err != nil {
		return
	}
	adapter, ok := o.adapters.Get(spec.Adapter)
	if !ok {
		taskErr := &atypes.TaskError{Reason: "adapter missing", Detail: spec.Adapter}
		_, _ = o.appendEvent(ctx, taskID, atypes.TaskEventTaskFailed, atypes.TaskStatusFailed, atypes.EventSourceCore, taskErr.Reason, map[string]any{"detail": taskErr.Detail}, nil, taskErr, "")
		return
	}
	health, _ := adapter.Health(ctx, task.ResolvedAgentID, task.RuntimeOptions)
	healthy, _ := health["healthy"].(bool)
	var lease atypes.RuntimeLease
	if healthy {
		lease, err = adapter.EnsureReady(ctx, task.ResolvedAgentID, task.RuntimeOptions)
		if err != nil {
			taskErr := &atypes.TaskError{Reason: "ensure_ready_failed", Detail: err.Error()}
			_, _ = o.appendEvent(ctx, taskID, atypes.TaskEventTaskFailed, atypes.TaskStatusFailed, atypes.EventSourceRuntime, taskErr.Reason, map[string]any{"detail": taskErr.Detail}, nil, taskErr, "")
			return
		}
		_, _ = o.appendEvent(ctx, taskID, atypes.TaskEventRuntimeReady, atypes.TaskStatusReady, atypes.EventSourceRuntime, "Runtime ready", map[string]any{"lease_id": lease.LeaseID}, nil, nil, "")
	} else if delivery.LaunchIfDown {
		_, _ = o.appendEvent(ctx, taskID, atypes.TaskEventRuntimePendingLaunch, atypes.TaskStatusPendingLaunch, atypes.EventSourceCore, "Runtime launch required", map[string]any{}, nil, nil, "")
		_, _ = o.appendEvent(ctx, taskID, atypes.TaskEventRuntimeLaunching, atypes.TaskStatusLaunching, atypes.EventSourceRuntime, "Launching runtime", map[string]any{}, nil, nil, "")
		lease, err = adapter.EnsureReady(ctx, task.ResolvedAgentID, task.RuntimeOptions)
		if err != nil {
			taskErr := &atypes.TaskError{Reason: "launch_failed", Detail: err.Error()}
			_, _ = o.appendEvent(ctx, taskID, atypes.TaskEventTaskFailed, atypes.TaskStatusFailed, atypes.EventSourceRuntime, taskErr.Reason, map[string]any{"detail": taskErr.Detail}, nil, taskErr, "")
			return
		}
		_, _ = o.appendEvent(ctx, taskID, atypes.TaskEventRuntimeReady, atypes.TaskStatusReady, atypes.EventSourceRuntime, "Runtime ready", map[string]any{"lease_id": lease.LeaseID}, nil, nil, "")
	} else {
		taskErr := &atypes.TaskError{Reason: "runtime unavailable", Detail: "launch_if_down=false and runtime reported unhealthy"}
		_, _ = o.appendEvent(ctx, taskID, atypes.TaskEventTaskFailed, atypes.TaskStatusFailed, atypes.EventSourceRuntime, taskErr.Reason, map[string]any{"detail": taskErr.Detail}, nil, taskErr, "")
		return
	}
	_, _ = o.appendEvent(ctx, taskID, atypes.TaskEventTaskDispatching, atypes.TaskStatusDispatching, atypes.EventSourceCore, "Dispatching task", map[string]any{"target_agent_id": task.ResolvedAgentID}, nil, nil, "")
	envelope := atypes.TaskEnvelope{TaskID: task.TaskID, ThreadID: task.ThreadID, ConversationID: task.ConversationID, Sender: task.Sender, TargetAgentID: task.ResolvedAgentID, Intent: task.Intent, Payload: payload, RuntimeOptions: task.RuntimeOptions, Delivery: delivery, Trace: atypes.DefaultTraceContext(), Metadata: map[string]any{}}
	handle, err := adapter.Submit(ctx, envelope, lease)
	if err != nil {
		taskErr := &atypes.TaskError{Reason: "submit_failed", Detail: err.Error()}
		_, _ = o.appendEvent(ctx, taskID, atypes.TaskEventTaskFailed, atypes.TaskStatusFailed, atypes.EventSourceAdapter, taskErr.Reason, map[string]any{"detail": taskErr.Detail}, nil, taskErr, "")
		return
	}
	_, _ = o.appendEvent(ctx, taskID, atypes.TaskEventTaskDispatching, atypes.TaskStatusDispatching, atypes.EventSourceAdapter, "Task submitted to runtime", map[string]any{"binding": handle.Binding}, &handle, nil, "")
	eventCh, errCh := adapter.StreamEvents(ctx, handle)
	for {
		select {
		case event, ok := <-eventCh:
			if !ok {
				return
			}
			resultArtifactID := ""
			if event.State == atypes.TaskStatusCompleted {
				if result, ok := event.Data["result"].(map[string]any); ok {
					if artifact, err := o.store.StoreJSONArtifact(ctx, result); err == nil {
						resultArtifactID = artifact.ArtifactID
					}
				}
			}
			var taskErr *atypes.TaskError
			if event.State == atypes.TaskStatusFailed {
				taskErr = &atypes.TaskError{Reason: "remote_failed", Detail: asString(event.Data, "reason")}
			}
			_, _ = o.appendEvent(ctx, taskID, event.Kind, event.State, event.Source, event.Message, event.Data, &handle, taskErr, resultArtifactID)
			if event.State.IsTerminal() {
				return
			}
		case err, ok := <-errCh:
			if ok && err != nil {
				taskErr := &atypes.TaskError{Reason: "stream_failed", Detail: err.Error()}
				_, _ = o.appendEvent(ctx, taskID, atypes.TaskEventTaskFailed, atypes.TaskStatusFailed, atypes.EventSourceAdapter, taskErr.Reason, map[string]any{"detail": taskErr.Detail}, &handle, taskErr, "")
			}
			return
		}
	}
}

func (o *Orchestrator) GetTask(ctx context.Context, taskID string) (atypes.TaskRecord, error) {
	task, err := o.store.GetTask(ctx, taskID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return atypes.TaskRecord{}, ErrTaskNotFound
		}
		return atypes.TaskRecord{}, err
	}
	return task, nil
}

func (o *Orchestrator) ListEvents(ctx context.Context, taskID string) ([]atypes.TaskEvent, error) {
	if _, err := o.GetTask(ctx, taskID); err != nil {
		return nil, err
	}
	return o.store.ListEvents(ctx, taskID)
}

func (o *Orchestrator) Subscribe(taskID string) (<-chan atypes.TaskEvent, func()) {
	o.mu.Lock()
	defer o.mu.Unlock()
	id := o.nextSubscriberID
	o.nextSubscriberID++
	ch := make(chan atypes.TaskEvent, 32)
	o.subscribers[id] = subscriber{taskID: taskID, ch: ch}
	cancel := func() {
		o.mu.Lock()
		defer o.mu.Unlock()
		if sub, ok := o.subscribers[id]; ok {
			delete(o.subscribers, id)
			close(sub.ch)
		}
	}
	return ch, cancel
}

func (o *Orchestrator) ResumeTask(ctx context.Context, taskID string, payload map[string]any) (atypes.TaskRecord, error) {
	task, err := o.GetTask(ctx, taskID)
	if err != nil {
		return atypes.TaskRecord{}, err
	}
	if task.Status != atypes.TaskStatusAwaitingInput {
		return atypes.TaskRecord{}, ErrTaskNotAwaitable
	}
	spec, err := o.discovery.ResolveRuntime(ctx, task.ResolvedAgentID)
	if err != nil {
		return atypes.TaskRecord{}, ErrTargetAgentNotFound
	}
	adapter, ok := o.adapters.Get(spec.Adapter)
	if !ok {
		return atypes.TaskRecord{}, fmt.Errorf("adapter missing")
	}
	handle := o.rehydrateRemoteHandle(task)
	if err := adapter.Resume(ctx, handle, payload); err != nil {
		return atypes.TaskRecord{}, err
	}
	_, _ = o.appendEvent(ctx, taskID, atypes.TaskEventTaskResumeRequested, atypes.TaskStatusAwaitingInput, atypes.EventSourceAPI, "Resume requested", payload, &handle, nil, "")
	return o.GetTask(ctx, taskID)
}

func (o *Orchestrator) CancelTask(ctx context.Context, taskID, reason string) (atypes.TaskRecord, error) {
	task, err := o.GetTask(ctx, taskID)
	if err != nil {
		return atypes.TaskRecord{}, err
	}
	if task.Status.IsTerminal() {
		return task, nil
	}
	spec, err := o.discovery.ResolveRuntime(ctx, task.ResolvedAgentID)
	if err != nil {
		return atypes.TaskRecord{}, ErrTargetAgentNotFound
	}
	adapter, ok := o.adapters.Get(spec.Adapter)
	if !ok {
		return atypes.TaskRecord{}, fmt.Errorf("adapter missing")
	}
	handle := o.rehydrateRemoteHandle(task)
	payload := map[string]any{}
	if reason != "" {
		payload["reason"] = reason
	}
	if err := adapter.Cancel(ctx, handle); err != nil {
		return atypes.TaskRecord{}, err
	}
	_, _ = o.appendEvent(ctx, taskID, atypes.TaskEventTaskCancelRequested, task.Status, atypes.EventSourceAPI, "Cancel requested", payload, &handle, nil, "")
	return o.GetTask(ctx, taskID)
}

func (o *Orchestrator) ListTargets(ctx context.Context) ([]atypes.RuntimeSpec, error) {
	return o.discovery.ListRuntimes(ctx)
}

func (o *Orchestrator) TargetHealth(ctx context.Context, agentID string) (map[string]any, error) {
	spec, err := o.discovery.ResolveRuntime(ctx, agentID)
	if err != nil {
		return nil, ErrTargetAgentNotFound
	}
	adapter, ok := o.adapters.Get(spec.Adapter)
	if !ok {
		return nil, fmt.Errorf("adapter missing")
	}
	return adapter.Health(ctx, agentID, spec.Defaults)
}

func (o *Orchestrator) StartTarget(ctx context.Context, agentID string, runtimeOptions map[string]any) (map[string]any, error) {
	spec, err := o.discovery.ResolveRuntime(ctx, agentID)
	if err != nil {
		return nil, ErrTargetAgentNotFound
	}
	adapter, ok := o.adapters.Get(spec.Adapter)
	if !ok {
		return nil, fmt.Errorf("adapter missing")
	}
	lease, err := adapter.EnsureReady(ctx, agentID, mergeMaps(spec.Defaults, runtimeOptions))
	if err != nil {
		return nil, err
	}
	return map[string]any{"target_agent_id": agentID, "healthy": true, "lease_id": lease.LeaseID, "process_id": lease.ProcessID, "runtime_options": cloneMap(runtimeOptions)}, nil
}

func (o *Orchestrator) StopTarget(ctx context.Context, agentID string, runtimeOptions map[string]any) (map[string]any, error) {
	spec, err := o.discovery.ResolveRuntime(ctx, agentID)
	if err != nil {
		return nil, ErrTargetAgentNotFound
	}
	adapter, ok := o.adapters.Get(spec.Adapter)
	if !ok {
		return nil, fmt.Errorf("adapter missing")
	}
	merged := mergeMaps(spec.Defaults, runtimeOptions)
	subcontext := adapter.SubcontextKey(spec, merged)
	if err := o.runtime.Stop(ctx, agentID, subcontext); err != nil {
		return nil, err
	}
	return map[string]any{"target_agent_id": agentID, "healthy": false, "runtime_options": cloneMap(runtimeOptions)}, nil
}

func (o *Orchestrator) StopAllRuntimeProcesses(ctx context.Context) error {
	return o.runtime.StopAll(ctx)
}

func (o *Orchestrator) LoadArtifactPath(ctx context.Context, artifactID string) (string, error) {
	return o.store.LoadArtifactPath(ctx, artifactID)
}

func asString(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	value, _ := m[key].(string)
	return value
}

func ErrorStatus(err error) int {
	switch {
	case errors.Is(err, ErrTaskNotFound), errors.Is(err, ErrTargetAgentNotFound), errors.Is(err, ErrRouteNotFound), errors.Is(err, ErrThreadNotFound):
		return 404
	case errors.Is(err, ErrRouteAmbiguous):
		return 409
	case errors.Is(err, ErrTargetCannotSatisfyIntent), errors.Is(err, ErrTaskNotAwaitable), errors.Is(err, ErrThreadAgentPairInvalid):
		return 422
	default:
		return 400
	}
}

func ErrorMessage(err error) string {
	if err == nil {
		return ""
	}
	return fmt.Sprintf("%v", err)
}
