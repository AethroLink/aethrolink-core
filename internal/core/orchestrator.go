package core

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"

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

// validateThreadParticipants enforces that a thread-bound task stays inside its pair.
func validateThreadParticipants(thread atypes.ThreadRecord, sender, target string) (string, string, error) {
	if sender == "" {
		return "", "", ErrThreadAgentPairInvalid
	}
	if sender != thread.AgentAID && sender != thread.AgentBID {
		return "", "", ErrThreadAgentPairInvalid
	}
	if target == "" {
		if sender == thread.AgentAID {
			return sender, thread.AgentBID, nil
		}
		return sender, thread.AgentAID, nil
	}
	if target == sender {
		return "", "", ErrThreadAgentPairInvalid
	}
	if target != thread.AgentAID && target != thread.AgentBID {
		return "", "", ErrThreadAgentPairInvalid
	}
	return sender, target, nil
}

// getThreadRecord loads a thread and converts storage misses into core errors.
func (o *Orchestrator) getThreadRecord(ctx context.Context, threadID string) (atypes.ThreadRecord, error) {
	thread, err := o.store.GetThread(ctx, threadID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return atypes.ThreadRecord{}, ErrThreadNotFound
		}
		return atypes.ThreadRecord{}, err
	}
	return thread, nil
}

// deriveThreadContinuationPair fills omitted sender/target values from persisted
// thread history so manual continuation can alternate cleanly by default.
func deriveThreadContinuationPair(thread atypes.ThreadRecord, sender, target string) (string, string, error) {
	if sender == "" {
		switch {
		case target == thread.AgentAID:
			sender = thread.AgentBID
		case target == thread.AgentBID:
			sender = thread.AgentAID
		case thread.LastTargetAgentID != "":
			sender = thread.LastTargetAgentID
		default:
			sender = thread.AgentAID
		}
	}
	if target == "" {
		if sender == thread.AgentAID {
			target = thread.AgentBID
		} else {
			target = thread.AgentAID
		}
	}
	return validateThreadParticipants(thread, sender, target)
}

// autoContinueWaitTimeout keeps controller-owned loops bounded per turn.
func autoContinueWaitTimeout(delivery *atypes.DeliveryPolicy) time.Duration {
	if delivery != nil && delivery.TimeoutMS != nil && *delivery.TimeoutMS > 0 {
		return time.Duration(*delivery.TimeoutMS) * time.Millisecond
	}
	return 60 * time.Second
}

// waitForThreadTaskStop polls durable task state until one turn reaches a stop boundary.
func (o *Orchestrator) waitForThreadTaskStop(ctx context.Context, taskID string, timeout time.Duration) (atypes.TaskRecord, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		task, err := o.GetTask(ctx, taskID)
		if err == nil && (task.Status == atypes.TaskStatusAwaitingInput || task.Status.IsTerminal()) {
			return task, nil
		}
		select {
		case <-ctx.Done():
			return atypes.TaskRecord{}, ctx.Err()
		case <-ticker.C:
		}
	}
}

// continueThreadAutoHandoff runs a bounded controller-owned loop above persisted turns.
func (o *Orchestrator) continueThreadAutoHandoff(threadID string, req atypes.ThreadContinueRequest, previous atypes.TaskRecord) {
	ctx := context.Background()
	lastTask := previous
	for turn := 1; turn < req.MaxTurns; turn++ {
		stoppedTask, err := o.waitForThreadTaskStop(ctx, lastTask.TaskID, autoContinueWaitTimeout(req.Delivery))
		if err != nil {
			return
		}
		if stoppedTask.Status == atypes.TaskStatusAwaitingInput {
			return
		}
		if req.StopOnTerminalError && stoppedTask.Status != atypes.TaskStatusCompleted {
			return
		}
		thread, err := o.getThreadRecord(ctx, threadID)
		if err != nil {
			return
		}
		sender, target, err := deriveThreadContinuationPair(thread, "", "")
		if err != nil {
			return
		}
		if req.StopOnSameActorRepeat && sender == lastTask.Sender {
			return
		}
		nextTask, err := o.CreateTask(ctx, atypes.TaskCreateRequest{
			ThreadID:       threadID,
			Sender:         sender,
			TargetAgentID:  target,
			Intent:         req.IntentForAgent(sender),
			Payload:        cloneMap(req.PayloadForAgent(sender)),
			RuntimeOptions: cloneMap(req.RuntimeOptions),
			ConversationID: req.ConversationID,
			Delivery:       req.Delivery,
			Metadata:       cloneMap(req.Metadata),
		})
		if err != nil {
			return
		}
		lastTask = nextTask
	}
}

