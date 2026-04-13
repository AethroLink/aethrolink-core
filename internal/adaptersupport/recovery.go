package adaptersupport

import (
	"context"
	"strings"
	"time"

	"github.com/aethrolink/aethrolink-core/internal/runtime"
)

// RecoverAssistantTextFromSessionLoad replays a session through session/load and
// returns the last assistant message observed in the replay stream. Adapters use
// this when a prompt completed without yielding recoverable terminal text but the
// runtime can still replay session history over ACP updates.
func RecoverAssistantTextFromSessionLoad(ctx context.Context, worker *runtime.StdioWorker, sessionID string, loadParams map[string]any) string {
	if worker == nil || sessionID == "" {
		return ""
	}
	sub, cancel := worker.Subscribe()
	defer cancel()
	loadDone := make(chan struct{})
	var loadErr error
	go func() {
		defer close(loadDone)
		_, loadErr = worker.RequestWithTimeout("session/load", loadParams, 10*time.Second)
	}()

	currentRole := ""
	currentText := ""
	lastAssistant := ""
	flush := func() {
		if currentRole == "assistant" && strings.TrimSpace(currentText) != "" {
			lastAssistant = strings.TrimSpace(currentText)
		}
		currentRole = ""
		currentText = ""
	}

	loadFinished := false
	idle := time.NewTimer(12 * time.Second)
	defer idle.Stop()
	drain := time.NewTimer(0)
	if !drain.Stop() {
		<-drain.C
	}
	for {
		select {
		case <-ctx.Done():
			flush()
			return lastAssistant
		case <-idle.C:
			flush()
			return lastAssistant
		case <-loadDone:
			if loadErr != nil {
				flush()
				return lastAssistant
			}
			loadFinished = true
			drain.Reset(150 * time.Millisecond)
		case <-drain.C:
			flush()
			return lastAssistant
		case msg, ok := <-sub:
			if !ok {
				flush()
				return lastAssistant
			}
			params, _ := msg["params"].(map[string]any)
			if params == nil {
				continue
			}
			sid, _ := params["sessionId"].(string)
			if sid == "" {
				sid, _ = params["session_id"].(string)
			}
			if sid != sessionID {
				continue
			}
			update, _ := params["update"].(map[string]any)
			if update == nil {
				continue
			}
			updateType, _ := update["sessionUpdate"].(string)
			content, _ := update["content"].(map[string]any)
			text, _ := content["text"].(string)
			switch updateType {
			case "user_message_chunk":
				if currentRole != "user" {
					flush()
					currentRole = "user"
				}
				currentText += text
			case "agent_message_chunk":
				if currentRole != "assistant" {
					flush()
					currentRole = "assistant"
				}
				currentText += text
			}
			if loadFinished {
				if !drain.Stop() {
					select {
					case <-drain.C:
					default:
					}
				}
				drain.Reset(150 * time.Millisecond)
			}
		}
	}
}
