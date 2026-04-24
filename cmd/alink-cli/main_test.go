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
	var requestBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/agents/register" {
			http.NotFound(w, r)
			return
		}
		sawRegister = true
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			t.Fatalf("decode register request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"agent": map[string]any{
				"agent_id":         "agent-123",
				"display_name":     "hermes-dev",
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
		"--adapter", "acp",
		"--dialect", "hermes",
		"--launch-command", "hermes -p aethrolink-agent acp",
		"--defaults", "executor=aethrolink-agent",
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
	if requestBody["adapter"] != "acp" {
		t.Fatalf("expected adapter to be sent, got %#v", requestBody["adapter"])
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
		"--target-agent-id", "core",
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

func TestThreadCommandsUseThreadEndpoints(t *testing.T) {
	var createBody map[string]any
	var continueBody map[string]any
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		switch r.URL.Path {
		case "/v1/threads":
			if err := json.NewDecoder(r.Body).Decode(&createBody); err != nil {
				t.Fatalf("decode thread create request: %v", err)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"thread": map[string]any{"thread_id": "thread-123"}})
		case "/v1/threads/thread-123/continue":
			if err := json.NewDecoder(r.Body).Decode(&continueBody); err != nil {
				t.Fatalf("decode thread continue request: %v", err)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"task": map[string]any{"task_id": "task-123"}})
		case "/v1/threads/thread-123/inspect":
			_ = json.NewEncoder(w).Encode(map[string]any{"thread": map[string]any{"thread_id": "thread-123"}, "continuity": []map[string]any{{"agent_id": "core"}}})
		case "/v1/threads/thread-123/turns":
			_ = json.NewEncoder(w).Encode(map[string]any{"turns": []map[string]any{{"turn_index": 1}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	var out, errOut bytes.Buffer
	if err := run([]string{"thread-create", "--server", server.URL, "--agent-a-id", "core", "--agent-b-id", "openclaw_main"}, &out, &errOut); err != nil {
		t.Fatalf("run thread-create: %v", err)
	}
	if err := run([]string{"thread-continue", "--server", server.URL, "--thread-id", "thread-123", "--intent", "ui.review", "--text", "hello"}, &out, &errOut); err != nil {
		t.Fatalf("run thread-continue: %v", err)
	}
	if err := run([]string{"thread-get", "--server", server.URL, "--thread-id", "thread-123"}, &out, &errOut); err != nil {
		t.Fatalf("run thread-get: %v", err)
	}
	if err := run([]string{"thread-turns", "--server", server.URL, "--thread-id", "thread-123"}, &out, &errOut); err != nil {
		t.Fatalf("run thread-turns: %v", err)
	}
	if createBody["agent_a_id"] != "core" || createBody["agent_b_id"] != "openclaw_main" {
		t.Fatalf("expected thread-create body, got %#v", createBody)
	}
	if continueBody["intent"] != "ui.review" {
		t.Fatalf("expected thread-continue intent, got %#v", continueBody)
	}
	continuePayload, _ := continueBody["payload"].(map[string]any)
	if continuePayload["text"] != "hello" {
		t.Fatalf("expected thread-continue payload text, got %#v", continuePayload)
	}
	if len(paths) != 4 || paths[0] != "/v1/threads" || paths[1] != "/v1/threads/thread-123/continue" || paths[2] != "/v1/threads/thread-123/inspect" || paths[3] != "/v1/threads/thread-123/turns" {
		t.Fatalf("unexpected thread command paths: %#v", paths)
	}
}

func TestEnsureRegisteredReusesExistingAgentState(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "agent.json")
	if err := saveAgentState(statePath, agentState{AgentID: "agent-123"}); err != nil {
		t.Fatalf("save state: %v", err)
	}

	registerCalls := 0
	getCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/agents/agent-123":
			getCalls++
			_ = json.NewEncoder(w).Encode(map[string]any{
				"agent_id":         "agent-123",
				"display_name":     "hermes-dev",
				"transport_kind":   "local_managed",
				"status":           "online",
				"registered_at":    "2026-04-14T00:00:00Z",
				"updated_at":       "2026-04-14T00:00:00Z",
				"last_seen_at":     "2026-04-14T00:00:00Z",
				"lease_expires_at": "2026-04-14T00:05:00Z",
			})
		case "/v1/agents/register":
			registerCalls++
			http.Error(w, "should not register", http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	var out, errOut bytes.Buffer
	if err := run([]string{
		"ensure-registered",
		"--server", server.URL,
		"--state-file", statePath,
		"--display-name", "hermes-dev",
	}, &out, &errOut); err != nil {
		t.Fatalf("run ensure-registered: %v", err)
	}
	if getCalls != 1 {
		t.Fatalf("expected one lookup call, got %d", getCalls)
	}
	if registerCalls != 0 {
		t.Fatalf("expected no register calls, got %d", registerCalls)
	}
}

func TestCallHeartbeatRefreshesLeaseBeforeSubmit(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "agent.json")
	if err := saveAgentState(statePath, agentState{AgentID: "agent-123"}); err != nil {
		t.Fatalf("save state: %v", err)
	}

	var calls []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.URL.Path)
		switch r.URL.Path {
		case "/v1/agents/agent-123/heartbeat":
			_ = json.NewEncoder(w).Encode(map[string]any{"agent": map[string]any{"agent_id": "agent-123"}})
		case "/v1/tasks":
			_ = json.NewEncoder(w).Encode(map[string]any{"task": map[string]any{"task_id": "task-123"}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	var out, errOut bytes.Buffer
	if err := run([]string{
		"call",
		"--server", server.URL,
		"--state-file", statePath,
		"--target-agent-id", "core",
		"--intent", "agent.runtime",
		"--text", "hello",
		"--heartbeat",
	}, &out, &errOut); err != nil {
		t.Fatalf("run call with heartbeat: %v", err)
	}
	if len(calls) != 2 || calls[0] != "/v1/agents/agent-123/heartbeat" || calls[1] != "/v1/tasks" {
		t.Fatalf("expected heartbeat then task submit, got %#v", calls)
	}
}

func TestAgentsAndTargetsCommands(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		switch r.URL.Path {
		case "/v1/agents":
			_ = json.NewEncoder(w).Encode(map[string]any{"agents": []map[string]any{{"agent_id": "agent-123"}}})
		case "/v1/targets":
			_ = json.NewEncoder(w).Encode(map[string]any{"targets": []map[string]any{{"agent_id": "core"}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	var out, errOut bytes.Buffer
	if err := run([]string{"agents", "--server", server.URL}, &out, &errOut); err != nil {
		t.Fatalf("run agents: %v", err)
	}
	if err := run([]string{"targets", "--server", server.URL}, &out, &errOut); err != nil {
		t.Fatalf("run targets: %v", err)
	}
	if len(paths) != 2 || paths[0] != "/v1/agents" || paths[1] != "/v1/targets" {
		t.Fatalf("unexpected request paths: %#v", paths)
	}
}

func TestPeerCommandsUsePeerEndpoints(t *testing.T) {
	var paths []string
	var addBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		switch r.URL.Path {
		case "/v1/peers":
			if r.Method == http.MethodPost {
				if err := json.NewDecoder(r.Body).Decode(&addBody); err != nil {
					t.Fatalf("decode peer add body: %v", err)
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"peer": map[string]any{"peer_id": "peer-b", "base_url": "http://node-b"}})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"peers": []map[string]any{{"peer_id": "peer-b"}}})
		case "/v1/peers/peer-b/sync":
			_ = json.NewEncoder(w).Encode(map[string]any{"peer": map[string]any{"peer_id": "peer-b"}, "targets": []map[string]any{{"agent_id": "remote-coder"}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	var out, errOut bytes.Buffer
	if err := run([]string{"peer-add", "--server", server.URL, "--peer-id", "peer-b", "--display-name", "Node B", "--base-url", "http://node-b"}, &out, &errOut); err != nil {
		t.Fatalf("run peer-add: %v", err)
	}
	if err := run([]string{"peer-list", "--server", server.URL}, &out, &errOut); err != nil {
		t.Fatalf("run peer-list: %v", err)
	}
	if err := run([]string{"peer-sync", "--server", server.URL, "--peer-id", "peer-b"}, &out, &errOut); err != nil {
		t.Fatalf("run peer-sync: %v", err)
	}
	if addBody["peer_id"] != "peer-b" || addBody["base_url"] != "http://node-b" {
		t.Fatalf("unexpected peer-add body: %#v", addBody)
	}
	want := []string{"/v1/peers", "/v1/peers", "/v1/peers/peer-b/sync"}
	if len(paths) != len(want) {
		t.Fatalf("unexpected peer command paths: %#v", paths)
	}
	for i := range want {
		if paths[i] != want[i] {
			t.Fatalf("unexpected peer command paths: %#v", paths)
		}
	}
}
