package api

import (
	"bufio"
	"bytes"
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
	"github.com/aethrolink/aethrolink-core/internal/nodeproto"
	"github.com/aethrolink/aethrolink-core/internal/runtime"
	"github.com/aethrolink/aethrolink-core/internal/storage"
	"github.com/aethrolink/aethrolink-core/internal/testsupport/mockadapters"
	atypes "github.com/aethrolink/aethrolink-core/pkg/types"
)

func setupNodeTransportServer(t *testing.T, nodeID string) (*httptest.Server, *core.Orchestrator) {
	t.Helper()
	server, orchestrator, _ := setupNodeTransportServerWithStore(t, nodeID)
	return server, orchestrator
}

func setupNodeTransportServerWithStore(t *testing.T, nodeID string) (*httptest.Server, *core.Orchestrator, *storage.SQLiteStore) {
	t.Helper()
	tmp := t.TempDir()
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
			"coder": {"go", "run", root + "/cmd/fake-acp-client-agent"},
		}},
		Defaults:     map[string]any{"executor": "coder"},
		Capabilities: []string{"code.patch"},
	})
	adapterRegistry := adapters.NewRegistry()
	mockadapters.RegisterAll(adapterRegistry, agentService, runtimeManager)
	orchestrator, err := core.NewOrchestrator(agentService, store, runtimeManager, adapterRegistry)
	if err != nil {
		_ = store.Close()
		t.Fatalf("create orchestrator: %v", err)
	}
	server := httptest.NewServer(NewServerWithNodeID(orchestrator, agentService, nodeID))
	t.Cleanup(func() {
		_ = orchestrator.StopAllRuntimeProcesses(context.Background())
		server.Close()
		_ = store.Close()
	})
	return server, orchestrator, store
}

