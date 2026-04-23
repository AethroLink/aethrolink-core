package adapters

import (
	"context"
	"fmt"
	"time"

	"github.com/aethrolink/aethrolink-core/internal/adaptersupport"
	atypes "github.com/aethrolink/aethrolink-core/pkg/types"
)

func (a *ACPAdapter) consumeRun(ctx context.Context, run *acpRun, sub <-chan map[string]any, cancel func(), task atypes.TaskEnvelope, lease atypes.RuntimeLease, dialect acpDialect, binding atypes.WorkspaceBinding, stickyKey, sessionID, promptText string, promptTimeout, idleTimeout time.Duration) {
	defer a.deleteRun(task.TaskID)
	defer cancel()
	defer close(run.events)
	defer close(run.errs)

	scope := adaptersupport.SessionScope(task.TargetAgentID, lease.SubcontextKey, stickyKey)
	trackerState := &acpRunTracker{}
	tracker := &acpNotificationTracker{run: trackerState}
	lastActivity := time.Now()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	promptResult := make(chan error, 1)
	promptFinished := false
	drainUntil := time.Time{}

	run.events <- runtimeEventToTaskEvent(task.TaskID, dialect.AcceptedEvent(task, sessionID))
	go func() {
		promptResult <- a.transport.Prompt(
			ctx,
			task.TargetAgentID,
			lease.SubcontextKey,
			sessionID,
			dialect.PromptPayload(sessionID, promptText, binding, task.RuntimeOptions),
			promptTimeout,
		)
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case err := <-promptResult:
			if err != nil {
				a.sessions.Release(scope, task.TaskID)
				run.errs <- err
				return
			}
			promptFinished = true
			drainUntil = time.Now().Add(500 * time.Millisecond)
		case <-ticker.C:
			if promptFinished && !drainUntil.IsZero() && time.Now().After(drainUntil) {
				finalText := adapterTrimText(trackerState.FinalText())
				if dialect.ShouldRecoverFinalText(finalText, trackerState.sawStructuredEvents) {
					finalText = a.recoverFinalText(context.Background(), task.TargetAgentID, lease.SubcontextKey, sessionID, binding, task.RuntimeOptions, dialect)
				}
				trackerState.SetFinalText(finalText)
				if syntheticCompletionDecision(dialect, trackerState) {
					run.events <- runtimeEventToTaskEvent(task.TaskID, dialect.CompletionEvent(task, finalText))
				}
				a.sessions.Release(scope, task.TaskID)
				return
			}
			if idleTimeout > 0 && time.Since(lastActivity) > idleTimeout {
				a.sessions.Release(scope, task.TaskID)
				run.errs <- fmt.Errorf("%s session idle timeout exceeded", dialect.Name())
				return
			}
		case msg, ok := <-sub:
			if !ok {
				return
			}
			params, _ := msg["params"].(map[string]any)
			if params == nil || !sessionMatches(params, sessionID) {
				continue
			}
			lastActivity = time.Now()
			if promptFinished {
				drainUntil = time.Now().Add(250 * time.Millisecond)
			}
			_ = a.sessions.TouchActivity(context.Background(), task.TargetAgentID, lease.SubcontextKey, stickyKey, lastActivity.UTC())
			events, terminal := dialect.HandleNotification(params, tracker)
			trackerState.Record(events)
			for _, event := range events {
				run.events <- runtimeEventToTaskEvent(task.TaskID, event)
			}
			if terminal {
				a.sessions.Release(scope, task.TaskID)
				return
			}
		}
	}
}

func (a *ACPAdapter) deleteRun(taskID string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.runs, taskID)
}

func (a *ACPAdapter) StreamEvents(ctx context.Context, handle atypes.RemoteHandle) (<-chan atypes.TaskEvent, <-chan error) {
	a.mu.Lock()
	run := a.runs[handle.TaskID]
	a.mu.Unlock()
	if run == nil {
		events := make(chan atypes.TaskEvent)
		errs := make(chan error, 1)
		close(events)
		errs <- fmt.Errorf("missing acp run")
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

func (a *ACPAdapter) recoverFinalText(ctx context.Context, targetID, subcontextKey, sessionID string, binding atypes.WorkspaceBinding, runtimeOptions map[string]any, dialect acpDialect) string {
	if sessionID == "" {
		return ""
	}
	return adaptersupport.RecoverAssistantTextFromSessionLoad(
		ctx,
		a.runtime.GetStdioWorker(targetID, subcontextKey),
		sessionID,
		dialect.LoadSessionPayload(sessionID, binding, runtimeOptions),
	)
}
