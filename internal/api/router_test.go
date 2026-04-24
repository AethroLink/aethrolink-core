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
	server, orchestrator, cleanup := setupServerAtRoot(t, t.TempDir())
	t.Cleanup(cleanup)
	return server, orchestrator
}

func setupServerAtRoot(t *testing.T, tmp string) (*httptest.Server, *core.Orchestrator, func()) {
	t.Helper()
	root := filepath.Clean(filepath.Join("..", ".."))
	store, err := storage.Open(filepath.Join(tmp, "aethrolink.db"), filepath.Join(tmp, "artifacts"), "http://127.0.0.1")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	runtimeManager := runtime.NewManager(store)
	agentService := agents.NewService(store)
	registerTestAgent(t, agentService, atypes.AgentRegistrationRequest{
		AgentID:       "mock_hermes",
		DisplayName:   "mock-hermes",
		TransportKind: "local_managed",
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
		AgentID:       "mock_openclaw",
		DisplayName:   "mock-openclaw",
		TransportKind: "local_managed",
		Adapter:       "mock_openclaw",
		Launch:        atypes.LaunchSpec{Mode: atypes.LaunchModeOnDemand, Command: []string{"go", "run", root + "/cmd/fake-acp-client-agent"}},
		Defaults:      map[string]any{"session_key": "main"},
		Capabilities:  []string{"ui.review"},
	})
	registerTestAgent(t, agentService, atypes.AgentRegistrationRequest{
		AgentID:       "mock_researcher_http",
		DisplayName:   "mock-http",
		TransportKind: "local_managed",
		Adapter:       "mock_acp_comm_http",
		Endpoint:      "http://127.0.0.1:19102",
		Healthcheck:   "http://127.0.0.1:19102/ping",
		Launch:        atypes.LaunchSpec{Mode: atypes.LaunchModeManaged, Command: []string{"go", "run", root + "/cmd/fake-acp-comm-agent", "--port", "19102"}},
		Capabilities:  []string{"summarize"},
	})
	adapterRegistry := adapters.NewRegistry()
	mockadapters.RegisterAll(adapterRegistry, agentService, runtimeManager)
	orchestrator, err := core.NewOrchestrator(agentService, store, runtimeManager, adapterRegistry)
	if err != nil {
		_ = store.Close()
		t.Fatalf("create orchestrator: %v", err)
	}
	server := httptest.NewServer(NewServer(orchestrator, agentService))
	cleanup := func() {
		_ = orchestrator.StopAllRuntimeProcesses(context.Background())
		server.Close()
		_ = store.Close()
	}
	return server, orchestrator, cleanup
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
	var last map[string]any
	for time.Now().Before(deadline) {
		resp, err := http.Get(baseURL + "/v1/tasks/" + taskID)
		if err == nil {
			var task map[string]any
			if json.NewDecoder(resp.Body).Decode(&task) == nil {
				_ = resp.Body.Close()
				last = task
				if status, _ := task["status"].(string); status == expected {
					return task
				}
			} else {
				_ = resp.Body.Close()
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s; last task=%+v", expected, last)
	return nil
}

func waitForThreadTurns(t *testing.T, baseURL, threadID string, expected int, statuses ...string) []map[string]any {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(baseURL + "/v1/threads/" + threadID + "/turns")
		if err == nil {
			var body struct {
				Turns []map[string]any `json:"turns"`
			}
			if json.NewDecoder(resp.Body).Decode(&body) == nil {
				_ = resp.Body.Close()
				if len(body.Turns) == expected {
					allMatched := true
					for index, status := range statuses {
						if index >= len(body.Turns) || body.Turns[index]["status"] != status {
							allMatched = false
							break
						}
					}
					if allMatched {
						return body.Turns
					}
				}
			} else {
				_ = resp.Body.Close()
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d thread turns", expected)
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

	registerBody := `{"display_name":"hermes-dev","transport_kind":"local_managed","capabilities":["code.patch"],"sticky_mode":"conversation"}`
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

func TestOfflineTargetStillAppearsInTargetsAndCanLaunchOnDemand(t *testing.T) {
	server, orchestrator := setupServer(t)
	defer func() { _ = orchestrator.StopAllRuntimeProcesses(context.Background()) }()

	unregisterReq, err := http.NewRequest(http.MethodPost, server.URL+"/v1/agents/mock_openclaw/unregister", nil)
	if err != nil {
		t.Fatalf("build unregister request: %v", err)
	}
	unregisterResp, err := http.DefaultClient.Do(unregisterReq)
	if err != nil {
		t.Fatalf("unregister target: %v", err)
	}
	_ = unregisterResp.Body.Close()

	targetsResp, err := http.Get(server.URL + "/v1/targets")
	if err != nil {
		t.Fatalf("list targets: %v", err)
	}
	defer targetsResp.Body.Close()
	var listed struct {
		Targets []map[string]any `json:"targets"`
	}
	if err := json.NewDecoder(targetsResp.Body).Decode(&listed); err != nil {
		t.Fatalf("decode targets response: %v", err)
	}
	found := false
	for _, target := range listed.Targets {
		if target["target_id"] == "mock_openclaw" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected offline target to remain visible in targets list")
	}

	payload := `{"target_agent_id":"mock_openclaw","intent":"ui.review","payload":{"mode":"success"}}`
	createResp, err := http.Post(server.URL+"/v1/tasks", "application/json", strings.NewReader(payload))
	if err != nil {
		t.Fatalf("create task for offline target: %v", err)
	}
	defer createResp.Body.Close()
	if createResp.StatusCode != http.StatusAccepted {
		var body map[string]any
		_ = json.NewDecoder(createResp.Body).Decode(&body)
		t.Fatalf("expected accepted create response, got %d with body %#v", createResp.StatusCode, body)
	}
	var created struct {
		Task struct {
			TaskID string `json:"task_id"`
		} `json:"task"`
	}
	if err := json.NewDecoder(createResp.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if created.Task.TaskID == "" {
		t.Fatalf("expected task id for offline target launch path")
	}
	_ = waitForStatus(t, server.URL, created.Task.TaskID, "completed")
}

func TestThreadCreateContinueAndListTurns(t *testing.T) {
	server, orchestrator := setupServer(t)
	defer func() { _ = orchestrator.StopAllRuntimeProcesses(context.Background()) }()

	createResp, err := http.Post(server.URL+"/v1/threads", "application/json", strings.NewReader(`{"agent_a_id":"mock_hermes","agent_b_id":"mock_openclaw","metadata":{"purpose":"review-loop"}}`))
	if err != nil {
		t.Fatalf("create thread: %v", err)
	}
	defer createResp.Body.Close()
	if createResp.StatusCode != http.StatusCreated {
		var body map[string]any
		_ = json.NewDecoder(createResp.Body).Decode(&body)
		t.Fatalf("expected created thread response, got %d with body %#v", createResp.StatusCode, body)
	}
	var created struct {
		Thread struct {
			ThreadID string `json:"thread_id"`
		} `json:"thread"`
	}
	if err := json.NewDecoder(createResp.Body).Decode(&created); err != nil {
		t.Fatalf("decode thread create response: %v", err)
	}
	if created.Thread.ThreadID == "" {
		t.Fatalf("expected thread id in create response")
	}

	firstContinueResp, err := http.Post(server.URL+"/v1/threads/"+created.Thread.ThreadID+"/continue", "application/json", strings.NewReader(`{"sender":"mock_hermes","intent":"ui.review","payload":{"mode":"success"}}`))
	if err != nil {
		t.Fatalf("continue thread first turn: %v", err)
	}
	defer firstContinueResp.Body.Close()
	var firstTask struct {
		Task struct {
			TaskID string `json:"task_id"`
		} `json:"task"`
	}
	if err := json.NewDecoder(firstContinueResp.Body).Decode(&firstTask); err != nil {
		t.Fatalf("decode first continue response: %v", err)
	}
	if firstTask.Task.TaskID == "" {
		t.Fatalf("expected first continue task id")
	}

	secondContinueResp, err := http.Post(server.URL+"/v1/threads/"+created.Thread.ThreadID+"/continue", "application/json", strings.NewReader(`{"intent":"code.patch","payload":{"mode":"success"}}`))
	if err != nil {
		t.Fatalf("continue thread second turn: %v", err)
	}
	defer secondContinueResp.Body.Close()
	var secondTask struct {
		Task struct {
			TaskID string `json:"task_id"`
		} `json:"task"`
	}
	if err := json.NewDecoder(secondContinueResp.Body).Decode(&secondTask); err != nil {
		t.Fatalf("decode second continue response: %v", err)
	}
	if secondTask.Task.TaskID == "" {
		t.Fatalf("expected second continue task id")
	}

	thirdContinueResp, err := http.Post(server.URL+"/v1/threads/"+created.Thread.ThreadID+"/continue", "application/json", strings.NewReader(`{"intent":"ui.review","payload":{"mode":"success"}}`))
	if err != nil {
		t.Fatalf("continue thread third turn: %v", err)
	}
	defer thirdContinueResp.Body.Close()
	var thirdTask struct {
		Task struct {
			TaskID string `json:"task_id"`
		} `json:"task"`
	}
	if err := json.NewDecoder(thirdContinueResp.Body).Decode(&thirdTask); err != nil {
		t.Fatalf("decode third continue response: %v", err)
	}
	if thirdTask.Task.TaskID == "" {
		t.Fatalf("expected third continue task id")
	}

	firstLoaded := waitForStatus(t, server.URL, firstTask.Task.TaskID, "completed")
	secondLoaded := waitForStatus(t, server.URL, secondTask.Task.TaskID, "completed")
	thirdLoaded := waitForStatus(t, server.URL, thirdTask.Task.TaskID, "completed")
	firstConversationID, _ := firstLoaded["conversation_id"].(string)
	secondConversationID, _ := secondLoaded["conversation_id"].(string)
	thirdConversationID, _ := thirdLoaded["conversation_id"].(string)
	if firstConversationID == "" || secondConversationID == "" || thirdConversationID == "" {
		t.Fatalf("expected thread tasks to persist conversation ids, got %q, %q, %q", firstConversationID, secondConversationID, thirdConversationID)
	}
	if firstConversationID != secondConversationID || secondConversationID != thirdConversationID {
		t.Fatalf("expected thread continuation to preserve conversation id, got %q, %q, %q", firstConversationID, secondConversationID, thirdConversationID)
	}

	getThreadResp, err := http.Get(server.URL + "/v1/threads/" + created.Thread.ThreadID)
	if err != nil {
		t.Fatalf("get thread: %v", err)
	}
	defer getThreadResp.Body.Close()
	var loadedThread map[string]any
	if err := json.NewDecoder(getThreadResp.Body).Decode(&loadedThread); err != nil {
		t.Fatalf("decode thread get response: %v", err)
	}
	if loadedThread["last_actor_agent_id"] != "mock_hermes" {
		t.Fatalf("expected thread to remember last actor, got %#v", loadedThread["last_actor_agent_id"])
	}
	if loadedThread["last_target_agent_id"] != "mock_openclaw" {
		t.Fatalf("expected thread to remember last target, got %#v", loadedThread["last_target_agent_id"])
	}

	turnsResp, err := http.Get(server.URL + "/v1/threads/" + created.Thread.ThreadID + "/turns")
	if err != nil {
		t.Fatalf("list thread turns: %v", err)
	}
	defer turnsResp.Body.Close()
	var turnsBody struct {
		Turns []map[string]any `json:"turns"`
	}
	if err := json.NewDecoder(turnsResp.Body).Decode(&turnsBody); err != nil {
		t.Fatalf("decode turns response: %v", err)
	}
	if len(turnsBody.Turns) != 3 {
		t.Fatalf("expected 3 thread turns, got %d", len(turnsBody.Turns))
	}
	if turnsBody.Turns[0]["sender_agent_id"] != "mock_hermes" || turnsBody.Turns[0]["target_agent_id"] != "mock_openclaw" {
		t.Fatalf("expected first turn order to persist, got %#v", turnsBody.Turns[0])
	}
	if turnsBody.Turns[1]["sender_agent_id"] != "mock_openclaw" || turnsBody.Turns[1]["target_agent_id"] != "mock_hermes" {
		t.Fatalf("expected second turn order to persist, got %#v", turnsBody.Turns[1])
	}
	if turnsBody.Turns[2]["sender_agent_id"] != "mock_hermes" || turnsBody.Turns[2]["target_agent_id"] != "mock_openclaw" {
		t.Fatalf("expected third turn order to persist, got %#v", turnsBody.Turns[2])
	}
}

func TestTaskCreateWithThreadIDRejectsInvalidAgentPair(t *testing.T) {
	server, orchestrator := setupServer(t)
	defer func() { _ = orchestrator.StopAllRuntimeProcesses(context.Background()) }()

	createResp, err := http.Post(server.URL+"/v1/threads", "application/json", strings.NewReader(`{"agent_a_id":"mock_hermes","agent_b_id":"mock_openclaw"}`))
	if err != nil {
		t.Fatalf("create thread: %v", err)
	}
	defer createResp.Body.Close()
	var created struct {
		Thread struct {
			ThreadID string `json:"thread_id"`
		} `json:"thread"`
	}
	if err := json.NewDecoder(createResp.Body).Decode(&created); err != nil {
		t.Fatalf("decode thread create response: %v", err)
	}

	invalidTaskResp, err := http.Post(server.URL+"/v1/tasks", "application/json", strings.NewReader(`{"thread_id":"`+created.Thread.ThreadID+`","sender":"mock_researcher_http","target_agent_id":"mock_hermes","intent":"summarize","payload":{"mode":"success"}}`))
	if err != nil {
		t.Fatalf("create invalid thread task: %v", err)
	}
	defer invalidTaskResp.Body.Close()
	if invalidTaskResp.StatusCode != http.StatusUnprocessableEntity {
		var body map[string]any
		_ = json.NewDecoder(invalidTaskResp.Body).Decode(&body)
		t.Fatalf("expected invalid thread pair rejection, got %d with body %#v", invalidTaskResp.StatusCode, body)
	}
}

func TestThreadContinuationAfterRestartPreservesHistory(t *testing.T) {
	root := t.TempDir()
	server, _, cleanup := setupServerAtRoot(t, root)
	createResp, err := http.Post(server.URL+"/v1/threads", "application/json", strings.NewReader(`{"agent_a_id":"mock_hermes","agent_b_id":"mock_openclaw"}`))
	if err != nil {
		t.Fatalf("create thread: %v", err)
	}
	defer createResp.Body.Close()
	var created struct {
		Thread struct {
			ThreadID string `json:"thread_id"`
		} `json:"thread"`
	}
	if err := json.NewDecoder(createResp.Body).Decode(&created); err != nil {
		t.Fatalf("decode create thread response: %v", err)
	}
	continueResp, err := http.Post(server.URL+"/v1/threads/"+created.Thread.ThreadID+"/continue", "application/json", strings.NewReader(`{"sender":"mock_hermes","intent":"ui.review","payload":{"mode":"success"}}`))
	if err != nil {
		t.Fatalf("continue thread before restart: %v", err)
	}
	defer continueResp.Body.Close()
	var firstTask struct {
		Task struct {
			TaskID string `json:"task_id"`
		} `json:"task"`
	}
	if err := json.NewDecoder(continueResp.Body).Decode(&firstTask); err != nil {
		t.Fatalf("decode first continue response: %v", err)
	}
	_ = waitForStatus(t, server.URL, firstTask.Task.TaskID, "completed")
	cleanup()

	restartedServer, restartedOrchestrator, restartedCleanup := setupServerAtRoot(t, root)
	defer restartedCleanup()
	secondContinueResp, err := http.Post(restartedServer.URL+"/v1/threads/"+created.Thread.ThreadID+"/continue", "application/json", strings.NewReader(`{"intent":"code.patch","payload":{"mode":"success"}}`))
	if err != nil {
		t.Fatalf("continue thread after restart: %v", err)
	}
	defer secondContinueResp.Body.Close()
	var secondTask struct {
		Task struct {
			TaskID string `json:"task_id"`
		} `json:"task"`
	}
	if err := json.NewDecoder(secondContinueResp.Body).Decode(&secondTask); err != nil {
		t.Fatalf("decode second continue response: %v", err)
	}
	_ = waitForStatus(t, restartedServer.URL, secondTask.Task.TaskID, "completed")
	threadResp, err := http.Get(restartedServer.URL + "/v1/threads/" + created.Thread.ThreadID + "/turns")
	if err != nil {
		t.Fatalf("get restarted thread turns: %v", err)
	}
	defer threadResp.Body.Close()
	var turns struct {
		Turns []map[string]any `json:"turns"`
	}
	if err := json.NewDecoder(threadResp.Body).Decode(&turns); err != nil {
		t.Fatalf("decode restarted turns response: %v", err)
	}
	if len(turns.Turns) != 2 {
		t.Fatalf("expected 2 turns after restart continuation, got %d", len(turns.Turns))
	}
	_ = restartedOrchestrator
}

func TestRestartMarksAwaitingInputThreadInterrupted(t *testing.T) {
	root := t.TempDir()
	server, _, cleanup := setupServerAtRoot(t, root)
	createResp, err := http.Post(server.URL+"/v1/threads", "application/json", strings.NewReader(`{"agent_a_id":"mock_hermes","agent_b_id":"mock_openclaw"}`))
	if err != nil {
		t.Fatalf("create thread: %v", err)
	}
	defer createResp.Body.Close()
	var created struct {
		Thread struct {
			ThreadID string `json:"thread_id"`
		} `json:"thread"`
	}
	if err := json.NewDecoder(createResp.Body).Decode(&created); err != nil {
		t.Fatalf("decode create thread response: %v", err)
	}
	continueResp, err := http.Post(server.URL+"/v1/threads/"+created.Thread.ThreadID+"/continue", "application/json", strings.NewReader(`{"sender":"mock_hermes","intent":"ui.review","payload":{"mode":"await_then_resume"}}`))
	if err != nil {
		t.Fatalf("continue thread before restart: %v", err)
	}
	defer continueResp.Body.Close()
	var firstTask struct {
		Task struct {
			TaskID string `json:"task_id"`
		} `json:"task"`
	}
	if err := json.NewDecoder(continueResp.Body).Decode(&firstTask); err != nil {
		t.Fatalf("decode awaiting-input continue response: %v", err)
	}
	_ = waitForStatus(t, server.URL, firstTask.Task.TaskID, "awaiting_input")
	cleanup()

	restartedServer, _, restartedCleanup := setupServerAtRoot(t, root)
	defer restartedCleanup()
	threadResp, err := http.Get(restartedServer.URL + "/v1/threads/" + created.Thread.ThreadID)
	if err != nil {
		t.Fatalf("get restarted thread: %v", err)
	}
	defer threadResp.Body.Close()
	var thread map[string]any
	if err := json.NewDecoder(threadResp.Body).Decode(&thread); err != nil {
		t.Fatalf("decode restarted thread response: %v", err)
	}
	if thread["status"] != "interrupted" {
		t.Fatalf("expected interrupted thread after restart, got %#v", thread["status"])
	}
}

func TestThreadAutoContinueRunsBoundedTurnsAndPersistsHistory(t *testing.T) {
	server, orchestrator := setupServer(t)
	defer func() { _ = orchestrator.StopAllRuntimeProcesses(context.Background()) }()

	createResp, err := http.Post(server.URL+"/v1/threads", "application/json", strings.NewReader(`{"agent_a_id":"mock_hermes","agent_b_id":"mock_openclaw"}`))
	if err != nil {
		t.Fatalf("create thread: %v", err)
	}
	defer createResp.Body.Close()
	var created struct {
		Thread struct {
			ThreadID string `json:"thread_id"`
		} `json:"thread"`
	}
	if err := json.NewDecoder(createResp.Body).Decode(&created); err != nil {
		t.Fatalf("decode thread create response: %v", err)
	}

	continueResp, err := http.Post(server.URL+"/v1/threads/"+created.Thread.ThreadID+"/continue", "application/json", strings.NewReader(`{
		"sender":"mock_hermes",
		"intent":"ui.review",
		"intent_by_agent":{"mock_hermes":"ui.review","mock_openclaw":"code.patch"},
		"payload":{"mode":"success"},
		"auto_continue":true,
		"max_turns":3,
		"stop_on_awaiting_input":true,
		"stop_on_terminal_error":true
	}`))
	if err != nil {
		t.Fatalf("auto-continue thread: %v", err)
	}
	defer continueResp.Body.Close()
	if continueResp.StatusCode != http.StatusAccepted {
		var body map[string]any
		_ = json.NewDecoder(continueResp.Body).Decode(&body)
		t.Fatalf("expected accepted auto-continue response, got %d with body %#v", continueResp.StatusCode, body)
	}

	turns := waitForThreadTurns(t, server.URL, created.Thread.ThreadID, 3, "completed", "completed", "completed")
	if turns[0]["sender_agent_id"] != "mock_hermes" || turns[1]["sender_agent_id"] != "mock_openclaw" || turns[2]["sender_agent_id"] != "mock_hermes" {
		t.Fatalf("expected bounded auto-continue to alternate senders, got %#v", turns)
	}
}

func TestThreadAutoContinueStopsDeterministicallyOnTerminalError(t *testing.T) {
	server, orchestrator := setupServer(t)
	defer func() { _ = orchestrator.StopAllRuntimeProcesses(context.Background()) }()

	createResp, err := http.Post(server.URL+"/v1/threads", "application/json", strings.NewReader(`{"agent_a_id":"mock_hermes","agent_b_id":"mock_openclaw"}`))
	if err != nil {
		t.Fatalf("create thread: %v", err)
	}
	defer createResp.Body.Close()
	var created struct {
		Thread struct {
			ThreadID string `json:"thread_id"`
		} `json:"thread"`
	}
	if err := json.NewDecoder(createResp.Body).Decode(&created); err != nil {
		t.Fatalf("decode thread create response: %v", err)
	}

	continueResp, err := http.Post(server.URL+"/v1/threads/"+created.Thread.ThreadID+"/continue", "application/json", strings.NewReader(`{
		"sender":"mock_hermes",
		"intent":"ui.review",
		"intent_by_agent":{"mock_hermes":"ui.review","mock_openclaw":"code.patch"},
		"payload":{"mode":"success"},
		"payload_by_agent":{"mock_openclaw":{"mode":"submit_fail"}},
		"auto_continue":true,
		"max_turns":3,
		"stop_on_terminal_error":true
	}`))
	if err != nil {
		t.Fatalf("auto-continue thread with failure: %v", err)
	}
	defer continueResp.Body.Close()

	turns := waitForThreadTurns(t, server.URL, created.Thread.ThreadID, 2, "completed", "failed")
	if turns[1]["sender_agent_id"] != "mock_openclaw" {
		t.Fatalf("expected second auto turn to come from mock_openclaw before stopping, got %#v", turns[1])
	}
}

func TestThreadInspectShowsContinuityBindingsAndNextReuse(t *testing.T) {
	server, orchestrator := setupRealAdapterServer(t)
	defer func() { _ = orchestrator.StopAllRuntimeProcesses(context.Background()) }()

	createResp, err := http.Post(server.URL+"/v1/threads", "application/json", strings.NewReader(`{"agent_a_id":"hermes_real_test","agent_b_id":"openclaw_real_test"}`))
	if err != nil {
		t.Fatalf("create thread: %v", err)
	}
	defer createResp.Body.Close()
	var created struct {
		Thread struct {
			ThreadID string `json:"thread_id"`
		} `json:"thread"`
	}
	if err := json.NewDecoder(createResp.Body).Decode(&created); err != nil {
		t.Fatalf("decode thread create response: %v", err)
	}

	firstResp, err := http.Post(server.URL+"/v1/threads/"+created.Thread.ThreadID+"/continue", "application/json", strings.NewReader(`{"sender":"hermes_real_test","intent":"ui.review","payload":{"mode":"success"}}`))
	if err != nil {
		t.Fatalf("first continue: %v", err)
	}
	defer firstResp.Body.Close()
	var first struct {
		Task struct {
			TaskID string `json:"task_id"`
		} `json:"task"`
	}
	if err := json.NewDecoder(firstResp.Body).Decode(&first); err != nil {
		t.Fatalf("decode first continue: %v", err)
	}
	_ = waitForStatus(t, server.URL, first.Task.TaskID, "completed")

	secondResp, err := http.Post(server.URL+"/v1/threads/"+created.Thread.ThreadID+"/continue", "application/json", strings.NewReader(`{"intent":"code.patch","payload":{"mode":"success"}}`))
	if err != nil {
		t.Fatalf("second continue: %v", err)
	}
	defer secondResp.Body.Close()
	var second struct {
		Task struct {
			TaskID string `json:"task_id"`
		} `json:"task"`
	}
	if err := json.NewDecoder(secondResp.Body).Decode(&second); err != nil {
		t.Fatalf("decode second continue: %v", err)
	}
	_ = waitForStatus(t, server.URL, second.Task.TaskID, "completed")

	inspectResp, err := http.Get(server.URL + "/v1/threads/" + created.Thread.ThreadID + "/inspect")
	if err != nil {
		t.Fatalf("inspect thread: %v", err)
	}
	defer inspectResp.Body.Close()
	var inspect struct {
		NextContinue map[string]any   `json:"next_continue"`
		Continuity   []map[string]any `json:"continuity"`
		Turns        []map[string]any `json:"turns"`
	}
	if err := json.NewDecoder(inspectResp.Body).Decode(&inspect); err != nil {
		t.Fatalf("decode inspect response: %v", err)
	}
	if len(inspect.Turns) != 2 {
		t.Fatalf("expected inspect response to include 2 turns, got %d", len(inspect.Turns))
	}
	if len(inspect.Continuity) != 2 {
		t.Fatalf("expected inspect response to include both thread sides, got %#v", inspect.Continuity)
	}
	if inspect.NextContinue["target_agent_id"] != "openclaw_real_test" {
		t.Fatalf("expected next continue target to be openclaw_real_test, got %#v", inspect.NextContinue)
	}
	if inspect.NextContinue["will_reuse_remote_session"] != true {
		t.Fatalf("expected next continue to report session reuse, got %#v", inspect.NextContinue)
	}
}

func TestThreadInspectShowsInterruptionReasonAfterRestart(t *testing.T) {
	root := t.TempDir()
	server, _, cleanup := setupServerAtRoot(t, root)
	createResp, err := http.Post(server.URL+"/v1/threads", "application/json", strings.NewReader(`{"agent_a_id":"mock_hermes","agent_b_id":"mock_openclaw"}`))
	if err != nil {
		t.Fatalf("create thread: %v", err)
	}
	defer createResp.Body.Close()
	var created struct {
		Thread struct {
			ThreadID string `json:"thread_id"`
		} `json:"thread"`
	}
	if err := json.NewDecoder(createResp.Body).Decode(&created); err != nil {
		t.Fatalf("decode create thread response: %v", err)
	}
	continueResp, err := http.Post(server.URL+"/v1/threads/"+created.Thread.ThreadID+"/continue", "application/json", strings.NewReader(`{"sender":"mock_hermes","intent":"ui.review","payload":{"mode":"await_then_resume"}}`))
	if err != nil {
		t.Fatalf("continue thread before restart: %v", err)
	}
	defer continueResp.Body.Close()
	var task struct {
		Task struct {
			TaskID string `json:"task_id"`
		} `json:"task"`
	}
	if err := json.NewDecoder(continueResp.Body).Decode(&task); err != nil {
		t.Fatalf("decode awaiting-input continue response: %v", err)
	}
	_ = waitForStatus(t, server.URL, task.Task.TaskID, "awaiting_input")
	cleanup()

	restartedServer, _, restartedCleanup := setupServerAtRoot(t, root)
	defer restartedCleanup()
	inspectResp, err := http.Get(restartedServer.URL + "/v1/threads/" + created.Thread.ThreadID + "/inspect")
	if err != nil {
		t.Fatalf("inspect restarted thread: %v", err)
	}
	defer inspectResp.Body.Close()
	var inspect map[string]any
	if err := json.NewDecoder(inspectResp.Body).Decode(&inspect); err != nil {
		t.Fatalf("decode inspect response: %v", err)
	}
	reason, _ := inspect["interruption_reason"].(string)
	if reason == "" {
		t.Fatalf("expected interruption reason after restart, got %#v", inspect)
	}
}
