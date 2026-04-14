package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aethrolink/aethrolink-core/internal/adapters"
	"github.com/aethrolink/aethrolink-core/internal/config"
	"github.com/aethrolink/aethrolink-core/internal/core"
	"github.com/aethrolink/aethrolink-core/internal/runtime"
	"github.com/aethrolink/aethrolink-core/internal/storage"
	"github.com/aethrolink/aethrolink-core/internal/testsupport/mockadapters"
)

func setupServer(t *testing.T) (*httptest.Server, *core.Orchestrator) {
	t.Helper()
	root := filepath.Clean(filepath.Join("..", ".."))
	registryPath := filepath.Join(t.TempDir(), "registry.yaml")
	content := fmt.Sprintf(`runtimes:
  mock_hermes:
    adapter: mock_hermes
    launch:
      mode: managed
      commands:
        coder: ["go", "run", "%s/cmd/fake-acp-client-agent"]
        research: ["go", "run", "%s/cmd/fake-acp-client-agent"]
        ops: ["go", "run", "%s/cmd/fake-acp-client-agent"]
    defaults:
      executor: coder
    capabilities:
      - code.patch
      - research.topic
  mock_openclaw:
    adapter: mock_openclaw
    launch:
      mode: on_demand
      command: ["go", "run", "%s/cmd/fake-acp-client-agent"]
    defaults:
      session_key: main
    capabilities:
      - ui.review
  mock_researcher_http:
    adapter: mock_acp_comm_http
    endpoint: http://127.0.0.1:19102
    healthcheck: http://127.0.0.1:19102/ping
    launch:
      mode: managed
      command: ["go", "run", "%s/cmd/fake-acp-comm-agent", "--port", "19102"]
    capabilities:
      - summarize
routes:
  code.patch:
    runtime: mock_hermes
    runtime_options:
      executor: coder
  ui.review:
    runtime: mock_openclaw
    runtime_options:
      session_key: design
  summarize:
    runtime: mock_researcher_http
`, root, root, root, root, root)
	if err := os.WriteFile(registryPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write registry: %v", err)
	}
	registry, err := config.LoadRegistry(registryPath)
	if err != nil {
		t.Fatalf("load registry: %v", err)
	}
	tmp := t.TempDir()
	store, err := storage.Open(filepath.Join(tmp, "aethrolink.db"), filepath.Join(tmp, "artifacts"), "http://127.0.0.1")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	runtimeManager := runtime.NewManager(store)
	adapterRegistry := adapters.NewRegistry()
	mockadapters.RegisterAll(adapterRegistry, registry, runtimeManager)
	orchestrator := core.NewOrchestrator(registry, store, runtimeManager, adapterRegistry)
	if err := orchestrator.PreloadRegistry(context.Background()); err != nil {
		t.Fatalf("preload registry: %v", err)
	}
	server := httptest.NewServer(NewServer(orchestrator))
	t.Cleanup(func() {
		_ = orchestrator.StopAllRuntimeProcesses(context.Background())
		server.Close()
		_ = store.Close()
	})
	return server, orchestrator
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
