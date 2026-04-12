package drivers

import (
	"fmt"

	"github.com/aethrolink/aethrolink-core/internal/runtime"
)

type ACPClientDriver struct {
	worker *runtime.StdioWorker
}

// NewACPClientDriver wraps the raw stdio JSON-RPC worker with ACP-shaped
// verbs so adapters do not have to construct method names and payloads by hand.
func NewACPClientDriver(worker *runtime.StdioWorker) *ACPClientDriver {
	return &ACPClientDriver{worker: worker}
}

func (d *ACPClientDriver) Initialize() error {
	_, err := d.worker.Request("initialize", map[string]any{})
	return err
}

func (d *ACPClientDriver) SessionNew(sessionID string) (string, error) {
	params := map[string]any{}
	if sessionID != "" {
		params["session_id"] = sessionID
	}
	result, err := d.worker.Request("session/new", params)
	if err != nil {
		return "", err
	}
	id, _ := result["session_id"].(string)
	if id == "" {
		return "", fmt.Errorf("missing session_id")
	}
	return id, nil
}

func (d *ACPClientDriver) SessionLoad(sessionID string) (string, error) {
	result, err := d.worker.Request("session/load", map[string]any{"session_id": sessionID})
	if err != nil {
		return "", err
	}
	id, _ := result["session_id"].(string)
	if id == "" {
		id = sessionID
	}
	return id, nil
}

func (d *ACPClientDriver) SessionPrompt(sessionID string, payload map[string]any) (map[string]any, error) {
	return d.worker.Request("session/prompt", map[string]any{"session_id": sessionID, "payload": payload})
}

func (d *ACPClientDriver) SessionResume(sessionID string, payload map[string]any) error {
	_, err := d.worker.Request("session/resume", map[string]any{"session_id": sessionID, "payload": payload})
	return err
}

func (d *ACPClientDriver) SessionCancel(sessionID string) error {
	_, err := d.worker.Request("session/cancel", map[string]any{"session_id": sessionID})
	return err
}

func (d *ACPClientDriver) EventStream(sessionID string) (<-chan map[string]any, func()) {
	sub, cancel := d.worker.Subscribe()
	out := make(chan map[string]any, 64)
	go func() {
		defer close(out)
		for msg := range sub {
			// The worker subscription is shared, so filter aggressively down to the
			// single session the adapter asked to observe.
			params, _ := msg["params"].(map[string]any)
			if params == nil {
				continue
			}
			if sid, _ := params["session_id"].(string); sid != sessionID {
				continue
			}
			event, _ := params["event"].(map[string]any)
			if event == nil {
				continue
			}
			out <- event
		}
	}()
	return out, cancel
}
