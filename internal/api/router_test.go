package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aethrolink/aethrolink-core/internal/adapters"
	"github.com/aethrolink/aethrolink-core/internal/agents"
	"github.com/aethrolink/aethrolink-core/internal/core"
	"github.com/aethrolink/aethrolink-core/internal/runtime"
	"github.com/aethrolink/aethrolink-core/internal/storage"
	"github.com/aethrolink/aethrolink-core/internal/testsupport/mockadapters"
	atypes "github.com/aethrolink/aethrolink-core/pkg/types"
)

func setupServer(t *testing.T) (*httptest.Server, *core.Orchestrator) {
	t.Helper()
	root := filepath.Clean(filepath.Join("..", ".."))
	tmp := t.TempDir()
	store, err := storage.Open(filepath.Join(tmp, "aethrolink.db"), filepath.Join(tmp, "artifacts"), "http://127.0.0.1")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	runtimeManager := runtime.NewManager(store)
	agentService := agents.NewService(store)
	registerTestAgent(t, agentService, atypes.AgentRegistrationRequest{
		DisplayName:   "mock-hermes",
		RuntimeKind:   "hermes",
		TransportKind: "local_managed",
		RuntimeID:     "mock_hermes",
		Adapter:       "mock_hermes",
		Launch: atypes.LaunchSpec{Mode: atypes.LaunchModeManaged, Commands: map[string][]string{
			"coder":    {"go", "run", root + "/cmd/fake-acp-client-agent"},
			"research": {"go", "run", root + "/cmd/fake-acp-client-agent"},
			"ops":      {"go", "run", root + "/cmd/fake-acp-client-agent"},
		}},
		Defaults:     map[string]any{"executor": "coder"},
		Capabilities: []string{"code.patch", "research.topic"},
	})
	registerTestAgent(t, agentService, atypes.AgentRegistrationRequest{
		DisplayName:   "mock-openclaw",
		RuntimeKind:   "openclaw",
		TransportKind: "local_managed",
		RuntimeID:     "mock_openclaw",
		Adapter:       "mock_openclaw",
		Launch:        atypes.LaunchSpec{Mode: atypes.LaunchModeOnDemand, Command: []string{"go", "run", root + "/cmd/fake-acp-client-agent"}},
		Defaults:      map[string]any{"session_key": "main"},
		Capabilities:  []string{"ui.review"},
	})
	registerTestAgent(t, agentService, atypes.AgentRegistrationRequest{
		DisplayName:   "mock-http",
		RuntimeKind:   "http_acp",
		TransportKind: "local_managed",
		RuntimeID:     "mock_researcher_http",
		Adapter:       "mock_acp_comm_http",
		Endpoint:      "http://127.0.0.1:19102",
		Healthcheck:   "http://127.0.0.1:19102/ping",
		Launch:        atypes.LaunchSpec{Mode: atypes.LaunchModeManaged, Command: []string{"go", "run", root + "/cmd/fake-acp-comm-agent", "--port", "19102"}},
		Capabilities:  []string{"summarize"},
	})
	adapterRegistry := adapters.NewRegistry()
	mockadapters.RegisterAll(adapterRegistry, agentService, runtimeManager)
	orchestrator := core.NewOrchestrator(agentService, store, runtimeManager, adapterRegistry)
	server := httptest.NewServer(NewServer(orchestrator, agentService))
	t.Cleanup(func() {
		_ = orchestrator.StopAllRuntimeProcesses(context.Background())
		server.Close()
		_ = store.Close()
	})
	return server, orchestrator
}

func registerTestAgent(t *testing.T, svc *agents.Service, req atypes.AgentRegistrationRequest) {
	t.Helper()
	if _, err := svc.Register(context.Background(), req); err != nil {
		t.Fatalf("register test agent %s: %v", req.DisplayName, err)
	}
}

