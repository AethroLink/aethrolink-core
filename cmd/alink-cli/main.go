package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	atypes "github.com/aethrolink/aethrolink-core/pkg/types"
)

type cli struct {
	client *http.Client
	stdout io.Writer
	stderr io.Writer
}

type agentState struct {
	AgentID string `json:"agent_id"`
}

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	c := cli{
		client: &http.Client{Timeout: 30 * time.Second},
		stdout: stdout,
		stderr: stderr,
	}
	return c.run(args)
}

func (c cli) run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: alink-cli <register|ensure-registered|heartbeat|call|task-get|task-events|thread-create|thread-get|thread-continue|thread-turns|agents|targets>")
	}
	switch args[0] {
	case "register":
		return c.runRegister(args[1:])
	case "ensure-registered":
		return c.runEnsureRegistered(args[1:])
	case "heartbeat":
		return c.runHeartbeat(args[1:])
	case "call":
		return c.runCall(args[1:])
	case "task-get":
		return c.runTaskGet(args[1:])
	case "task-events":
		return c.runTaskEvents(args[1:])
	case "thread-create":
		return c.runThreadCreate(args[1:])
	case "thread-get":
		return c.runThreadGet(args[1:])
	case "thread-continue":
		return c.runThreadContinue(args[1:])
	case "thread-turns":
		return c.runThreadTurns(args[1:])
	case "agents":
		return c.runAgents(args[1:])
	case "targets":
		return c.runTargets(args[1:])
	default:
		return fmt.Errorf("unknown command: %s", args[0])
	}
}

