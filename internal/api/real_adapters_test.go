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
	atypes "github.com/aethrolink/aethrolink-core/pkg/types"
)

func setupRealAdapterServer(t *testing.T) (*httptest.Server, *core.Orchestrator) {
	t.Helper()
	root := filepath.Clean(filepath.Join("..", ".."))
	tmp := t.TempDir()
	store, err := storage.Open(filepath.Join(tmp, "aethrolink.db"), filepath.Join(tmp, "artifacts"), "http://127.0.0.1")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	runtimeManager := runtime.NewManager(store)
	agentService := agents.NewService(store)
	registerRealTestAgent(t, agentService, atypes.AgentRegistrationRequest{
		AgentID:       "hermes_real_test",
		DisplayName:   "hermes-real-test",
		TransportKind: "local_managed",
		Adapter:       "acp",
		Dialect:       "hermes",
		Launch:        atypes.LaunchSpec{Mode: atypes.LaunchModeManaged, Command: []string{"go", "run", root + "/cmd/fake-acp-client-agent"}},
		Defaults:      map[string]any{"executor": "coder"},
		Capabilities:  []string{"code.patch"},
	})
	registerRealTestAgent(t, agentService, atypes.AgentRegistrationRequest{
		AgentID:       "openclaw_real_test",
		DisplayName:   "openclaw-real-test",
		TransportKind: "local_managed",
		Adapter:       "acp",
		Dialect:       "openclaw",
		Launch:        atypes.LaunchSpec{Mode: atypes.LaunchModeManaged, Command: []string{"go", "run", root + "/cmd/fake-acp-client-agent"}},
		Defaults:      map[string]any{"session_key": "main"},
		Capabilities:  []string{"ui.review"},
	})
	adapterRegistry := adapters.NewRegistry()
	adapterRegistry.Register("acp", adapters.NewACPAdapter(agentService, runtimeManager))
	orchestrator := core.NewOrchestrator(agentService, store, runtimeManager, adapterRegistry)
	server := httptest.NewServer(NewServer(orchestrator, agentService))
	t.Cleanup(func() {
		_ = orchestrator.StopAllRuntimeProcesses(context.Background())
		server.Close()
		_ = store.Close()
	})
	return server, orchestrator
}

func registerRealTestAgent(t *testing.T, svc *agents.Service, req atypes.AgentRegistrationRequest) {
	t.Helper()
	if _, err := svc.Register(context.Background(), req); err != nil {
		t.Fatalf("register real test agent %s: %v", req.DisplayName, err)
	}
}

func createTaskForTest(t *testing.T, baseURL string, body string) map[string]any {
	t.Helper()
	resp, err := http.Post(baseURL+"/v1/tasks", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	defer resp.Body.Close()
	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode create task response: %v", err)
	}
	return payload
}

func fetchJSON(t *testing.T, url string) map[string]any {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("get %s: %v", url, err)
	}
	defer resp.Body.Close()
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode %s: %v", url, err)
	}
	return body
}

func waitForTaskStatus(t *testing.T, baseURL, taskID, expected string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		body := fetchJSON(t, baseURL+"/v1/tasks/"+taskID)
		if status, _ := body["status"].(string); status == expected {
			return body
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", expected)
	return nil
}

func readArtifactText(t *testing.T, baseURL string, task map[string]any) string {
	t.Helper()
	artifactID, _ := task["result_artifact_id"].(string)
	if artifactID == "" {
		t.Fatalf("missing result artifact id")
	}
	artifact := fetchJSON(t, baseURL+"/artifacts/"+artifactID)
	text, _ := artifact["text"].(string)
	if text == "" {
		t.Fatalf("expected text artifact body, got %#v", artifact)
	}
	return text
}

func nestedString(m map[string]any, keys ...string) string {
	current := any(m)
	for _, key := range keys {
		nextMap, ok := current.(map[string]any)
		if !ok {
			return ""
		}
		current = nextMap[key]
	}
	value, _ := current.(string)
	return value
}

func TestHermesStickySessionReusesConversationSession(t *testing.T) {
	server, orchestrator := setupRealAdapterServer(t)
	first := createTaskForTest(t, server.URL, `{"target_agent_id":"hermes_real_test","intent":"code.patch","conversation_id":"conv-sticky-1","payload":{"text":"Say exactly ALPHA"}}`)
	firstTaskID := nestedString(first, "task", "task_id")
	firstTask := waitForTaskStatus(t, server.URL, firstTaskID, "completed")
	if got := readArtifactText(t, server.URL, firstTask); got != "ALPHA" {
		t.Fatalf("expected ALPHA, got %q", got)
	}

	second := createTaskForTest(t, server.URL, `{"target_agent_id":"hermes_real_test","intent":"code.patch","conversation_id":"conv-sticky-1","payload":{"text":"What did I ask you to say exactly in the previous turn?"}}`)
	secondTaskID := nestedString(second, "task", "task_id")
	secondTask := waitForTaskStatus(t, server.URL, secondTaskID, "completed")
	if got := readArtifactText(t, server.URL, secondTask); got != "ALPHA" {
		t.Fatalf("expected sticky session answer ALPHA, got %q", got)
	}
	if nestedString(firstTask, "remote", "remote_session_id") != nestedString(secondTask, "remote", "remote_session_id") {
		t.Fatalf("expected remote session reuse, got %q vs %q", nestedString(firstTask, "remote", "remote_session_id"), nestedString(secondTask, "remote", "remote_session_id"))
	}
	_ = orchestrator.StopAllRuntimeProcesses(context.Background())
}

func TestOpenClawAdapterReusesSessionKey(t *testing.T) {
	server, orchestrator := setupRealAdapterServer(t)
	first := createTaskForTest(t, server.URL, `{"target_agent_id":"openclaw_real_test","intent":"ui.review","payload":{"mode":"success"},"runtime_options":{"session_key":"design-thread"}}`)
	firstTask := waitForTaskStatus(t, server.URL, nestedString(first, "task", "task_id"), "completed")
	second := createTaskForTest(t, server.URL, `{"target_agent_id":"openclaw_real_test","intent":"ui.review","payload":{"mode":"success"},"runtime_options":{"session_key":"design-thread"}}`)
	secondTask := waitForTaskStatus(t, server.URL, nestedString(second, "task", "task_id"), "completed")
	if nestedString(firstTask, "remote", "remote_session_id") != nestedString(secondTask, "remote", "remote_session_id") {
		t.Fatalf("expected openclaw session reuse, got %q vs %q", nestedString(firstTask, "remote", "remote_session_id"), nestedString(secondTask, "remote", "remote_session_id"))
	}
	_ = orchestrator.StopAllRuntimeProcesses(context.Background())
}

func TestOpenClawLongRunningTaskSurvivesHeartbeat(t *testing.T) {
	server, orchestrator := setupRealAdapterServer(t)
	created := createTaskForTest(t, server.URL, `{"target_agent_id":"openclaw_real_test","intent":"ui.review","payload":{"mode":"delayed_success","delay_ms":1200,"heartbeat_ms":100},"runtime_options":{"session_key":"slow-thread","session_idle_timeout_ms":5000}}`)
	task := waitForTaskStatus(t, server.URL, nestedString(created, "task", "task_id"), "completed")
	if got := readArtifactText(t, server.URL, task); got != "delayed_ok" {
		t.Fatalf("expected delayed_ok, got %q", got)
	}
	_ = orchestrator.StopAllRuntimeProcesses(context.Background())
}
