package nodetransport

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/aethrolink/aethrolink-core/internal/nodeproto"
)

// NodeError preserves typed peer failures for origin-side routing decisions.
type NodeError struct {
	StatusCode int
	Response   nodeproto.ErrorResponse
}

func (e *NodeError) Error() string {
	return fmt.Sprintf("node transport error %d %s: %s", e.StatusCode, e.Response.Code, e.Response.Message)
}

// HTTPClient is the minimal static-peer transport client for Phase 3.
type HTTPClient struct {
	baseURL    string
	originNode string
	httpClient *http.Client
}

// NewHTTPClient binds one origin node to one destination peer base URL.
func NewHTTPClient(baseURL string, originNode string, httpClient *http.Client) *HTTPClient {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &HTTPClient{baseURL: strings.TrimRight(baseURL, "/"), originNode: originNode, httpClient: httpClient}
}

// Health checks destination reachability and protocol identity.
func (c *HTTPClient) Health(ctx context.Context) (nodeproto.NodeHealthResponse, error) {
	var out nodeproto.NodeHealthResponse
	if err := c.doJSON(ctx, http.MethodGet, "/v1/node/health", nil, http.StatusOK, &out); err != nil {
		return nodeproto.NodeHealthResponse{}, err
	}
	return out, nil
}

// SubmitTask relays a local proxy task to the destination node.
func (c *HTTPClient) SubmitTask(ctx context.Context, req nodeproto.TaskSubmitRequest) (nodeproto.TaskAcceptedResponse, error) {
	if req.OriginNodeID == "" {
		req.OriginNodeID = c.originNode
	}
	var out nodeproto.TaskAcceptedResponse
	if err := c.doJSON(ctx, http.MethodPost, "/v1/node/tasks", req, http.StatusAccepted, &out); err != nil {
		return nodeproto.TaskAcceptedResponse{}, err
	}
	return out, nil
}

// ResumeTask forwards operator continuation to the destination task owner.
func (c *HTTPClient) ResumeTask(ctx context.Context, req nodeproto.TaskResumeRequest) error {
	if req.OriginNodeID == "" {
		req.OriginNodeID = c.originNode
	}
	path := fmt.Sprintf("/v1/node/tasks/%s/resume", url.PathEscape(req.DestinationTaskID))
	return c.doJSON(ctx, http.MethodPost, path, req, http.StatusAccepted, nil)
}

// CancelTask forwards operator cancellation to the destination task owner.
func (c *HTTPClient) CancelTask(ctx context.Context, req nodeproto.TaskCancelRequest) error {
	if req.OriginNodeID == "" {
		req.OriginNodeID = c.originNode
	}
	path := fmt.Sprintf("/v1/node/tasks/%s/cancel", url.PathEscape(req.DestinationTaskID))
	return c.doJSON(ctx, http.MethodPost, path, req, 0, nil)
}

// StreamTaskEvents drains the Phase 3 SSE event stream into typed frames.
func (c *HTTPClient) StreamTaskEvents(ctx context.Context, destinationTaskID string, originProxyTaskID string) ([]nodeproto.TaskEventFrame, error) {
	path := fmt.Sprintf("/v1/node/tasks/%s/events?origin_proxy_task_id=%s", url.PathEscape(destinationTaskID), url.QueryEscape(originProxyTaskID))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, decodeNodeError(resp)
	}
	var frames []nodeproto.TaskEventFrame
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var frame nodeproto.TaskEventFrame
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &frame); err != nil {
			return nil, err
		}
		frames = append(frames, frame)
	}
	return frames, scanner.Err()
}

// doJSON sends one JSON request and decodes either the expected response or a typed error.
func (c *HTTPClient) doJSON(ctx context.Context, method string, path string, body any, expectedStatus int, out any) error {
	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader(nil)
	} else {
		payload, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if expectedStatus != 0 && resp.StatusCode != expectedStatus {
		return decodeNodeError(resp)
	}
	if expectedStatus == 0 && (resp.StatusCode < 200 || resp.StatusCode >= 300) {
		return decodeNodeError(resp)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// decodeNodeError maps peer error payloads onto the node transport error type.
func decodeNodeError(resp *http.Response) error {
	var nodeErr nodeproto.ErrorResponse
	if err := json.NewDecoder(resp.Body).Decode(&nodeErr); err != nil || nodeErr.Code == "" {
		nodeErr = nodeproto.ErrorResponse{Code: "http_error", Message: resp.Status}
	}
	return &NodeError{StatusCode: resp.StatusCode, Response: nodeErr}
}
