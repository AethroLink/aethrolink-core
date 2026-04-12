package main

import (
	"encoding/json"
	"flag"
	"net/http"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"
)

type runState struct {
	RunID     string         `json:"run_id"`
	SessionID string         `json:"session_id"`
	Status    string         `json:"status"`
	Result    map[string]any `json:"result,omitempty"`
	Reason    string         `json:"reason,omitempty"`
	Mode      string         `json:"-"`
}

func main() {
	port := flag.String("port", "9102", "listen port")
	flag.Parse()
	var mu sync.Mutex
	runs := map[string]*runState{}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /ping", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	})
	mux.HandleFunc("POST /runs", func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		mode := "success"
		if messages, ok := body["messages"].([]any); ok && len(messages) > 1 {
			if second, ok := messages[1].(map[string]any); ok {
				if content, ok := second["content"].(string); ok {
					var parsed map[string]any
					if json.Unmarshal([]byte(content), &parsed) == nil {
						if m, ok := parsed["mode"].(string); ok && m != "" {
							mode = m
						}
					}
				}
			}
		}
		if mode == "submit_fail" {
			w.WriteHeader(http.StatusUnprocessableEntity)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "submit_failed"})
			return
		}
		runID := "run_" + ulid.Make().String()
		sessionID := "sess_" + ulid.Make().String()
		status := "running"
		if mode == "await_then_resume" {
			status = "awaiting_input"
		}
		mu.Lock()
		runs[runID] = &runState{RunID: runID, SessionID: sessionID, Status: status, Mode: mode}
		mu.Unlock()
		go func(runID, mode string) {
			switch mode {
			case "success":
				time.Sleep(150 * time.Millisecond)
				mu.Lock()
				if run := runs[runID]; run != nil {
					run.Status = "completed"
					run.Result = map[string]any{"text": "ok"}
				}
				mu.Unlock()
			case "launch_fail":
				mu.Lock()
				if run := runs[runID]; run != nil {
					run.Status = "failed"
					run.Reason = "launch_failed"
				}
				mu.Unlock()
			}
		}(runID, mode)
		_ = json.NewEncoder(w).Encode(map[string]any{"run_id": runID, "session_id": sessionID})
	})
	mux.HandleFunc("GET /runs/{run_id}", func(w http.ResponseWriter, r *http.Request) {
		runID := r.PathValue("run_id")
		mu.Lock()
		run := runs[runID]
		mu.Unlock()
		if run == nil {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "not_found", "status": "failed", "reason": "not_found"})
			return
		}
		_ = json.NewEncoder(w).Encode(run)
	})
	mux.HandleFunc("POST /runs/{run_id}/resume", func(w http.ResponseWriter, r *http.Request) {
		runID := r.PathValue("run_id")
		go func() {
			mu.Lock()
			if run := runs[runID]; run != nil {
				run.Status = "running"
			}
			mu.Unlock()
			time.Sleep(80 * time.Millisecond)
			mu.Lock()
			if run := runs[runID]; run != nil {
				run.Status = "completed"
				run.Result = map[string]any{"text": "resumed"}
			}
			mu.Unlock()
		}()
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	})
	mux.HandleFunc("POST /runs/{run_id}/cancel", func(w http.ResponseWriter, r *http.Request) {
		runID := r.PathValue("run_id")
		mu.Lock()
		if run := runs[runID]; run != nil {
			run.Status = "cancelled"
		}
		mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	})
	_ = http.ListenAndServe(":"+*port, mux)
}