func TestNodeHealthEndpointReturnsDestinationIdentity(t *testing.T) {
	server, _ := setupNodeTransportServer(t, "node-b")

	resp, err := http.Get(server.URL + "/v1/node/health")
	if err != nil {
		t.Fatalf("get node health: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var health nodeproto.NodeHealthResponse
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		t.Fatalf("decode health: %v", err)
	}
	if health.NodeID != "node-b" || !health.OK {
		t.Fatalf("expected node-b healthy response, got %+v", health)
	}
}

func TestNodeTaskSubmitEndpointAcceptsRemoteSubmitAndExecutesLocally(t *testing.T) {
	server, _ := setupNodeTransportServer(t, "node-b")
	delivery := atypes.DefaultDeliveryPolicy()
	submit := nodeproto.TaskSubmitRequest{
		OriginNodeID:      "node-a",
		OriginProxyTaskID: "proxy-task-1",
		OriginThreadID:    "origin-thread-1",
		TargetAgentID:     "mock_hermes",
		Intent:            "code.patch",
		Payload:           map[string]any{"mode": "success"},
		RuntimeOptions:    map[string]any{"executor": "coder"},
		Trace:             atypes.TraceContext{TraceID: "trace-node-submit"},
		Delivery:          &delivery,
		SubmittedAt:       time.Now().UTC(),
	}
	body, err := json.Marshal(submit)
	if err != nil {
		t.Fatalf("marshal submit: %v", err)
	}

	resp, err := http.Post(server.URL+"/v1/node/tasks", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("submit node task: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", resp.StatusCode)
	}
	var accepted nodeproto.TaskAcceptedResponse
	if err := json.NewDecoder(resp.Body).Decode(&accepted); err != nil {
		t.Fatalf("decode accepted response: %v", err)
	}
	if accepted.OriginProxyTaskID != "proxy-task-1" || accepted.DestinationNodeID != "node-b" || accepted.DestinationTaskID == "" {
		t.Fatalf("accepted response did not bind origin and destination tasks: %+v", accepted)
	}
	if err := accepted.Validate(); err != nil {
		t.Fatalf("accepted response should validate: %v", err)
	}

	task := waitForStatus(t, server.URL, accepted.DestinationTaskID, "completed")
	if task["requested_agent_id"] != "mock_hermes" || task["resolved_agent_id"] != "mock_hermes" {
		t.Fatalf("destination task did not execute through local target: %+v", task)
	}
}

func TestNodeTaskSubmitEndpointUsesDefaultDeliveryWhenPeerOmitsPolicy(t *testing.T) {
	server, _ := setupNodeTransportServer(t, "node-b")
	body := []byte(`{
		"origin_node_id":"node-a",
		"origin_proxy_task_id":"proxy-task-omitted-delivery",
		"target_agent_id":"mock_hermes",
		"intent":"code.patch",
		"payload":{"mode":"success"},
		"runtime_options":{"executor":"coder"},
		"trace":{"trace_id":"trace-omitted-delivery"}
	}`)

	resp, err := http.Post(server.URL+"/v1/node/tasks", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("submit node task without delivery: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", resp.StatusCode)
	}
	var accepted nodeproto.TaskAcceptedResponse
	if err := json.NewDecoder(resp.Body).Decode(&accepted); err != nil {
		t.Fatalf("decode accepted response: %v", err)
	}

	// Omitted peer delivery must behave like /v1/tasks and launch an idle runtime.
	_ = waitForStatus(t, server.URL, accepted.DestinationTaskID, "completed")
}

func TestNodeTaskEventsEndpointStreamsTypedEventFrames(t *testing.T) {
	server, _ := setupNodeTransportServer(t, "node-b")
	accepted := submitNodeTask(t, server.URL, map[string]any{"mode": "success"})
	_ = waitForStatus(t, server.URL, accepted.DestinationTaskID, "completed")

	resp, err := http.Get(server.URL + "/v1/node/tasks/" + accepted.DestinationTaskID + "/events?origin_proxy_task_id=proxy-task-1")
	if err != nil {
		t.Fatalf("stream node task events: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	frames := decodeSSETaskEventFrames(t, resp)
	if len(frames) == 0 {
		t.Fatalf("expected at least one typed node event frame")
	}
	last := frames[len(frames)-1]
	if last.OriginProxyTaskID != "proxy-task-1" || last.DestinationNodeID != "node-b" || last.DestinationTaskID != accepted.DestinationTaskID || last.State != atypes.TaskStatusCompleted {
		t.Fatalf("unexpected terminal event frame: %+v", last)
	}
}

func TestNodeTaskEventsEndpointDoesNotMissTerminalEventDuringReplay(t *testing.T) {
	server, orchestrator, store := setupNodeTransportServerWithStore(t, "node-b")
	accepted := submitNodeTask(t, server.URL, map[string]any{"mode": "await_then_resume"})
	_ = waitForStatus(t, server.URL, accepted.DestinationTaskID, "awaiting_input")
	history, err := orchestrator.ListEvents(context.Background(), accepted.DestinationTaskID)
	if err != nil {
		t.Fatalf("list seed events: %v", err)
	}
	for i := 0; i < 10000; i++ {
		// Extra non-terminal history widens the replay window for the race under test.
		event := atypes.TaskEvent{EventID: atypes.NewID(), TaskID: accepted.DestinationTaskID, Seq: int64(len(history) + i + 1), Kind: atypes.TaskEventTaskAwaitingInput, State: atypes.TaskStatusAwaitingInput, Source: atypes.EventSourceAdapter, Message: "replay padding", Data: map[string]any{"index": i}, CreatedAt: atypes.NowUTC()}
		if err := store.AppendEvent(context.Background(), event); err != nil {
			t.Fatalf("append replay padding event: %v", err)
		}
	}

	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(server.URL + "/v1/node/tasks/" + accepted.DestinationTaskID + "/events?origin_proxy_task_id=proxy-task-1")
	if err != nil {
		t.Fatalf("stream node task events: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	cancelNodeTask(t, server.URL, accepted.DestinationTaskID)

	frames, err := readNodeEventFramesUntilState(resp, atypes.TaskStatusCancelled)
	if err != nil {
		t.Fatalf("expected cancelled frame before stream timeout: %v", err)
	}
	last := frames[len(frames)-1]
	if last.State != atypes.TaskStatusCancelled || last.DestinationTaskID != accepted.DestinationTaskID {
		t.Fatalf("unexpected terminal frame: %+v", last)
	}
	minReplayFrames := len(history) + 10000
	if len(frames) < minReplayFrames {
		t.Fatalf("expected replay frames to be preserved before live terminal event, got %d want at least %d", len(frames), minReplayFrames)
	}
}

func TestNodeTaskResumeEndpointContinuesDestinationTask(t *testing.T) {
	server, _ := setupNodeTransportServer(t, "node-b")
	accepted := submitNodeTask(t, server.URL, map[string]any{"mode": "await_then_resume"})
	_ = waitForStatus(t, server.URL, accepted.DestinationTaskID, "awaiting_input")

	resume := nodeproto.TaskResumeRequest{OriginNodeID: "node-a", OriginProxyTaskID: "proxy-task-1", DestinationTaskID: accepted.DestinationTaskID, Payload: map[string]any{"approved": true}, Trace: atypes.DefaultTraceContext(), RequestedAt: time.Now().UTC()}
	body, err := json.Marshal(resume)
	if err != nil {
		t.Fatalf("marshal resume: %v", err)
	}
	resp, err := http.Post(server.URL+"/v1/node/tasks/"+accepted.DestinationTaskID+"/resume", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("resume node task: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", resp.StatusCode)
	}
	_ = waitForStatus(t, server.URL, accepted.DestinationTaskID, "completed")
}

func TestNodeTaskCancelEndpointCancelsDestinationTask(t *testing.T) {
	server, _ := setupNodeTransportServer(t, "node-b")
	accepted := submitNodeTask(t, server.URL, map[string]any{"mode": "await_then_resume"})
	_ = waitForStatus(t, server.URL, accepted.DestinationTaskID, "awaiting_input")

	cancel := nodeproto.TaskCancelRequest{OriginNodeID: "node-a", OriginProxyTaskID: "proxy-task-1", DestinationTaskID: accepted.DestinationTaskID, Reason: "operator stopped", Trace: atypes.DefaultTraceContext(), RequestedAt: time.Now().UTC()}
	body, err := json.Marshal(cancel)
	if err != nil {
		t.Fatalf("marshal cancel: %v", err)
	}
	resp, err := http.Post(server.URL+"/v1/node/tasks/"+accepted.DestinationTaskID+"/cancel", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("cancel node task: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		t.Fatalf("expected 200 or 202, got %d", resp.StatusCode)
	}
	_ = waitForStatus(t, server.URL, accepted.DestinationTaskID, "cancelled")
}

func TestNodeTaskSubmitEndpointReturnsTypedProtocolError(t *testing.T) {
	server, _ := setupNodeTransportServer(t, "node-b")
	submit := nodeproto.TaskSubmitRequest{
		OriginNodeID:      "node-a",
		OriginProxyTaskID: "proxy-task-1",
		Intent:            "code.patch",
		Payload:           map[string]any{"mode": "success"},
		SubmittedAt:       time.Now().UTC(),
	}
	body, err := json.Marshal(submit)
	if err != nil {
		t.Fatalf("marshal submit: %v", err)
	}

	resp, err := http.Post(server.URL+"/v1/node/tasks", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("submit invalid node task: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
	var protocolErr nodeproto.ErrorResponse
	if err := json.NewDecoder(resp.Body).Decode(&protocolErr); err != nil {
		t.Fatalf("decode protocol error: %v", err)
	}
	if protocolErr.MessageTypeHint != nodeproto.MessageTypeTaskSubmit || protocolErr.Code != "invalid_task_submit" || protocolErr.Message == "" {
		t.Fatalf("expected typed invalid submit error, got %+v", protocolErr)
	}
}

func submitNodeTask(t *testing.T, baseURL string, payload map[string]any) nodeproto.TaskAcceptedResponse {
	t.Helper()
	delivery := atypes.DefaultDeliveryPolicy()
	submit := nodeproto.TaskSubmitRequest{
		OriginNodeID:      "node-a",
		OriginProxyTaskID: "proxy-task-1",
		OriginThreadID:    "origin-thread-1",
		TargetAgentID:     "mock_hermes",
		Intent:            "code.patch",
		Payload:           payload,
		RuntimeOptions:    map[string]any{"executor": "coder"},
		Trace:             atypes.TraceContext{TraceID: "trace-node-submit"},
		Delivery:          &delivery,
		SubmittedAt:       time.Now().UTC(),
	}
	body, err := json.Marshal(submit)
	if err != nil {
		t.Fatalf("marshal submit: %v", err)
	}
	resp, err := http.Post(baseURL+"/v1/node/tasks", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("submit node task: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", resp.StatusCode)
	}
	var accepted nodeproto.TaskAcceptedResponse
	if err := json.NewDecoder(resp.Body).Decode(&accepted); err != nil {
		t.Fatalf("decode accepted response: %v", err)
	}
	return accepted
}

func cancelNodeTask(t *testing.T, baseURL, taskID string) {
	t.Helper()
	cancel := nodeproto.TaskCancelRequest{OriginNodeID: "node-a", OriginProxyTaskID: "proxy-task-1", DestinationTaskID: taskID, Reason: "race regression", Trace: atypes.DefaultTraceContext(), RequestedAt: time.Now().UTC()}
	body, err := json.Marshal(cancel)
	if err != nil {
		t.Fatalf("marshal cancel: %v", err)
	}
	resp, err := http.Post(baseURL+"/v1/node/tasks/"+taskID+"/cancel", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("cancel node task: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		t.Fatalf("expected 200 or 202, got %d", resp.StatusCode)
	}
}

func readNodeEventFramesUntilState(resp *http.Response, state atypes.TaskStatus) ([]nodeproto.TaskEventFrame, error) {
	var frames []nodeproto.TaskEventFrame
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var frame nodeproto.TaskEventFrame
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &frame); err != nil {
			return frames, err
		}
		frames = append(frames, frame)
		if frame.State == state {
			return frames, nil
		}
	}
	return frames, scanner.Err()
}

func decodeSSETaskEventFrames(t *testing.T, resp *http.Response) []nodeproto.TaskEventFrame {
	t.Helper()
	var frames []nodeproto.TaskEventFrame
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var frame nodeproto.TaskEventFrame
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &frame); err != nil {
			t.Fatalf("decode node event frame: %v", err)
		}
		frames = append(frames, frame)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan event stream: %v", err)
	}
	return frames
}