func waitForStatus(t *testing.T, baseURL, taskID, expected string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(baseURL + "/v1/tasks/" + taskID)
		if err == nil {
			var task map[string]any
			if json.NewDecoder(resp.Body).Decode(&task) == nil {
				_ = resp.Body.Close()
				if status, _ := task["status"].(string); status == expected {
					return task
				}
			} else {
				_ = resp.Body.Close()
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", expected)
	return nil
}

func TestHermesTaskCompletes(t *testing.T) {
	server, orchestrator := setupServer(t)
	payload := `{"intent":"code.patch","payload":{"mode":"success"}}`
	resp, err := http.Post(server.URL+"/v1/tasks", "application/json", strings.NewReader(payload))
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	defer resp.Body.Close()
	var created struct {
		Task struct {
			TaskID string `json:"task_id"`
		} `json:"task"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	task := waitForStatus(t, server.URL, created.Task.TaskID, "completed")
	if task["result_artifact_id"] == "" {
		t.Fatalf("expected result artifact id")
	}
	_ = orchestrator.StopAllRuntimeProcesses(context.Background())
}

func TestOpenClawResumeFlow(t *testing.T) {
	server, orchestrator := setupServer(t)
	payload := `{"intent":"ui.review","payload":{"mode":"await_then_resume"}}`
	resp, err := http.Post(server.URL+"/v1/tasks", "application/json", strings.NewReader(payload))
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	defer resp.Body.Close()
	var created struct {
		Task struct {
			TaskID string `json:"task_id"`
		} `json:"task"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	_ = waitForStatus(t, server.URL, created.Task.TaskID, "awaiting_input")
	resumeResp, err := http.Post(server.URL+"/v1/tasks/"+created.Task.TaskID+"/resume", "application/json", strings.NewReader(`{"payload":{"approved":true}}`))
	if err != nil {
		t.Fatalf("resume task: %v", err)
	}
	_ = resumeResp.Body.Close()
	_ = waitForStatus(t, server.URL, created.Task.TaskID, "completed")
	_ = orchestrator.StopAllRuntimeProcesses(context.Background())
}

func TestHTTPAdapterCancelFlow(t *testing.T) {
	server, orchestrator := setupServer(t)
	payload := `{"intent":"summarize","payload":{"mode":"await_then_resume"}}`
	resp, err := http.Post(server.URL+"/v1/tasks", "application/json", strings.NewReader(payload))
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	defer resp.Body.Close()
	var created struct {
		Task struct {
			TaskID string `json:"task_id"`
		} `json:"task"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	_ = waitForStatus(t, server.URL, created.Task.TaskID, "awaiting_input")
	cancelReq, _ := http.NewRequest(http.MethodPost, server.URL+"/v1/tasks/"+created.Task.TaskID+"/cancel", nil)
	cancelResp, err := http.DefaultClient.Do(cancelReq)
	if err != nil {
		t.Fatalf("cancel task: %v", err)
	}
	_ = cancelResp.Body.Close()
	_ = waitForStatus(t, server.URL, created.Task.TaskID, "cancelled")
	_ = orchestrator.StopAllRuntimeProcesses(context.Background())
}

func TestAgentRegistrationHeartbeatAndList(t *testing.T) {
	server, orchestrator := setupServer(t)
	defer func() { _ = orchestrator.StopAllRuntimeProcesses(context.Background()) }()

	registerBody := `{"display_name":"hermes-dev","runtime_kind":"hermes","transport_kind":"local_managed","runtime_id":"mock_hermes","capabilities":["code.patch"],"sticky_mode":"conversation"}`
	resp, err := http.Post(server.URL+"/v1/agents/register", "application/json", strings.NewReader(registerBody))
	if err != nil {
		t.Fatalf("register agent: %v", err)
	}
	defer resp.Body.Close()
	var created map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatalf("decode register response: %v", err)
	}
	agent, _ := created["agent"].(map[string]any)
	agentID, _ := agent["agent_id"].(string)
	if agentID == "" {
		t.Fatalf("expected agent id in registration response")
	}

	getResp, err := http.Get(server.URL + "/v1/agents/" + agentID)
	if err != nil {
		t.Fatalf("get agent: %v", err)
	}
	defer getResp.Body.Close()
	var loaded map[string]any
	if err := json.NewDecoder(getResp.Body).Decode(&loaded); err != nil {
		t.Fatalf("decode get agent response: %v", err)
	}
	if loaded["display_name"] != "hermes-dev" {
		t.Fatalf("expected display name hermes-dev, got %#v", loaded["display_name"])
	}

	heartbeatResp, err := http.Post(server.URL+"/v1/agents/"+agentID+"/heartbeat", "application/json", strings.NewReader(`{"lease_ttl_seconds":120}`))
	if err != nil {
		t.Fatalf("heartbeat agent: %v", err)
	}
	_ = heartbeatResp.Body.Close()

	listResp, err := http.Get(server.URL + "/v1/agents")
	if err != nil {
		t.Fatalf("list agents: %v", err)
	}
	defer listResp.Body.Close()
	var listed struct {
		Agents []map[string]any `json:"agents"`
	}
	if err := json.NewDecoder(listResp.Body).Decode(&listed); err != nil {
		t.Fatalf("decode list agents response: %v", err)
	}
	if len(listed.Agents) < 4 {
		t.Fatalf("expected registered agent plus built-in test agents, got %d", len(listed.Agents))
	}
	found := false
	for _, listedAgent := range listed.Agents {
		if listedAgent["agent_id"] == agentID {
			found = true
			if listedAgent["status"] != string("online") {
				t.Fatalf("expected online agent status, got %#v", listedAgent["status"])
			}
		}
	}
	if !found {
		t.Fatalf("expected newly registered agent to appear in list")
	}
}
