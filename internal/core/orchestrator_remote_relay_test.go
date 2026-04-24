package core_test

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/aethrolink/aethrolink-core/internal/adapters"
	"github.com/aethrolink/aethrolink-core/internal/agents"
	"github.com/aethrolink/aethrolink-core/internal/api"
	"github.com/aethrolink/aethrolink-core/internal/core"
	"github.com/aethrolink/aethrolink-core/internal/runtime"
	"github.com/aethrolink/aethrolink-core/internal/storage"
	"github.com/aethrolink/aethrolink-core/internal/testsupport/mockadapters"
	atypes "github.com/aethrolink/aethrolink-core/pkg/types"
)

func TestCreateTaskRelaysExplicitRemoteTargetAndPersistsProxyBinding(t *testing.T) {
	ctx := context.Background()
	destinationServer, destinationOrchestrator, destinationStore := setupRelayDestination(t, "node-b")
	defer destinationServer.Close()
	defer destinationStore.Close()
	defer destinationOrchestrator.StopAllRuntimeProcesses(ctx)

	originStore, originOrchestrator := setupRelayOrigin(t, "node-a", destinationServer.URL)
	defer originStore.Close()
	defer originOrchestrator.StopAllRuntimeProcesses(ctx)

	task, err := originOrchestrator.CreateTask(ctx, atypes.TaskCreateRequest{
		Sender:         "operator",
		TargetAgentID:  "remote-coder",
		Intent:         "code.patch",
		Payload:        map[string]any{"mode": "success"},
		RuntimeOptions: map[string]any{"executor": "coder"},
	})
	if err != nil {
		t.Fatalf("create relayed task: %v", err)
	}
	if task.ResolvedAgentID != "remote-coder" {
		t.Fatalf("expected origin proxy to resolve remote target, got %+v", task)
	}

	completed := waitForOriginStatus(t, originOrchestrator, task.TaskID, atypes.TaskStatusCompleted)
	if completed.Remote == nil || completed.Remote.Binding == "" {
		t.Fatalf("expected origin task to expose remote binding, got %+v", completed.Remote)
	}
	binding, err := originStore.GetRemoteTaskBinding(ctx, task.TaskID)
	if err != nil {
		t.Fatalf("get remote task binding: %v", err)
	}
	if binding.LocalTaskID != task.TaskID || binding.RemotePeerID != "peer-b" || binding.DestinationNodeID != "node-b" || binding.DestinationTaskID == "" {
		t.Fatalf("unexpected remote binding: %+v", binding)
	}
	if _, err := destinationOrchestrator.GetTask(ctx, binding.DestinationTaskID); err != nil {
		t.Fatalf("destination did not create real task %q: %v", binding.DestinationTaskID, err)
	}
}

func TestCreateTaskMirrorsRemoteTerminalEventsIntoOriginEventLog(t *testing.T) {
	ctx := context.Background()
	destinationServer, destinationOrchestrator, destinationStore := setupRelayDestination(t, "node-b")
	defer destinationServer.Close()
	defer destinationStore.Close()
	defer destinationOrchestrator.StopAllRuntimeProcesses(ctx)

	originStore, originOrchestrator := setupRelayOrigin(t, "node-a", destinationServer.URL)
	defer originStore.Close()
	defer originOrchestrator.StopAllRuntimeProcesses(ctx)

	task, err := originOrchestrator.CreateTask(ctx, atypes.TaskCreateRequest{
		Sender:         "operator",
		TargetAgentID:  "remote-coder",
		Intent:         "code.patch",
		Payload:        map[string]any{"mode": "success"},
		RuntimeOptions: map[string]any{"executor": "coder"},
	})
	if err != nil {
		t.Fatalf("create relayed task: %v", err)
	}
	_ = waitForOriginStatus(t, originOrchestrator, task.TaskID, atypes.TaskStatusCompleted)

	events, err := originOrchestrator.ListEvents(ctx, task.TaskID)
	if err != nil {
		t.Fatalf("list origin events: %v", err)
	}
	var terminal atypes.TaskEvent
	for _, event := range events {
		if event.State == atypes.TaskStatusCompleted {
			terminal = event
		}
	}
	if terminal.EventID == "" {
		t.Fatalf("expected mirrored terminal event in origin log: %+v", events)
	}
	if terminal.Source != atypes.EventSourceTransport || terminal.TaskID != task.TaskID || terminal.Seq == 0 {
		t.Fatalf("terminal event was not origin-local transport event: %+v", terminal)
	}
	if terminal.Data["destination_node_id"] != "node-b" || terminal.Data["destination_task_id"] == "" || terminal.Data["remote_event_seq"] == nil {
		t.Fatalf("terminal event lost remote audit metadata: %+v", terminal.Data)
	}
}