// appendTaskToThread records the created task as the next ordered thread turn.
func (o *Orchestrator) appendTaskToThread(ctx context.Context, thread atypes.ThreadRecord, task atypes.TaskRecord) error {
	now := atypes.NowUTC()
	turn := atypes.ThreadTurn{
		ThreadID:      thread.ThreadID,
		TaskID:        task.TaskID,
		SenderAgentID: task.Sender,
		TargetAgentID: task.ResolvedAgentID,
		Status:        string(task.Status),
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	return o.store.AppendThreadTurnAndUpdateThread(ctx, thread.ThreadID, turn, task.TaskID, task.Sender, task.ResolvedAgentID, now)
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
		if supportsIntent(runtimeSpec, req.Intent) {
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

// CreateThread creates the durable pair boundary above individual task hops.
func (o *Orchestrator) CreateThread(ctx context.Context, req atypes.ThreadCreateRequest) (atypes.ThreadRecord, error) {
	req.Normalize()
	if req.AgentAID == "" || req.AgentBID == "" || req.AgentAID == req.AgentBID {
		return atypes.ThreadRecord{}, ErrThreadAgentPairInvalid
	}
	if _, err := o.discovery.ResolveRuntime(ctx, req.AgentAID); err != nil {
		return atypes.ThreadRecord{}, ErrTargetAgentNotFound
	}
	if _, err := o.discovery.ResolveRuntime(ctx, req.AgentBID); err != nil {
		return atypes.ThreadRecord{}, ErrTargetAgentNotFound
	}
	now := atypes.NowUTC()
	thread := atypes.ThreadRecord{
		ThreadID:      atypes.NewID(),
		AgentAID:      req.AgentAID,
		AgentBID:      req.AgentBID,
		Status:        atypes.ThreadStatusActive,
		ContinuityKey: req.ContinuityKey,
		Metadata:      cloneMap(req.Metadata),
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := o.store.InsertThread(ctx, thread); err != nil {
		return atypes.ThreadRecord{}, err
	}
	return thread, nil
}

// GetThread returns one persisted thread record by id.
func (o *Orchestrator) GetThread(ctx context.Context, threadID string) (atypes.ThreadRecord, error) {
	return o.getThreadRecord(ctx, threadID)
}

// ListThreadTurns returns ordered turns for one persisted thread.
func (o *Orchestrator) ListThreadTurns(ctx context.Context, threadID string) ([]atypes.ThreadTurn, error) {
	if _, err := o.getThreadRecord(ctx, threadID); err != nil {
		return nil, err
	}
	return o.store.ListThreadTurns(ctx, threadID)
}

func threadLastRemote(turns []atypes.ThreadTurn, agentID string) (string, string) {
	for index := len(turns) - 1; index >= 0; index-- {
		if turns[index].TargetAgentID == agentID {
			return turns[index].RemoteSessionID, turns[index].RemoteExecutionID
		}
	}
	return "", ""
}

func deriveThreadInterruptionReason(thread atypes.ThreadRecord, lastTask atypes.TaskRecord) string {
	if thread.Status == atypes.ThreadStatusInterrupted {
		switch lastTask.Status {
		case atypes.TaskStatusAwaitingInput:
			return "restart interrupted the thread while the last task was awaiting input"
		case atypes.TaskStatusCreated, atypes.TaskStatusPendingLaunch, atypes.TaskStatusLaunching, atypes.TaskStatusReady, atypes.TaskStatusDispatching, atypes.TaskStatusRunning:
			return "restart interrupted the thread while the last task was still in progress"
		default:
			return "thread was interrupted before the prior turn reached a terminal state"
		}
	}
	if lastTask.Status == atypes.TaskStatusFailed && lastTask.LastError != nil && lastTask.LastError.Reason != "" {
		return fmt.Sprintf("last task failed: %s", lastTask.LastError.Reason)
	}
	return ""
}

// InspectThread bundles persisted turns and continuity state for operator debugging.
func (o *Orchestrator) InspectThread(ctx context.Context, threadID string) (atypes.ThreadInspection, error) {
	thread, err := o.getThreadRecord(ctx, threadID)
	if err != nil {
		return atypes.ThreadInspection{}, err
	}
	turns, err := o.store.ListThreadTurns(ctx, threadID)
	if err != nil {
		return atypes.ThreadInspection{}, err
	}
	bindings, err := o.store.ListSessionBindingsByStickyKey(ctx, threadID)
	if err != nil {
		return atypes.ThreadInspection{}, err
	}
	continuity := make([]atypes.ThreadContinuitySide, 0, 2)
	for _, agentID := range []string{thread.AgentAID, thread.AgentBID} {
		lastRemoteSessionID, lastRemoteExecutionID := threadLastRemote(turns, agentID)
		side := atypes.ThreadContinuitySide{
			AgentID:               agentID,
			LastRemoteSessionID:   lastRemoteSessionID,
			LastRemoteExecutionID: lastRemoteExecutionID,
			SessionBindings:       []atypes.SessionBinding{},
		}
		for _, binding := range bindings {
			if binding.TargetID == agentID {
				side.SessionBindings = append(side.SessionBindings, binding)
			}
		}
		continuity = append(continuity, side)
	}
	sender, target, err := deriveThreadContinuationPair(thread, "", "")
	if err != nil {
		return atypes.ThreadInspection{}, err
	}
	nextContinue := atypes.ThreadNextContinue{SenderAgentID: sender, TargetAgentID: target}
	for _, side := range continuity {
		if side.AgentID == target && len(side.SessionBindings) > 0 {
			nextContinue.WillReuseRemoteSession = true
			break
		}
	}
	var lastTask atypes.TaskRecord
	if thread.LastTaskID != "" {
		if loadedTask, err := o.GetTask(ctx, thread.LastTaskID); err == nil {
			lastTask = loadedTask
		}
	}
	return atypes.ThreadInspection{
		Thread:             thread,
		Turns:              turns,
		Continuity:         continuity,
		NextContinue:       nextContinue,
		InterruptionReason: deriveThreadInterruptionReason(thread, lastTask),
	}, nil
}

// ContinueThread creates the next thread-bound task under an existing pair boundary.
func (o *Orchestrator) ContinueThread(ctx context.Context, threadID string, req atypes.ThreadContinueRequest) (atypes.TaskRecord, error) {
	req.Normalize()
	thread, err := o.getThreadRecord(ctx, threadID)
	if err != nil {
		return atypes.TaskRecord{}, err
	}
	sender, target, err := deriveThreadContinuationPair(thread, req.Sender, req.TargetAgentID)
	if err != nil {
		return atypes.TaskRecord{}, err
	}
	task, err := o.CreateTask(ctx, atypes.TaskCreateRequest{
		ThreadID:       threadID,
		Sender:         sender,
		TargetAgentID:  target,
		Intent:         req.IntentForAgent(sender),
		Payload:        cloneMap(req.PayloadForAgent(sender)),
		RuntimeOptions: cloneMap(req.RuntimeOptions),
		ConversationID: req.ConversationID,
		Delivery:       req.Delivery,
		Metadata:       cloneMap(req.Metadata),
	})
	if err != nil {
		return atypes.TaskRecord{}, err
	}
	if req.AutoContinue && req.MaxTurns > 1 {
		go o.continueThreadAutoHandoff(threadID, req, task)
	}
	return task, nil
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
