package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"
)

type sessionState struct {
	Waiting   bool
	Messages  []historyMessage
	LastExact string
}

// historyMessage stores the minimal replayable transcript needed for
// session/load based recovery tests across adapters.
type historyMessage struct {
	Role    string
	Content string
}

func main() {
	sessions := map[string]*sessionState{}
	var sessionMu sync.Mutex
	var writeMu sync.Mutex
	write := func(msg map[string]any) error {
		encoded, err := json.Marshal(msg)
		if err != nil {
			return err
		}
		writeMu.Lock()
		defer writeMu.Unlock()
		_, err = os.Stdout.Write(append(encoded, '\n'))
		return err
	}
	writeSessionResult := func(id any, sessionID string, remoteExecutionID string, hermesStyle bool) {
		result := map[string]any{"remote_execution_id": remoteExecutionID, "session_id": sessionID, "sessionId": sessionID}
		_ = write(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
	}
	emitACPEvent := func(sessionID string, event map[string]any) {
		_ = write(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"session_id": sessionID, "event": event}})
	}
	emitHermesChunk := func(sessionID, text string) {
		_ = write(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": sessionID, "update": map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"text": text}}}})
	}
	ensureSession := func(sessionID string) *sessionState {
		sessionMu.Lock()
		defer sessionMu.Unlock()
		if _, ok := sessions[sessionID]; !ok {
			sessions[sessionID] = &sessionState{}
		}
		return sessions[sessionID]
	}
	lookupSession := func(sessionID string) *sessionState {
		sessionMu.Lock()
		defer sessionMu.Unlock()
		return sessions[sessionID]
	}
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var msg map[string]any
		if err := json.Unmarshal(line, &msg); err != nil {
			continue
		}
		id := msg["id"]
		method, _ := msg["method"].(string)
		params, _ := msg["params"].(map[string]any)
		switch method {
		case "initialize":
			_ = write(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{}})
		case "session/new":
			sessionID := firstString(params, "session_id", "sessionId")
			if sessionID == "" {
				sessionID = ulid.Make().String()
			}
			_ = ensureSession(sessionID)
			_ = write(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{"session_id": sessionID, "sessionId": sessionID}})
		case "session/load":
			sessionID := firstString(params, "session_id", "sessionId")
			state := ensureSession(sessionID)
			for _, msg := range state.Messages {
				switch msg.Role {
				case "user":
					_ = write(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": sessionID, "update": map[string]any{"sessionUpdate": "user_message_chunk", "content": map[string]any{"text": msg.Content}}}})
				case "assistant":
					emitHermesChunk(sessionID, msg.Content)
				}
			}
			_ = write(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{"session_id": sessionID, "sessionId": sessionID}})
		case "session/prompt":
			hermesStyle := params["sessionId"] != nil
			sessionID := firstString(params, "session_id", "sessionId")
			remoteExecutionID := "run_" + ulid.Make().String()
			if hermesStyle {
				state := ensureSession(sessionID)
				promptText := extractHermesPromptText(params)
				state.Messages = append(state.Messages, historyMessage{Role: "user", Content: promptText})
				if exact := parseSayExactly(promptText); exact != "" {
					state.LastExact = exact
				}
				if payload := parsePromptPayload(promptText); payload != nil {
					mode := extractMode(payload)
					if mode == "delayed_success" {
						delay := durationFromPayload(payload, "delay_ms", 300*time.Millisecond)
						heartbeat := durationFromPayload(payload, "heartbeat_ms", 0)
						deadline := time.Now().Add(delay)
						if heartbeat > 0 {
							ticker := time.NewTicker(heartbeat)
							defer ticker.Stop()
							for time.Now().Before(deadline) {
								<-ticker.C
								emitACPEvent(sessionID, map[string]any{"kind": "task.running", "message": "Heartbeat", "data": map[string]any{"heartbeat": true}})
							}
						} else {
							time.Sleep(delay)
						}
						emitHermesChunk(sessionID, "delayed_ok")
						state.Messages = append(state.Messages, historyMessage{Role: "assistant", Content: "delayed_ok"})
						time.Sleep(20 * time.Millisecond)
						writeSessionResult(id, sessionID, remoteExecutionID, true)
						continue
					}
				}
				responseText := hermesResponse(state, promptText)
				time.Sleep(40 * time.Millisecond)
				emitHermesChunk(sessionID, responseText)
				state.Messages = append(state.Messages, historyMessage{Role: "assistant", Content: responseText})
				time.Sleep(20 * time.Millisecond)
				writeSessionResult(id, sessionID, remoteExecutionID, true)
				continue
			}

			payload, _ := params["payload"].(map[string]any)
			mode := extractMode(payload)
			if mode == "submit_fail" {
				_ = write(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"message": "submit_failed"}})
				continue
			}
			state := ensureSession(sessionID)
			if promptText := extractHermesPromptText(params); promptText != "" {
				state.Messages = append(state.Messages, historyMessage{Role: "user", Content: promptText})
			}
			state.Waiting = mode == "await_then_resume"
			writeSessionResult(id, sessionID, remoteExecutionID, false)
			go func(sessionID, mode, remoteExecutionID string, payload map[string]any) {
				emitACPEvent(sessionID, map[string]any{"kind": "task.running", "message": "Runtime accepted the task", "data": map[string]any{"remote_execution_id": remoteExecutionID}})
				switch mode {
				case "success":
					time.Sleep(100 * time.Millisecond)
					state.Messages = append(state.Messages, historyMessage{Role: "assistant", Content: "ok"})
					emitACPEvent(sessionID, map[string]any{"kind": "task.completed", "message": "Task completed", "data": map[string]any{"result": map[string]any{"text": "ok"}}})
				case "await_then_resume":
					time.Sleep(100 * time.Millisecond)
					emitACPEvent(sessionID, map[string]any{"kind": "task.awaiting_input", "message": "Runtime requires additional input", "data": map[string]any{"prompt": "Approve?"}})
				case "crash_after_start":
					time.Sleep(100 * time.Millisecond)
					os.Exit(1)
				case "delayed_success":
					delay := durationFromPayload(payload, "delay_ms", 300*time.Millisecond)
					heartbeat := durationFromPayload(payload, "heartbeat_ms", 0)
					deadline := time.Now().Add(delay)
					if heartbeat > 0 {
						ticker := time.NewTicker(heartbeat)
						defer ticker.Stop()
						for time.Now().Before(deadline) {
							<-ticker.C
							emitACPEvent(sessionID, map[string]any{"kind": "task.running", "message": "Heartbeat", "data": map[string]any{"heartbeat": true}})
						}
					} else {
						time.Sleep(delay)
					}
					state.Messages = append(state.Messages, historyMessage{Role: "assistant", Content: "delayed_ok"})
					emitACPEvent(sessionID, map[string]any{"kind": "task.completed", "message": "Task completed", "data": map[string]any{"result": map[string]any{"text": "delayed_ok"}}})
				}
			}(sessionID, mode, remoteExecutionID, payload)
		case "session/resume":
			sessionID := firstString(params, "session_id", "sessionId")
			if session := lookupSession(sessionID); session != nil {
				session.Waiting = false
			}
			_ = write(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{"ok": true}})
			go func(sessionID string) {
				emitACPEvent(sessionID, map[string]any{"kind": "task.running", "message": "Task resumed", "data": map[string]any{}})
				time.Sleep(80 * time.Millisecond)
				if session := lookupSession(sessionID); session != nil {
					session.Messages = append(session.Messages, historyMessage{Role: "assistant", Content: "resumed"})
				}
				emitACPEvent(sessionID, map[string]any{"kind": "task.completed", "message": "Task completed", "data": map[string]any{"result": map[string]any{"text": "resumed"}}})
			}(sessionID)
		case "session/cancel":
			sessionID := firstString(params, "session_id", "sessionId")
			_ = write(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{"ok": true}})
			go func(sessionID string) {
				emitACPEvent(sessionID, map[string]any{"kind": "task.cancelled", "message": "Task cancelled", "data": map[string]any{}})
			}(sessionID)
		default:
			_ = write(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"message": "method_not_found"}})
		}
	}
}