func (c cli) runRegister(args []string) error {
	fs := flag.NewFlagSet("register", flag.ContinueOnError)
	fs.SetOutput(c.stderr)
	server := fs.String("server", "http://127.0.0.1:7777", "alink-core base URL")
	statePath := fs.String("state-file", defaultStatePath(), "local state file")
	agentID := fs.String("agent-id", "", "explicit agent id")
	displayName := fs.String("display-name", "", "agent display name")
	transportKind := fs.String("transport-kind", "local_managed", "transport kind")
	endpoint := fs.String("endpoint", "", "agent endpoint")
	adapter := fs.String("adapter", "", "runtime adapter kind")
	dialect := fs.String("dialect", "", "runtime dialect")
	healthcheck := fs.String("healthcheck", "", "runtime healthcheck URL")
	launchMode := fs.String("launch-mode", "managed", "launch mode")
	launchCommand := fs.String("launch-command", "", "launch command string")
	defaults := fs.String("defaults", "", "comma-separated key=value runtime defaults")
	capabilities := fs.String("capabilities", "", "comma-separated capabilities")
	stickyMode := fs.String("sticky-mode", "", "sticky mode")
	leaseTTL := fs.Int("lease-ttl-seconds", int(atypes.DefaultAgentLeaseTTL/time.Second), "lease ttl in seconds")
	if err := fs.Parse(args); err != nil {
		return err
	}
	req := atypes.AgentRegistrationRequest{
		AgentID:       *agentID,
		DisplayName:   *displayName,
		TransportKind: *transportKind,
		Endpoint:      *endpoint,
		Adapter:       *adapter,
		Dialect:       *dialect,
		Healthcheck:   *healthcheck,
		Launch: atypes.LaunchSpec{
			Mode:    atypes.LaunchMode(*launchMode),
			Command: shellWords(*launchCommand),
		},
		Defaults:        parseKeyValueCSV(*defaults),
		Capabilities:    csvList(*capabilities),
		StickyMode:      *stickyMode,
		LeaseTTLSeconds: *leaseTTL,
	}
	body, err := c.postJSON(joinURL(*server, "/v1/agents/register"), req)
	if err != nil {
		return err
	}
	var resp struct {
		Agent atypes.AgentRecord `json:"agent"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return err
	}
	if err := saveAgentState(*statePath, agentState{AgentID: resp.Agent.AgentID}); err != nil {
		return err
	}
	_, _ = fmt.Fprintln(c.stdout, string(body))
	return nil
}

func (c cli) runEnsureRegistered(args []string) error {
	fs := flag.NewFlagSet("ensure-registered", flag.ContinueOnError)
	fs.SetOutput(c.stderr)
	server := fs.String("server", "http://127.0.0.1:7777", "alink-core base URL")
	statePath := fs.String("state-file", defaultStatePath(), "local state file")
	displayName := fs.String("display-name", "", "agent display name")
	transportKind := fs.String("transport-kind", "local_managed", "transport kind")
	endpoint := fs.String("endpoint", "", "agent endpoint")
	adapter := fs.String("adapter", "", "runtime adapter kind")
	dialect := fs.String("dialect", "", "runtime dialect")
	healthcheck := fs.String("healthcheck", "", "runtime healthcheck URL")
	launchMode := fs.String("launch-mode", "managed", "launch mode")
	launchCommand := fs.String("launch-command", "", "launch command string")
	defaults := fs.String("defaults", "", "comma-separated key=value runtime defaults")
	capabilities := fs.String("capabilities", "", "comma-separated capabilities")
	stickyMode := fs.String("sticky-mode", "", "sticky mode")
	leaseTTL := fs.Int("lease-ttl-seconds", int(atypes.DefaultAgentLeaseTTL/time.Second), "lease ttl in seconds")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if state, err := loadAgentState(*statePath); err == nil && state.AgentID != "" {
		body, err := c.get(joinURL(*server, "/v1/agents/"+state.AgentID))
		if err == nil {
			_, _ = fmt.Fprintln(c.stdout, string(body))
			return nil
		}
	}

	return c.runRegister([]string{
		"--server", *server,
		"--state-file", *statePath,
		"--display-name", *displayName,
		"--transport-kind", *transportKind,
		"--endpoint", *endpoint,
		"--adapter", *adapter,
		"--dialect", *dialect,
		"--healthcheck", *healthcheck,
		"--launch-mode", *launchMode,
		"--launch-command", *launchCommand,
		"--defaults", *defaults,
		"--capabilities", *capabilities,
		"--sticky-mode", *stickyMode,
		"--lease-ttl-seconds", fmt.Sprintf("%d", *leaseTTL),
	})
}

func (c cli) runHeartbeat(args []string) error {
	fs := flag.NewFlagSet("heartbeat", flag.ContinueOnError)
	fs.SetOutput(c.stderr)
	server := fs.String("server", "http://127.0.0.1:7777", "alink-core base URL")
	statePath := fs.String("state-file", defaultStatePath(), "local state file")
	agentID := fs.String("agent-id", "", "explicit agent id")
	leaseTTL := fs.Int("lease-ttl-seconds", int(atypes.DefaultAgentLeaseTTL/time.Second), "lease ttl in seconds")
	if err := fs.Parse(args); err != nil {
		return err
	}
	id, err := resolveAgentID(*agentID, *statePath)
	if err != nil {
		return err
	}
	body, err := c.postJSON(joinURL(*server, "/v1/agents/"+id+"/heartbeat"), atypes.AgentHeartbeatRequest{LeaseTTLSeconds: *leaseTTL})
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintln(c.stdout, string(body))
	return nil
}

func (c cli) runCall(args []string) error {
	fs := flag.NewFlagSet("call", flag.ContinueOnError)
	fs.SetOutput(c.stderr)
	server := fs.String("server", "http://127.0.0.1:7777", "alink-core base URL")
	statePath := fs.String("state-file", defaultStatePath(), "local state file")
	agentID := fs.String("agent-id", "", "explicit agent id")
	targetAgentID := fs.String("target-agent-id", "", "target agent id")
	intent := fs.String("intent", "", "task intent")
	text := fs.String("text", "", "text payload")
	conversationID := fs.String("conversation-id", "", "conversation id")
	heartbeat := fs.Bool("heartbeat", false, "refresh agent lease before submitting")
	if err := fs.Parse(args); err != nil {
		return err
	}
	sender := "local"
	if id, err := resolveAgentID(*agentID, *statePath); err == nil && id != "" {
		sender = id
		if *heartbeat {
			if _, err := c.postJSON(joinURL(*server, "/v1/agents/"+id+"/heartbeat"), atypes.AgentHeartbeatRequest{}); err != nil {
				return err
			}
		}
	}
	req := atypes.TaskCreateRequest{
		Sender:         sender,
		TargetAgentID:  *targetAgentID,
		Intent:         *intent,
		Payload:        map[string]any{"text": *text},
		ConversationID: *conversationID,
	}
	body, err := c.postJSON(joinURL(*server, "/v1/tasks"), req)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintln(c.stdout, string(body))
	return nil
}

func (c cli) runTaskGet(args []string) error {
	fs := flag.NewFlagSet("task-get", flag.ContinueOnError)
	fs.SetOutput(c.stderr)
	server := fs.String("server", "http://127.0.0.1:7777", "alink-core base URL")
	taskID := fs.String("task-id", "", "task id")
	if err := fs.Parse(args); err != nil {
		return err
	}
	body, err := c.get(joinURL(*server, "/v1/tasks/"+*taskID))
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintln(c.stdout, string(body))
	return nil
}

func (c cli) runTaskEvents(args []string) error {
	fs := flag.NewFlagSet("task-events", flag.ContinueOnError)
	fs.SetOutput(c.stderr)
	server := fs.String("server", "http://127.0.0.1:7777", "alink-core base URL")
	taskID := fs.String("task-id", "", "task id")
	if err := fs.Parse(args); err != nil {
		return err
	}
	body, err := c.get(joinURL(*server, "/v1/tasks/"+*taskID+"/events"))
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintln(c.stdout, string(body))
	return nil
}

func (c cli) runThreadCreate(args []string) error {
	fs := flag.NewFlagSet("thread-create", flag.ContinueOnError)
	fs.SetOutput(c.stderr)
	server := fs.String("server", "http://127.0.0.1:7777", "alink-core base URL")
	agentAID := fs.String("agent-a-id", "", "first thread agent id")
	agentBID := fs.String("agent-b-id", "", "second thread agent id")
	continuityKey := fs.String("continuity-key", "", "explicit continuity key")
	if err := fs.Parse(args); err != nil {
		return err
	}
	body, err := c.postJSON(joinURL(*server, "/v1/threads"), atypes.ThreadCreateRequest{AgentAID: *agentAID, AgentBID: *agentBID, ContinuityKey: *continuityKey})
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintln(c.stdout, string(body))
	return nil
}

func (c cli) runThreadGet(args []string) error {
	fs := flag.NewFlagSet("thread-get", flag.ContinueOnError)
	fs.SetOutput(c.stderr)
	server := fs.String("server", "http://127.0.0.1:7777", "alink-core base URL")
	threadID := fs.String("thread-id", "", "thread id")
	if err := fs.Parse(args); err != nil {
		return err
	}
	body, err := c.get(joinURL(*server, "/v1/threads/"+*threadID+"/inspect"))
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintln(c.stdout, string(body))
	return nil
}

func (c cli) runThreadContinue(args []string) error {
	fs := flag.NewFlagSet("thread-continue", flag.ContinueOnError)
	fs.SetOutput(c.stderr)
	server := fs.String("server", "http://127.0.0.1:7777", "alink-core base URL")
	threadID := fs.String("thread-id", "", "thread id")
	sender := fs.String("sender", "", "explicit sender agent id")
	targetAgentID := fs.String("target-agent-id", "", "explicit target agent id")
	intent := fs.String("intent", "", "task intent")
	text := fs.String("text", "", "text payload")
	conversationID := fs.String("conversation-id", "", "conversation id override")
	if err := fs.Parse(args); err != nil {
		return err
	}
	body, err := c.postJSON(joinURL(*server, "/v1/threads/"+*threadID+"/continue"), atypes.ThreadContinueRequest{
		Sender:         *sender,
		TargetAgentID:  *targetAgentID,
		Intent:         *intent,
		Payload:        map[string]any{"text": *text},
		ConversationID: *conversationID,
	})
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintln(c.stdout, string(body))
	return nil
}

func (c cli) runThreadTurns(args []string) error {
	fs := flag.NewFlagSet("thread-turns", flag.ContinueOnError)
	fs.SetOutput(c.stderr)
	server := fs.String("server", "http://127.0.0.1:7777", "alink-core base URL")
	threadID := fs.String("thread-id", "", "thread id")
	if err := fs.Parse(args); err != nil {
		return err
	}
	body, err := c.get(joinURL(*server, "/v1/threads/"+*threadID+"/turns"))
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintln(c.stdout, string(body))
	return nil
}

func (c cli) runAgents(args []string) error {
	fs := flag.NewFlagSet("agents", flag.ContinueOnError)
	fs.SetOutput(c.stderr)
	server := fs.String("server", "http://127.0.0.1:7777", "alink-core base URL")
	if err := fs.Parse(args); err != nil {
		return err
	}
	body, err := c.get(joinURL(*server, "/v1/agents"))
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintln(c.stdout, string(body))
	return nil
}

func (c cli) runTargets(args []string) error {
	fs := flag.NewFlagSet("targets", flag.ContinueOnError)
	fs.SetOutput(c.stderr)
	server := fs.String("server", "http://127.0.0.1:7777", "alink-core base URL")
	if err := fs.Parse(args); err != nil {
		return err
	}
	body, err := c.get(joinURL(*server, "/v1/targets"))
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintln(c.stdout, string(body))
	return nil
}

func (c cli) postJSON(url string, payload any) ([]byte, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	resp, err := c.client.Post(url, "application/json", bytes.NewReader(encoded))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("http %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return body, nil
}

func (c cli) get(url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("http %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return body, nil
}

func defaultStatePath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".aethrolink-agent.json"
	}
	return filepath.Join(home, ".aethrolink", "agent.json")
}

func saveAgentState(path string, state agentState) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	encoded, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return os.WriteFile(path, encoded, 0o644)
}

func loadAgentState(path string) (agentState, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return agentState{}, err
	}
	var state agentState
	if err := json.Unmarshal(body, &state); err != nil {
		return agentState{}, err
	}
	return state, nil
}

func resolveAgentID(explicit, statePath string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	state, err := loadAgentState(statePath)
	if err != nil {
		return "", fmt.Errorf("resolve agent id: %w", err)
	}
	if state.AgentID == "" {
		return "", errors.New("resolve agent id: empty agent_id in state file")
	}
	return state.AgentID, nil
}

func csvList(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return []string{}
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func parseKeyValueCSV(raw string) map[string]any {
	if strings.TrimSpace(raw) == "" {
		return map[string]any{}
	}
	out := map[string]any{}
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		key, value, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key != "" {
			out[key] = value
		}
	}
	return out
}

func shellWords(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	return strings.Fields(raw)
}

func joinURL(base, path string) string {
	return strings.TrimRight(base, "/") + path
}
