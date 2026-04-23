package core

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	atypes "github.com/aethrolink/aethrolink-core/pkg/types"
)

type threadSessionReusePredictor interface {
	PredictSessionReuse(ctx context.Context, task atypes.TaskEnvelope) (bool, error)
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

func (o *Orchestrator) predictThreadSessionReuse(ctx context.Context, thread atypes.ThreadRecord, sender, target string) bool {
	spec, err := o.discovery.ResolveRuntime(ctx, target)
	if err != nil {
		return false
	}
	adapter, ok := o.adapters.Get(spec.Adapter)
	if !ok {
		return false
	}
	predictor, ok := adapter.(threadSessionReusePredictor)
	if !ok {
		return false
	}
	willReuse, err := predictor.PredictSessionReuse(ctx, atypes.TaskEnvelope{
		TaskID:         atypes.NewID(),
		ThreadID:       thread.ThreadID,
		ConversationID: thread.ThreadID,
		Sender:         sender,
		TargetAgentID:  target,
		RuntimeOptions: map[string]any{},
	})
	if err != nil {
		return false
	}
	return willReuse
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
	nextContinue := atypes.ThreadNextContinue{
		SenderAgentID:          sender,
		TargetAgentID:          target,
		WillReuseRemoteSession: o.predictThreadSessionReuse(ctx, thread, sender, target),
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
