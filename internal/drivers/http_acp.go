package drivers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type HTTPACPDriver struct {
	client *http.Client
}

func NewHTTPACPDriver() *HTTPACPDriver {
	return &HTTPACPDriver{client: &http.Client{Timeout: 5 * time.Second}}
}

func (d *HTTPACPDriver) CreateRun(endpoint string, body map[string]any, headers map[string]any) (map[string]any, error) {
	return d.doJSON(http.MethodPost, strings.TrimRight(endpoint, "/")+"/runs", body, headers)
}

func (d *HTTPACPDriver) GetRun(endpoint, runID string) (map[string]any, error) {
	return d.doJSON(http.MethodGet, strings.TrimRight(endpoint, "/")+"/runs/"+runID, nil, nil)
}

func (d *HTTPACPDriver) ResumeRun(endpoint, runID string, payload map[string]any) error {
	_, err := d.doJSON(http.MethodPost, strings.TrimRight(endpoint, "/")+"/runs/"+runID+"/resume", payload, nil)
	return err
}

func (d *HTTPACPDriver) CancelRun(endpoint, runID string) error {
	_, err := d.doJSON(http.MethodPost, strings.TrimRight(endpoint, "/")+"/runs/"+runID+"/cancel", nil, nil)
	return err
}

func (d *HTTPACPDriver) PollRun(endpoint, runID string) (<-chan map[string]any, <-chan error) {
	out := make(chan map[string]any, 16)
	errCh := make(chan error, 1)
	go func() {
		defer close(out)
		defer close(errCh)
		lastStatus := ""
		for {
			run, err := d.GetRun(endpoint, runID)
			if err != nil {
				errCh <- err
				return
			}
			status, _ := run["status"].(string)
			if status != lastStatus {
				out <- run
				lastStatus = status
			}
			if status == "completed" || status == "failed" || status == "cancelled" {
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
	}()
	return out, errCh
}

func (d *HTTPACPDriver) doJSON(method, url string, payload map[string]any, headers map[string]any) (map[string]any, error) {
	var bodyReader *bytes.Reader
	if payload == nil {
		bodyReader = bytes.NewReader(nil)
	} else {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		bodyReader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequest(method, url, bodyReader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	for key, value := range headers {
		if s, ok := value.(string); ok {
			req.Header.Set(key, s)
		}
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("http acp status %d", resp.StatusCode)
	}
	if resp.ContentLength == 0 {
		return map[string]any{}, nil
	}
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if out == nil {
		out = map[string]any{}
	}
	return out, nil
}
