package main

import (
	"bufio"
	"encoding/json"
	"os"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"
)

type sessionState struct {
	Waiting bool
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
			sessionID, _ := params["session_id"].(string)
			if sessionID == "" {
				sessionID = ulid.Make().String()
			}
			sessionMu.Lock()
			sessions[sessionID] = &sessionState{}
			sessionMu.Unlock()
			_ = write(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{"session_id": sessionID}})
		case "session/load":
			sessionID, _ := params["session_id"].(string)
			sessionMu.Lock()
			if _, ok := sessions[sessionID]; !ok {
				sessions[sessionID] = &sessionState{}
			}
			sessionMu.Unlock()
			_ = write(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{"session_id": sessionID}})
		case "session/prompt":
			sessionID, _ := params["session_id"].(string)
			payload, _ := params["payload"].(map[string]any)
			mode := extractMode(payload)
			if mode == "submit_fail" {
				_ = write(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"message": "submit_failed"}})
				continue
			}
			sessionMu.Lock()
			sessions[sessionID] = &sessionState{Waiting: mode == "await_then_resume"}
			sessionMu.Unlock()
			remoteExecutionID := "run_" + ulid.Make().String()
			_ = write(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{"session_id": sessionID, "remote_execution_id": remoteExecutionID}})
			go func(sessionID, mode, remoteExecutionID string) {
				_ = write(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"session_id": sessionID, "event": map[string]any{"kind": "task.running", "message": "Runtime accepted the task", "data": map[string]any{"remote_execution_id": remoteExecutionID}}}})
				switch mode {
				case "success":
					time.Sleep(100 * time.Millisecond)
					_ = write(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"session_id": sessionID, "event": map[string]any{"kind": "task.completed", "message": "Task completed", "data": map[string]any{"result": map[string]any{"text": "ok"}}}}})
				case "await_then_resume":
					time.Sleep(100 * time.Millisecond)
					_ = write(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"session_id": sessionID, "event": map[string]any{"kind": "task.awaiting_input", "message": "Runtime requires additional input", "data": map[string]any{"prompt": "Approve?"}}}})
				case "crash_after_start":
					time.Sleep(100 * time.Millisecond)
					os.Exit(1)
				}
			}(sessionID, mode, remoteExecutionID)
		case "session/resume":
			sessionID, _ := params["session_id"].(string)
			sessionMu.Lock()
			if session, ok := sessions[sessionID]; ok {
				session.Waiting = false
			}
			sessionMu.Unlock()
			_ = write(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{"ok": true}})
			go func(sessionID string) {
				_ = write(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"session_id": sessionID, "event": map[string]any{"kind": "task.running", "message": "Task resumed", "data": map[string]any{}}}})
				time.Sleep(80 * time.Millisecond)
				_ = write(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"session_id": sessionID, "event": map[string]any{"kind": "task.completed", "message": "Task completed", "data": map[string]any{"result": map[string]any{"text": "resumed"}}}}})
			}(sessionID)
		case "session/cancel":
			sessionID, _ := params["session_id"].(string)
			_ = write(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{"ok": true}})
			go func(sessionID string) {
				_ = write(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"session_id": sessionID, "event": map[string]any{"kind": "task.cancelled", "message": "Task cancelled", "data": map[string]any{}}}})
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