func extractMode(payload map[string]any) string {
	if payload == nil {
		return "success"
	}
	if inner, ok := payload["payload"].(map[string]any); ok {
		if mode, ok := inner["mode"].(string); ok && mode != "" {
			return mode
		}
	}
	if mode, ok := payload["mode"].(string); ok && mode != "" {
		return mode
	}
	return "success"
}

func durationFromPayload(payload map[string]any, key string, fallback time.Duration) time.Duration {
	if payload == nil {
		return fallback
	}
	if value, ok := payload[key]; ok {
		switch typed := value.(type) {
		case int:
			return time.Duration(typed) * time.Millisecond
		case int64:
			return time.Duration(typed) * time.Millisecond
		case float64:
			return time.Duration(typed) * time.Millisecond
		}
	}
	return fallback
}

func firstString(m map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := m[key].(string); ok && value != "" {
			return value
		}
	}
	return ""
}

// extractHermesPromptText pulls the first text item from a prompt array so the
// fake runtime can store replayable user history without adapter-specific logic.
func extractHermesPromptText(params map[string]any) string {
	promptItems, _ := params["prompt"].([]any)
	if len(promptItems) == 0 {
		if maps, ok := params["prompt"].([]map[string]any); ok {
			for _, item := range maps {
				if text, ok := item["text"].(string); ok && text != "" {
					return text
				}
			}
		}
		return ""
	}
	for _, item := range promptItems {
		entry, _ := item.(map[string]any)
		if text, ok := entry["text"].(string); ok && text != "" {
			return text
		}
	}
	return ""
}

// parseSayExactly recognizes the deterministic prompts used by contract and
// live-validation tests.
func parseSayExactly(promptText string) string {
	re := regexp.MustCompile(`(?i)^say exactly\s+(.+)$`)
	matches := re.FindStringSubmatch(strings.TrimSpace(promptText))
	if len(matches) != 2 {
		return ""
	}
	return strings.Trim(matches[1], " .\"'")
}

// hermesResponse returns deterministic assistant text so tests can assert exact
// sticky-session and recovery behavior without using a real model.
func hermesResponse(state *sessionState, promptText string) string {
	if exact := parseSayExactly(promptText); exact != "" {
		return exact
	}
	if strings.Contains(strings.ToLower(promptText), "what did i ask you to say exactly") {
		if state.LastExact != "" {
			return state.LastExact
		}
		return "UNKNOWN"
	}
	return fmt.Sprintf("echo: %s", strings.TrimSpace(promptText))
}

// parsePromptPayload decodes JSON prompt payloads used by delayed-success and
// heartbeat tests.
func parsePromptPayload(promptText string) map[string]any {
	promptText = strings.TrimSpace(promptText)
	if promptText == "" || !strings.HasPrefix(promptText, "{") {
		return nil
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(promptText), &payload); err != nil {
		return nil
	}
	return payload
}
