package types

import (
	"context"
	"time"
)

// WorkspaceBinding describes how a local runtime should treat cwd, client-owned
// tools, and session-scoped integration surfaces such as MCP.
type WorkspaceBinding struct {
	CWD            string           `json:"cwd,omitempty"`
	AttachMCP      bool             `json:"attach_mcp,omitempty"`
	MCPServers     []map[string]any `json:"mcp_servers,omitempty"`
	ClientOwnsFS   bool             `json:"client_owns_fs,omitempty"`
	ClientOwnsExec bool             `json:"client_owns_exec,omitempty"`
	ApprovalMode   string           `json:"approval_mode,omitempty"`
	Metadata       map[string]any   `json:"metadata,omitempty"`
}

type LocalRuntimeEventKind string

const (
	LocalRuntimeEventMessage     LocalRuntimeEventKind = "message"
	LocalRuntimeEventTool        LocalRuntimeEventKind = "tool"
	LocalRuntimeEventApproval    LocalRuntimeEventKind = "approval"
	LocalRuntimeEventArtifact    LocalRuntimeEventKind = "artifact"
	LocalRuntimeEventProgress    LocalRuntimeEventKind = "progress"
	LocalRuntimeEventStateChange LocalRuntimeEventKind = "state_change"
	LocalRuntimeEventTerminal    LocalRuntimeEventKind = "terminal"
)

// LocalRuntimeEvent is the runtime-facing event shape used before the
// orchestrator translates it into persisted task events.
type LocalRuntimeEvent struct {
	Kind      LocalRuntimeEventKind `json:"kind"`
	State     TaskStatus            `json:"state"`
	Message   string                `json:"message,omitempty"`
	Data      map[string]any        `json:"data,omitempty"`
	CreatedAt time.Time             `json:"created_at"`
}

// LocalRuntimeHost owns local process/daemon lifecycle for a runtime scope.
type LocalRuntimeHost interface {
	EnsureRunning(ctx context.Context, spec RuntimeSpec, subcontextKey string) (RuntimeLease, error)
	Health(ctx context.Context, spec RuntimeSpec, subcontextKey string) (map[string]any, error)
	Stop(ctx context.Context, runtimeID string, subcontextKey string) error
}

// LocalSessionTransport owns wire-level session operations for local runtimes.
type LocalSessionTransport interface {
	Initialize(ctx context.Context, runtimeID, subcontextKey string, payload map[string]any, timeout time.Duration) error
	OpenSession(ctx context.Context, runtimeID, subcontextKey string, payload map[string]any, timeout time.Duration) (string, error)
	LoadSession(ctx context.Context, runtimeID, subcontextKey string, sessionID string, payload map[string]any, timeout time.Duration) (string, error)
	Prompt(ctx context.Context, runtimeID, subcontextKey string, sessionID string, payload map[string]any, timeout time.Duration) error
	Resume(ctx context.Context, runtimeID, subcontextKey string, sessionID string, payload map[string]any) error
	Cancel(ctx context.Context, runtimeID, subcontextKey string, sessionID string) error
	Stream(ctx context.Context, runtimeID, subcontextKey string) (<-chan map[string]any, func(), error)
}

// LocalRuntimeDialect owns runtime-specific scoping, workspace binding, request
// shaping, and event translation for local-first runtimes.
type LocalRuntimeDialect interface {
	Name() string
	SubcontextKey(options map[string]any) string
	StickyKey(task TaskEnvelope) string
	WorkspaceBinding(task TaskEnvelope) WorkspaceBinding
}
