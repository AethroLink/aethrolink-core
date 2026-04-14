package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestRegisterWritesStateFile(t *testing.T) {
	var sawRegister bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/agents/register" {
			http.NotFound(w, r)
			return
		}
		sawRegister = true
		_ = json.NewEncoder(w).Encode(map[string]any{
			"agent": map[string]any{
				"agent_id":         "agent-123",
				"display_name":     "hermes-dev",
				"runtime_kind":     "hermes",
				"transport_kind":   "local_managed",
				"status":           "online",
				"registered_at":    "2026-04-14T00:00:00Z",
				"updated_at":       "2026-04-14T00:00:00Z",
				"last_seen_at":     "2026-04-14T00:00:00Z",
				"lease_expires_at": "2026-04-14T00:05:00Z",
			},
		})
	}))
	defer server.Close()

	statePath := filepath.Join(t.TempDir(), "agent.json")
	var out, errOut bytes.Buffer
	if err := run([]string{
		"register",
		"--server", server.URL,
		"--state-file", statePath,
		"--display-name", "hermes-dev",
		"--runtime-kind", "hermes",
	}, &out, &errOut); err != nil {
		t.Fatalf("run register: %v", err)
	}
	if !sawRegister {
		t.Fatalf("expected register endpoint to be called")
	}
	state, err := loadAgentState(statePath)
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	if state.AgentID != "agent-123" {
		t.Fatalf("expected saved agent id, got %q", state.AgentID)
	}
}

func TestCallUsesStateAgentIDAsSender(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "agent.json")
	if err := saveAgentState(statePath, agentState{AgentID: "agent-123"}); err != nil {
		t.Fatalf("save state: %v", err)
	}

	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/tasks" {
			http.NotFound(w, r)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"task": map[string]any{"task_id": "task-123"}})
	}))
	defer server.Close()

	var out, errOut bytes.Buffer
	if err := run([]string{
		"call",
		"--server", server.URL,
		"--state-file", statePath,
		"--target-runtime", "core",
		"--intent", "agent.runtime",
		"--text", "hello",
	}, &out, &errOut); err != nil {
		t.Fatalf("run call: %v", err)
	}
	if body["sender"] != "agent-123" {
		t.Fatalf("expected sender from state file, got %#v", body["sender"])
	}
	payload, _ := body["payload"].(map[string]any)
	if payload["text"] != "hello" {
		t.Fatalf("expected text payload, got %#v", payload)
	}
	if !strings.Contains(out.String(), "task-123") {
		t.Fatalf("expected task id in output, got %q", out.String())
	}
}
