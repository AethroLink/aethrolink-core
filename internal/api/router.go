package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/aethrolink/aethrolink-core/internal/agents"
	"github.com/aethrolink/aethrolink-core/internal/core"
	atypes "github.com/aethrolink/aethrolink-core/pkg/types"
)

type Server struct {
	orchestrator *core.Orchestrator
	agents       *agents.Service
}

// NewServer wires the thin HTTP control plane onto the orchestrator. The HTTP
// layer should stay translation-only: validate/decode requests, delegate to the
// core, then shape the response.
func NewServer(orchestrator *core.Orchestrator, agentService *agents.Service) http.Handler {
	s := &Server{orchestrator: orchestrator, agents: agentService}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /ping", s.handlePing)
	mux.HandleFunc("GET /artifacts/{artifact_id}", s.handleGetArtifact)
	mux.HandleFunc("POST /v1/agents/register", s.handleRegisterAgent)
	mux.HandleFunc("GET /v1/agents", s.handleListAgents)
	mux.HandleFunc("GET /v1/agents/{agent_id}", s.handleGetAgent)
	mux.HandleFunc("POST /v1/agents/{agent_id}/heartbeat", s.handleHeartbeatAgent)
	mux.HandleFunc("POST /v1/agents/{agent_id}/unregister", s.handleUnregisterAgent)
	mux.HandleFunc("POST /v1/tasks", s.handleCreateTask)
	mux.HandleFunc("GET /v1/tasks/{task_id}", s.handleGetTask)
	mux.HandleFunc("GET /v1/tasks/{task_id}/events", s.handleTaskEvents)
	mux.HandleFunc("POST /v1/tasks/{task_id}/resume", s.handleResumeTask)
	mux.HandleFunc("POST /v1/tasks/{task_id}/cancel", s.handleCancelTask)
	mux.HandleFunc("GET /v1/runtimes", s.handleListRuntimes)
	mux.HandleFunc("GET /v1/runtimes/{runtime_id}/health", s.handleRuntimeHealth)
	mux.HandleFunc("POST /v1/runtimes/{runtime_id}/start", s.handleRuntimeStart)
	mux.HandleFunc("POST /v1/runtimes/{runtime_id}/stop", s.handleRuntimeStop)
	return mux
}

func (s *Server) handlePing(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "service": "aethrolink-go"})
}

func (s *Server) handleCreateTask(w http.ResponseWriter, r *http.Request) {
	var req atypes.TaskCreateRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	task, err := s.orchestrator.CreateTask(r.Context(), req)
	if err != nil {
		writeError(w, errorStatus(err), err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"task": task,
		"links": map[string]string{
			"self":   fmt.Sprintf("/v1/tasks/%s", task.TaskID),
			"events": fmt.Sprintf("/v1/tasks/%s/events", task.TaskID),
		},
	})
}

func (s *Server) handleRegisterAgent(w http.ResponseWriter, r *http.Request) {
	var req atypes.AgentRegistrationRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	agent, err := s.agents.Register(r.Context(), req)
	if err != nil {
		writeError(w, errorStatus(err), err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"agent": agent})
}

func (s *Server) handleHeartbeatAgent(w http.ResponseWriter, r *http.Request) {
	var req atypes.AgentHeartbeatRequest
	_ = decodeJSONOptional(r, &req)
	agent, err := s.agents.Heartbeat(r.Context(), r.PathValue("agent_id"), req)
	if err != nil {
		writeError(w, errorStatus(err), err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"agent": agent})
}

func (s *Server) handleUnregisterAgent(w http.ResponseWriter, r *http.Request) {
	if err := s.agents.Unregister(r.Context(), r.PathValue("agent_id")); err != nil {
		writeError(w, errorStatus(err), err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"agent_id": r.PathValue("agent_id"), "status": atypes.AgentStatusOffline})
}

func (s *Server) handleGetAgent(w http.ResponseWriter, r *http.Request) {
	agent, err := s.agents.Get(r.Context(), r.PathValue("agent_id"))
	if err != nil {
		writeError(w, errorStatus(err), err)
		return
	}
	writeJSON(w, http.StatusOK, agent)
}

func (s *Server) handleListAgents(w http.ResponseWriter, r *http.Request) {
	agents, err := s.agents.List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"agents": agents})
}

func (s *Server) handleGetTask(w http.ResponseWriter, r *http.Request) {
	task, err := s.orchestrator.GetTask(r.Context(), r.PathValue("task_id"))
	if err != nil {
		writeError(w, errorStatus(err), err)
		return
	}
	writeJSON(w, http.StatusOK, task)
}

