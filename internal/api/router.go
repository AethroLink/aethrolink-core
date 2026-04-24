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
	"github.com/aethrolink/aethrolink-core/internal/nodeproto"
	atypes "github.com/aethrolink/aethrolink-core/pkg/types"
)

type Server struct {
	orchestrator *core.Orchestrator
	agents       *agents.Service
	nodeID       string
}

// NewServer wires the thin HTTP control plane onto the orchestrator. The HTTP
// layer should stay translation-only: validate/decode requests, delegate to the
// core, then shape the response.
func NewServer(orchestrator *core.Orchestrator, agentService *agents.Service) http.Handler {
	return NewServerWithNodeID(orchestrator, agentService, "local-node")
}

// NewServerWithNodeID exposes node-transport endpoints with an explicit node identity.
func NewServerWithNodeID(orchestrator *core.Orchestrator, agentService *agents.Service, nodeID string) http.Handler {
	if nodeID == "" {
		nodeID = "local-node"
	}
	s := &Server{orchestrator: orchestrator, agents: agentService, nodeID: nodeID}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /ping", s.handlePing)
	mux.HandleFunc("GET /v1/node/health", s.handleNodeHealth)
	mux.HandleFunc("POST /v1/node/tasks", s.handleNodeTaskSubmit)
	mux.HandleFunc("GET /v1/node/tasks/{task_id}/events", s.handleNodeTaskEvents)
	mux.HandleFunc("POST /v1/node/tasks/{task_id}/resume", s.handleNodeTaskResume)
	mux.HandleFunc("POST /v1/node/tasks/{task_id}/cancel", s.handleNodeTaskCancel)
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

// handleNodeHealth is the static-peer liveness probe for Phase 3 HTTP transport.
func (s *Server) handleNodeHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, nodeproto.NodeHealthResponse{
		NodeID:    s.nodeID,
		OK:        true,
		Protocol:  "aethrolink.node.v1",
		CheckedAt: atypes.NowUTC(),
	})
}

// handleNodeTaskSubmit accepts a peer relay request and executes it through local routing.
func (s *Server) handleNodeTaskSubmit(w http.ResponseWriter, r *http.Request) {
	var req nodeproto.TaskSubmitRequest
	if err := decodeJSON(r, &req); err != nil {
		writeNodeError(w, http.StatusBadRequest, nodeproto.MessageTypeTaskSubmit, "invalid_json", err, false)
		return
	}
	if err := req.Validate(); err != nil {
		writeNodeError(w, http.StatusBadRequest, nodeproto.MessageTypeTaskSubmit, "invalid_task_submit", err, false)
		return
	}
	task, err := s.orchestrator.CreateTask(r.Context(), atypes.TaskCreateRequest{
		Sender:         req.OriginNodeID,
		TargetAgentID:  req.TargetAgentID,
		Intent:         req.Intent,
		Payload:        req.Payload,
		RuntimeOptions: req.RuntimeOptions,
		Delivery:       req.Delivery,
		Metadata: map[string]any{
			"origin_node_id":       req.OriginNodeID,
			"origin_proxy_task_id": req.OriginProxyTaskID,
			"origin_thread_id":     req.OriginThreadID,
			"trace_id":             req.Trace.TraceID,
		},
	})
	if err != nil {
		writeNodeError(w, errorStatus(err), nodeproto.MessageTypeTaskSubmit, "task_submit_failed", err, true)
		return
	}
	writeJSON(w, http.StatusAccepted, nodeproto.TaskAcceptedResponse{
		OriginProxyTaskID: req.OriginProxyTaskID,
		DestinationNodeID: s.nodeID,
		DestinationTaskID: task.TaskID,
		AcceptedAt:        atypes.NowUTC(),
	})
}

