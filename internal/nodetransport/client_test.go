package nodetransport

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aethrolink/aethrolink-core/internal/nodeproto"
	atypes "github.com/aethrolink/aethrolink-core/pkg/types"
)

func TestHTTPClientCallsNodeTransportEndpoints(t *testing.T) {
	seen := map[string]bool{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/node/health":
			seen["health"] = true
			writeTestJSON(t, w, http.StatusOK, nodeproto.NodeHealthResponse{NodeID: "node-b", OK: true, Protocol: "aethrolink.node.v1", CheckedAt: atypes.NowUTC()})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/node/tasks":
			seen["submit"] = true
			var req nodeproto.TaskSubmitRequest
			decodeTestJSON(t, r, &req)
			if req.OriginProxyTaskID != "proxy-1" || req.TargetAgentID != "mock_hermes" {
				t.Fatalf("unexpected submit request: %+v", req)
			}
			writeTestJSON(t, w, http.StatusAccepted, nodeproto.TaskAcceptedResponse{OriginProxyTaskID: req.OriginProxyTaskID, DestinationNodeID: "node-b", DestinationTaskID: "dest-1", AcceptedAt: atypes.NowUTC()})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/node/tasks/dest-1/resume":
			seen["resume"] = true
			var req nodeproto.TaskResumeRequest
			decodeTestJSON(t, r, &req)
			if approved, _ := req.Payload["approved"].(bool); req.DestinationTaskID != "dest-1" || !approved {
				t.Fatalf("unexpected resume request: %+v", req)
			}
			writeTestJSON(t, w, http.StatusAccepted, map[string]any{"task_id": "dest-1", "status": atypes.TaskStatusAwaitingInput})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/node/tasks/dest-1/cancel":
			seen["cancel"] = true
			var req nodeproto.TaskCancelRequest
			decodeTestJSON(t, r, &req)
			if req.Reason != "operator stopped" || req.DestinationTaskID != "dest-1" {
				t.Fatalf("unexpected cancel request: %+v", req)
			}
			writeTestJSON(t, w, http.StatusOK, map[string]any{"task_id": "dest-1", "status": atypes.TaskStatusCancelled})
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	client := NewHTTPClient(server.URL, "node-a", http.DefaultClient)
	ctx := context.Background()

	health, err := client.Health(ctx)
	if err != nil || health.NodeID != "node-b" {
		t.Fatalf("health failed: health=%+v err=%v", health, err)
	}
	accepted, err := client.SubmitTask(ctx, nodeproto.TaskSubmitRequest{OriginProxyTaskID: "proxy-1", TargetAgentID: "mock_hermes", Intent: "code.patch", Payload: map[string]any{"mode": "success"}, Trace: atypes.DefaultTraceContext(), Delivery: atypes.DefaultDeliveryPolicy(), SubmittedAt: atypes.NowUTC()})
	if err != nil || accepted.DestinationTaskID != "dest-1" {
		t.Fatalf("submit failed: accepted=%+v err=%v", accepted, err)
	}
	if err := client.ResumeTask(ctx, nodeproto.TaskResumeRequest{OriginProxyTaskID: "proxy-1", DestinationTaskID: "dest-1", Payload: map[string]any{"approved": true}, Trace: atypes.DefaultTraceContext(), RequestedAt: atypes.NowUTC()}); err != nil {
		t.Fatalf("resume failed: %v", err)
	}
	if err := client.CancelTask(ctx, nodeproto.TaskCancelRequest{OriginProxyTaskID: "proxy-1", DestinationTaskID: "dest-1", Reason: "operator stopped", Trace: atypes.DefaultTraceContext(), RequestedAt: atypes.NowUTC()}); err != nil {
		t.Fatalf("cancel failed: %v", err)
	}
	for _, name := range []string{"health", "submit", "resume", "cancel"} {
		if !seen[name] {
			t.Fatalf("expected %s endpoint to be called", name)
		}
	}
}

func TestHTTPClientStreamsNodeTaskEvents(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/node/tasks/dest-1/events" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if got := r.URL.Query().Get("origin_proxy_task_id"); got != "proxy-1" {
			t.Fatalf("expected origin proxy query, got %q", got)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, frame := range []nodeproto.TaskEventFrame{
			{OriginProxyTaskID: "proxy-1", DestinationNodeID: "node-b", DestinationTaskID: "dest-1", Seq: 1, Kind: atypes.TaskEventTaskRunning, State: atypes.TaskStatusRunning, Source: atypes.EventSourceRuntime, Message: "running", OccurredAt: atypes.NowUTC()},
			{OriginProxyTaskID: "proxy-1", DestinationNodeID: "node-b", DestinationTaskID: "dest-1", Seq: 2, Kind: atypes.TaskEventTaskCompleted, State: atypes.TaskStatusCompleted, Source: atypes.EventSourceRuntime, Message: "done", OccurredAt: atypes.NowUTC()},
		} {
			payload, _ := json.Marshal(frame)
			fmt.Fprintf(w, "event: task.event\nid: %d\ndata: %s\n\n", frame.Seq, payload)
		}
	}))
	defer server.Close()

	client := NewHTTPClient(server.URL, "node-a", http.DefaultClient)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	events, err := client.StreamTaskEvents(ctx, "dest-1", "proxy-1")
	if err != nil {
		t.Fatalf("stream events failed: %v", err)
	}
	if len(events) != 2 || events[1].State != atypes.TaskStatusCompleted || events[1].Message != "done" {
		t.Fatalf("unexpected streamed events: %+v", events)
	}
}

func TestHTTPClientReturnsTypedNodeErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeTestJSON(t, w, http.StatusBadRequest, nodeproto.ErrorResponse{MessageTypeHint: nodeproto.MessageTypeTaskSubmit, Code: "invalid_task_submit", Message: "target_agent_id is required"})
	}))
	defer server.Close()

	client := NewHTTPClient(server.URL, "node-a", http.DefaultClient)
	_, err := client.SubmitTask(context.Background(), nodeproto.TaskSubmitRequest{OriginProxyTaskID: "proxy-1", Intent: "code.patch", Payload: map[string]any{}})
	if err == nil {
		t.Fatalf("expected typed node error")
	}
	nodeErr, ok := err.(*NodeError)
	if !ok || nodeErr.Response.Code != "invalid_task_submit" {
		t.Fatalf("expected typed node error, got %#v", err)
	}
}

func writeTestJSON(t *testing.T, w http.ResponseWriter, status int, body any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		t.Fatalf("encode response: %v", err)
	}
}

func decodeTestJSON(t *testing.T, r *http.Request, out any) {
	t.Helper()
	defer r.Body.Close()
	if err := json.NewDecoder(r.Body).Decode(out); err != nil {
		t.Fatalf("decode request: %v", err)
	}
}
