package adapters

import (
	"context"
	"fmt"
	"time"

	"github.com/aethrolink/aethrolink-core/internal/runtime"
	atypes "github.com/aethrolink/aethrolink-core/pkg/types"
)

type acpSessionTransport struct {
	manager *runtime.Manager
}

func newACPSessionTransport(manager *runtime.Manager) atypes.LocalSessionTransport {
	return &acpSessionTransport{manager: manager}
}

func (t *acpSessionTransport) Initialize(ctx context.Context, runtimeID, subcontextKey string, payload map[string]any, timeout time.Duration) error {
	worker, err := t.worker(runtimeID, subcontextKey)
	if err != nil {
		return err
	}
	_, err = worker.RequestWithTimeout("initialize", payload, timeout)
	return err
}

func (t *acpSessionTransport) OpenSession(ctx context.Context, runtimeID, subcontextKey string, payload map[string]any, timeout time.Duration) (string, error) {
	worker, err := t.worker(runtimeID, subcontextKey)
	if err != nil {
		return "", err
	}
	result, err := worker.RequestWithTimeout("session/new", payload, timeout)
	if err != nil {
		return "", err
	}
	return sessionIDFromResult(result)
}

func (t *acpSessionTransport) LoadSession(ctx context.Context, runtimeID, subcontextKey string, sessionID string, payload map[string]any, timeout time.Duration) (string, error) {
	worker, err := t.worker(runtimeID, subcontextKey)
	if err != nil {
		return "", err
	}
	result, err := worker.RequestWithTimeout("session/load", payload, timeout)
	if err != nil {
		return "", err
	}
	resolved, err := sessionIDFromResult(result)
	if err != nil {
		return sessionID, nil
	}
	return resolved, nil
}

func (t *acpSessionTransport) Prompt(ctx context.Context, runtimeID, subcontextKey string, sessionID string, payload map[string]any, timeout time.Duration) error {
	worker, err := t.worker(runtimeID, subcontextKey)
	if err != nil {
		return err
	}
	_, err = worker.RequestWithTimeout("session/prompt", payload, timeout)
	return err
}

func (t *acpSessionTransport) Resume(ctx context.Context, runtimeID, subcontextKey string, sessionID string, payload map[string]any) error {
	worker, err := t.worker(runtimeID, subcontextKey)
	if err != nil {
		return err
	}
	_, err = worker.RequestWithTimeout("session/resume", payload, 20*time.Second)
	return err
}

func (t *acpSessionTransport) Cancel(ctx context.Context, runtimeID, subcontextKey string, sessionID string) error {
	worker, err := t.worker(runtimeID, subcontextKey)
	if err != nil {
		return err
	}
	_, err = worker.RequestWithTimeout("session/cancel", map[string]any{"sessionId": sessionID}, 20*time.Second)
	return err
}

func (t *acpSessionTransport) Stream(ctx context.Context, runtimeID, subcontextKey string) (<-chan map[string]any, func(), error) {
	worker, err := t.worker(runtimeID, subcontextKey)
	if err != nil {
		return nil, nil, err
	}
	ch, cancel := worker.Subscribe()
	return ch, cancel, nil
}

func (t *acpSessionTransport) worker(runtimeID, subcontextKey string) (*runtime.StdioWorker, error) {
	worker := t.manager.GetStdioWorker(runtimeID, subcontextKey)
	if worker == nil {
		return nil, fmt.Errorf("missing stdio worker")
	}
	return worker, nil
}

func sessionIDFromResult(result map[string]any) (string, error) {
	sessionID, _ := result["sessionId"].(string)
	if sessionID == "" {
		sessionID, _ = result["session_id"].(string)
	}
	if sessionID == "" {
		return "", fmt.Errorf("missing sessionId")
	}
	return sessionID, nil
}