func setupRelayDestination(t *testing.T, nodeID string) (*httptest.Server, *core.Orchestrator, *storage.SQLiteStore) {
	t.Helper()
	tmp := t.TempDir()
	root := filepath.Clean(filepath.Join("..", ".."))
	store, err := storage.Open(filepath.Join(tmp, "destination.db"), filepath.Join(tmp, "artifacts"), "http://127.0.0.1")
	if err != nil {
		t.Fatalf("open destination store: %v", err)
	}
	runtimeManager := runtime.NewManager(store)
	agentService := agents.NewService(store)
	registerRelayAgent(t, agentService, root)
	adapterRegistry := adapters.NewRegistry()
	mockadapters.RegisterAll(adapterRegistry, agentService, runtimeManager)
	orchestrator, err := core.NewOrchestrator(agentService, store, runtimeManager, adapterRegistry)
	if err != nil {
		store.Close()
		t.Fatalf("create destination orchestrator: %v", err)
	}
	return httptest.NewServer(api.NewServerWithNodeID(orchestrator, agentService, nodeID)), orchestrator, store
}

func setupRelayOrigin(t *testing.T, nodeID string, peerBaseURL string) (*storage.SQLiteStore, *core.Orchestrator) {
	t.Helper()
	tmp := t.TempDir()
	store, err := storage.Open(filepath.Join(tmp, "origin.db"), filepath.Join(tmp, "artifacts"), "http://127.0.0.1")
	if err != nil {
		t.Fatalf("open origin store: %v", err)
	}
	now := atypes.NowUTC()
	if err := store.UpsertPeer(context.Background(), atypes.PeerRecord{PeerID: "peer-b", DisplayName: "node-b", BaseURL: peerBaseURL, Status: atypes.PeerStatusOnline, RegisteredAt: now, UpdatedAt: now, LastSeenAt: now}); err != nil {
		t.Fatalf("upsert peer: %v", err)
	}
	if err := store.UpsertPeerTarget(context.Background(), atypes.PeerTargetRecord{PeerID: "peer-b", TargetID: "remote-coder", DisplayName: "remote coder", Capabilities: []string{"code.patch"}, Defaults: map[string]any{"executor": "coder"}, Status: atypes.PeerTargetStatusAvailable, SyncedAt: now}); err != nil {
		t.Fatalf("upsert peer target: %v", err)
	}
	agentService := agents.NewService(store)
	orchestrator, err := core.NewOrchestratorWithNodeID(agentService, store, runtime.NewManager(store), adapters.NewRegistry(), nodeID)
	if err != nil {
		store.Close()
		t.Fatalf("create origin orchestrator: %v", err)
	}
	return store, orchestrator
}

func registerRelayAgent(t *testing.T, service *agents.Service, root string) {
	t.Helper()
	_, err := service.Register(context.Background(), atypes.AgentRegistrationRequest{AgentID: "remote-coder", DisplayName: "remote coder", TransportKind: "local_managed", Adapter: "mock_hermes", Launch: atypes.LaunchSpec{Mode: atypes.LaunchModeManaged, Commands: map[string][]string{"coder": {"go", "run", root + "/cmd/fake-acp-client-agent"}}}, Defaults: map[string]any{"executor": "coder"}, Capabilities: []string{"code.patch"}})
	if err != nil {
		t.Fatalf("register destination agent: %v", err)
	}
}

func waitForOriginStatus(t *testing.T, orchestrator *core.Orchestrator, taskID string, want atypes.TaskStatus) atypes.TaskRecord {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		task, err := orchestrator.GetTask(context.Background(), taskID)
		if err == nil && task.Status == want {
			return task
		}
		if err == nil && task.Status.IsTerminal() && task.Status != want {
			t.Fatalf("task reached %s instead of %s: %+v", task.Status, want, task)
		}
		time.Sleep(50 * time.Millisecond)
	}
	task, err := orchestrator.GetTask(context.Background(), taskID)
	if err != nil {
		t.Fatalf("get task after timeout: %v", err)
	}
	t.Fatalf("timed out waiting for %s, got %+v", want, task)
	return atypes.TaskRecord{}
}
