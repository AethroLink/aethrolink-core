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
	mux.HandleFunc("POST /v1/threads", s.handleCreateThread)
	mux.HandleFunc("GET /v1/threads/{thread_id}", s.handleGetThread)
	mux.HandleFunc("GET /v1/threads/{thread_id}/inspect", s.handleInspectThread)
	mux.HandleFunc("GET /v1/threads/{thread_id}/turns", s.handleListThreadTurns)
	mux.HandleFunc("POST /v1/threads/{thread_id}/continue", s.handleContinueThread)
	mux.HandleFunc("GET /v1/targets", s.handleListTargets)
	mux.HandleFunc("GET /v1/targets/{agent_id}/health", s.handleTargetHealth)
	mux.HandleFunc("POST /v1/targets/{agent_id}/start", s.handleTargetStart)
	mux.HandleFunc("POST /v1/targets/{agent_id}/stop", s.handleTargetStop)
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

func (s *Server) handleCreateThread(w http.ResponseWriter, r *http.Request) {
	var req atypes.ThreadCreateRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	thread, err := s.orchestrator.CreateThread(r.Context(), req)
	if err != nil {
		writeError(w, errorStatus(err), err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"thread": thread,
		"links": map[string]string{
			"self":  fmt.Sprintf("/v1/threads/%s", thread.ThreadID),
			"turns": fmt.Sprintf("/v1/threads/%s/turns", thread.ThreadID),
		},
	})
}

func (s *Server) handleGetThread(w http.ResponseWriter, r *http.Request) {
	thread, err := s.orchestrator.GetThread(r.Context(), r.PathValue("thread_id"))
	if err != nil {
		writeError(w, errorStatus(err), err)
		return
	}
	writeJSON(w, http.StatusOK, thread)
}

func (s *Server) handleInspectThread(w http.ResponseWriter, r *http.Request) {
	inspection, err := s.orchestrator.InspectThread(r.Context(), r.PathValue("thread_id"))
	if err != nil {
		writeError(w, errorStatus(err), err)
		return
	}
	writeJSON(w, http.StatusOK, inspection)
}

func (s *Server) handleListThreadTurns(w http.ResponseWriter, r *http.Request) {
	turns, err := s.orchestrator.ListThreadTurns(r.Context(), r.PathValue("thread_id"))
	if err != nil {
		writeError(w, errorStatus(err), err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"turns": turns})
}

func (s *Server) handleContinueThread(w http.ResponseWriter, r *http.Request) {
	var req atypes.ThreadContinueRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	task, err := s.orchestrator.ContinueThread(r.Context(), r.PathValue("thread_id"), req)
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

func (s *Server) handleListTargets(w http.ResponseWriter, r *http.Request) {
	targets, err := s.orchestrator.ListTargets(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"targets": targets})
}

func (s *Server) handleTargetHealth(w http.ResponseWriter, r *http.Request) {
	health, err := s.orchestrator.TargetHealth(r.Context(), r.PathValue("agent_id"))
	if err != nil {
		writeError(w, core.ErrorStatus(err), err)
		return
	}
	writeJSON(w, http.StatusOK, health)
}

func (s *Server) handleTargetStart(w http.ResponseWriter, r *http.Request) {
	runtimeOptions, err := decodeRuntimeControl(r.Context(), r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	result, err := s.orchestrator.StartTarget(r.Context(), r.PathValue("agent_id"), runtimeOptions)
	if err != nil {
		writeError(w, core.ErrorStatus(err), err)
		return
	}
	writeJSON(w, http.StatusAccepted, result)
}

func (s *Server) handleTargetStop(w http.ResponseWriter, r *http.Request) {
	runtimeOptions, err := decodeRuntimeControl(r.Context(), r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	result, err := s.orchestrator.StopTarget(r.Context(), r.PathValue("agent_id"), runtimeOptions)
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