func (s *Server) handleGetArtifact(w http.ResponseWriter, r *http.Request) {
	artifactPath, err := s.orchestrator.LoadArtifactPath(r.Context(), r.PathValue("artifact_id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	http.ServeFile(w, r, artifactPath)
}

func (s *Server) handleTaskEvents(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("task_id")
	task, err := s.orchestrator.GetTask(r.Context(), taskID)
	if err != nil {
		writeError(w, errorStatus(err), err)
		return
	}
	history, err := s.orchestrator.ListEvents(r.Context(), taskID)
	if err != nil {
		writeError(w, errorStatus(err), err)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, errors.New("streaming unsupported"))
		return
	}
	// Replay persisted history first so late subscribers still see the full task
	// timeline before switching to live updates.
	for _, event := range history {
		writeSSE(w, event)
		flusher.Flush()
	}
	if task.Status.IsTerminal() {
		return
	}
	eventsCh, cancel := s.orchestrator.Subscribe(taskID)
	defer cancel()
	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-eventsCh:
			if !ok {
				return
			}
			writeSSE(w, event)
			flusher.Flush()
			if event.State.IsTerminal() {
				return
			}
		case <-time.After(15 * time.Second):
			fmt.Fprint(w, ": keep-alive\n\n")
			flusher.Flush()
		}
	}
}

func writeSSE(w http.ResponseWriter, event atypes.TaskEvent) {
	payload, _ := json.Marshal(event)
	fmt.Fprintf(w, "event: task.event\nid: %d\ndata: %s\n\n", event.Seq, payload)
}

func (s *Server) handleResumeTask(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Payload  map[string]any `json:"payload"`
		Metadata map[string]any `json:"metadata,omitempty"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	task, err := s.orchestrator.ResumeTask(r.Context(), r.PathValue("task_id"), body.Payload)
	if err != nil {
		writeError(w, errorStatus(err), err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"task_id": task.TaskID, "status": task.Status})
}

func (s *Server) handleCancelTask(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Reason string `json:"reason,omitempty"`
	}
	_ = decodeJSONOptional(r, &body)
	task, err := s.orchestrator.CancelTask(r.Context(), r.PathValue("task_id"), body.Reason)
	if err != nil {
		writeError(w, core.ErrorStatus(err), err)
		return
	}
	status := http.StatusAccepted
	if task.Status.IsTerminal() {
		status = http.StatusOK
	}
	writeJSON(w, status, map[string]any{"task_id": task.TaskID, "status": task.Status})
}

func (s *Server) handleListRuntimes(w http.ResponseWriter, r *http.Request) {
	runtimes, err := s.orchestrator.ListRuntimes(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"runtimes": runtimes})
}

func (s *Server) handleRuntimeHealth(w http.ResponseWriter, r *http.Request) {
	health, err := s.orchestrator.RuntimeHealth(r.Context(), r.PathValue("runtime_id"))
	if err != nil {
		writeError(w, core.ErrorStatus(err), err)
		return
	}
	writeJSON(w, http.StatusOK, health)
}

func (s *Server) handleRuntimeStart(w http.ResponseWriter, r *http.Request) {
	runtimeOptions, err := decodeRuntimeControl(r.Context(), r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	result, err := s.orchestrator.StartRuntime(r.Context(), r.PathValue("runtime_id"), runtimeOptions)
	if err != nil {
		writeError(w, core.ErrorStatus(err), err)
		return
	}
	writeJSON(w, http.StatusAccepted, result)
}

func (s *Server) handleRuntimeStop(w http.ResponseWriter, r *http.Request) {
	runtimeOptions, err := decodeRuntimeControl(r.Context(), r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	result, err := s.orchestrator.StopRuntime(r.Context(), r.PathValue("runtime_id"), runtimeOptions)
	if err != nil {
		writeError(w, core.ErrorStatus(err), err)
		return
	}
	writeJSON(w, http.StatusAccepted, result)
}

func decodeRuntimeControl(_ context.Context, r *http.Request) (map[string]any, error) {
	var body struct {
		RuntimeOptions map[string]any `json:"runtime_options,omitempty"`
	}
	if err := decodeJSONOptional(r, &body); err != nil {
		return nil, err
	}
	if body.RuntimeOptions == nil {
		body.RuntimeOptions = map[string]any{}
	}
	return body.RuntimeOptions, nil
}

func decodeJSON(r *http.Request, out any) error {
	if r.Body == nil {
		return errors.New("request body required")
	}
	defer r.Body.Close()
	decoder := json.NewDecoder(r.Body)
	// Reject unknown fields at the API edge so request mistakes fail fast instead
	// of being silently ignored deeper in the orchestration path.
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return err
	}
	return nil
}

func decodeJSONOptional(r *http.Request, out any) error {
	if r.Body == nil {
		return nil
	}
	defer r.Body.Close()
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(out); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, http.ErrBodyReadAfterClose) {
			return nil
		}
		if err.Error() == "EOF" {
			return nil
		}
		return err
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]any{"error": core.ErrorMessage(err)})
}
