package main

import (
	"flag"
	"fmt"

	atypes "github.com/aethrolink/aethrolink-core/pkg/types"
)

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
