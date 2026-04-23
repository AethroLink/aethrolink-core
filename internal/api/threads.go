package api

import (
	"fmt"
	"net/http"

	atypes "github.com/aethrolink/aethrolink-core/pkg/types"
)

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