// handleNodeTaskEvents streams destination task events as typed peer frames.
func (s *Server) handleNodeTaskEvents(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("task_id")
	originProxyTaskID := r.URL.Query().Get("origin_proxy_task_id")
	if originProxyTaskID == "" {
		writeNodeError(w, http.StatusBadRequest, nodeproto.MessageTypeTaskEvent, "invalid_task_events", errors.New("origin_proxy_task_id is required"), false)
		return
	}
	// Subscribe before replay so events committed during history reads cannot be lost.
	eventsCh, cancel := s.orchestrator.Subscribe(taskID)
	defer cancel()
	task, err := s.orchestrator.GetTask(r.Context(), taskID)
	if err != nil {
		writeNodeError(w, errorStatus(err), nodeproto.MessageTypeTaskEvent, "task_events_failed", err, true)
		return
	}
	history, err := s.orchestrator.ListEvents(r.Context(), taskID)
	if err != nil {
		writeNodeError(w, errorStatus(err), nodeproto.MessageTypeTaskEvent, "task_events_failed", err, true)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeNodeError(w, http.StatusInternalServerError, nodeproto.MessageTypeTaskEvent, "streaming_unsupported", errors.New("streaming unsupported"), false)
		return
	}
	// Flush headers before replay so peers can issue resume/cancel while history streams.
	flusher.Flush()
	var lastReplaySeq int64
	terminalReplayed := false
	for _, event := range history {
		if event.Seq <= lastReplaySeq {
			continue
		}
		if event.Seq > lastReplaySeq {
			lastReplaySeq = event.Seq
		}
		if event.State.IsTerminal() {
			terminalReplayed = true
		}
		writeNodeSSE(w, nodeTaskEventFrame(s.nodeID, originProxyTaskID, task, event))
		flusher.Flush()
		select {
		case liveEvent, ok := <-eventsCh:
			if !ok {
				return
			}
			if liveEvent.Seq <= lastReplaySeq {
				continue
			}
			// Live terminal events must not wait behind a large replay backlog.
			lastReplaySeq = liveEvent.Seq
			writeNodeSSE(w, nodeTaskEventFrame(s.nodeID, originProxyTaskID, task, liveEvent))
			flusher.Flush()
			if liveEvent.State.IsTerminal() {
				return
			}
		default:
		}
	}
	if terminalReplayed {
		return
	}
	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-eventsCh:
			if !ok {
				return
			}
			if event.Seq <= lastReplaySeq {
				continue
			}
			writeNodeSSE(w, nodeTaskEventFrame(s.nodeID, originProxyTaskID, task, event))
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

// handleNodeTaskResume applies a peer resume request to the local destination task.
func (s *Server) handleNodeTaskResume(w http.ResponseWriter, r *http.Request) {
	var req nodeproto.TaskResumeRequest
	if err := decodeJSON(r, &req); err != nil {
		writeNodeError(w, http.StatusBadRequest, nodeproto.MessageTypeTaskResume, "invalid_json", err, false)
		return
	}
	if err := req.Validate(); err != nil {
		writeNodeError(w, http.StatusBadRequest, nodeproto.MessageTypeTaskResume, "invalid_task_resume", err, false)
		return
	}
	if req.DestinationTaskID != r.PathValue("task_id") {
		writeNodeError(w, http.StatusBadRequest, nodeproto.MessageTypeTaskResume, "task_id_mismatch", errors.New("destination_task_id does not match path"), false)
		return
	}
	task, err := s.orchestrator.ResumeTask(r.Context(), req.DestinationTaskID, req.Payload)
	if err != nil {
		writeNodeError(w, errorStatus(err), nodeproto.MessageTypeTaskResume, "task_resume_failed", err, true)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"task_id": task.TaskID, "status": task.Status})
}

// handleNodeTaskCancel applies a peer cancel request to the local destination task.
func (s *Server) handleNodeTaskCancel(w http.ResponseWriter, r *http.Request) {
	var req nodeproto.TaskCancelRequest
	if err := decodeJSON(r, &req); err != nil {
		writeNodeError(w, http.StatusBadRequest, nodeproto.MessageTypeTaskCancel, "invalid_json", err, false)
		return
	}
	if err := req.Validate(); err != nil {
		writeNodeError(w, http.StatusBadRequest, nodeproto.MessageTypeTaskCancel, "invalid_task_cancel", err, false)
		return
	}
	if req.DestinationTaskID != r.PathValue("task_id") {
		writeNodeError(w, http.StatusBadRequest, nodeproto.MessageTypeTaskCancel, "task_id_mismatch", errors.New("destination_task_id does not match path"), false)
		return
	}
	task, err := s.orchestrator.CancelTask(r.Context(), req.DestinationTaskID, req.Reason)
	if err != nil {
		writeNodeError(w, errorStatus(err), nodeproto.MessageTypeTaskCancel, "task_cancel_failed", err, true)
		return
	}
	status := http.StatusAccepted
	if task.Status.IsTerminal() {
		status = http.StatusOK
	}
	writeJSON(w, status, map[string]any{"task_id": task.TaskID, "status": task.Status})
}

// writeNodeSSE serializes peer event frames onto the node transport stream.
func writeNodeSSE(w http.ResponseWriter, frame nodeproto.TaskEventFrame) {
	payload, _ := json.Marshal(frame)
	fmt.Fprintf(w, "event: task.event\nid: %d\ndata: %s\n\n", frame.Seq, payload)
}

// nodeTaskEventFrame projects local destination events onto the peer protocol shape.
func nodeTaskEventFrame(nodeID string, originProxyTaskID string, task atypes.TaskRecord, event atypes.TaskEvent) nodeproto.TaskEventFrame {
	frame := nodeproto.TaskEventFrame{
		OriginProxyTaskID:   originProxyTaskID,
		DestinationNodeID:   nodeID,
		DestinationTaskID:   event.TaskID,
		DestinationThreadID: task.ThreadID,
		Seq:                 event.Seq,
		Kind:                event.Kind,
		State:               event.State,
		Source:              event.Source,
		Message:             event.Message,
		Data:                event.Data,
		OccurredAt:          event.CreatedAt,
	}
	if task.Remote != nil {
		frame.RemoteExecutionID = task.Remote.RemoteExecutionID
		frame.RemoteSessionID = task.Remote.RemoteSessionID
	}
	return frame
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

// writeNodeError keeps peer-facing failures on the typed node protocol surface.
func writeNodeError(w http.ResponseWriter, status int, messageType nodeproto.MessageType, code string, err error, retryable bool) {
	writeJSON(w, status, nodeproto.ErrorResponse{
		MessageTypeHint: messageType,
		Code:            code,
		Message:         core.ErrorMessage(err),
		Retryable:       retryable,
	})
}
