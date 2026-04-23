package types

import (
	"context"
	crand "crypto/rand"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"
)

var (
	ulidEntropy io.Reader = ulid.Monotonic(crand.Reader, 0)
	ulidMu      sync.Mutex
)

func NewID() string {
	ulidMu.Lock()
	defer ulidMu.Unlock()
	return ulid.MustNew(ulid.Timestamp(time.Now().UTC()), ulidEntropy).String()
}

func NowUTC() time.Time {
	return time.Now().UTC()
}

type TaskStatus string

const (
	TaskStatusCreated       TaskStatus = "created"
	TaskStatusPendingLaunch TaskStatus = "pending_launch"
	TaskStatusLaunching     TaskStatus = "launching"
	TaskStatusReady         TaskStatus = "ready"
	TaskStatusDispatching   TaskStatus = "dispatching"
	TaskStatusRunning       TaskStatus = "running"
	TaskStatusAwaitingInput TaskStatus = "awaiting_input"
	TaskStatusCompleted     TaskStatus = "completed"
	TaskStatusFailed        TaskStatus = "failed"
	TaskStatusCancelled     TaskStatus = "cancelled"
)

func (s TaskStatus) IsTerminal() bool {
	switch s {
	case TaskStatusCompleted, TaskStatusFailed, TaskStatusCancelled:
		return true
	default:
		return false
	}
}

type TaskEventKind string

const (
	TaskEventTaskCreated          TaskEventKind = "task.created"
	TaskEventTaskRouted           TaskEventKind = "task.routed"
	TaskEventRuntimePendingLaunch TaskEventKind = "runtime.pending_launch"
	TaskEventRuntimeLaunching     TaskEventKind = "runtime.launching"
	TaskEventRuntimeReady         TaskEventKind = "runtime.ready"
	TaskEventTaskDispatching      TaskEventKind = "task.dispatching"
	TaskEventTaskRunning          TaskEventKind = "task.running"
	TaskEventTaskAwaitingInput    TaskEventKind = "task.awaiting_input"
	TaskEventTaskResumeRequested  TaskEventKind = "task.resume_requested"
	TaskEventTaskResumed          TaskEventKind = "task.resumed"
	TaskEventTaskCancelRequested  TaskEventKind = "task.cancel_requested"
	TaskEventTaskCompleted        TaskEventKind = "task.completed"
	TaskEventTaskFailed           TaskEventKind = "task.failed"
	TaskEventTaskCancelled        TaskEventKind = "task.cancelled"
	TaskEventArtifactCreated      TaskEventKind = "artifact.created"
	TaskEventRuntimeHealth        TaskEventKind = "runtime.health"
)

type EventSource string

const (
	EventSourceCore      EventSource = "core"
	EventSourceAdapter   EventSource = "adapter"
	EventSourceRuntime   EventSource = "runtime"
	EventSourceTransport EventSource = "transport"
	EventSourceStorage   EventSource = "storage"
	EventSourceAPI       EventSource = "api"
)

type DeliveryMode string

const (
	DeliveryModeStream DeliveryMode = "stream"
	DeliveryModeSync   DeliveryMode = "sync"
)

type DeliveryPolicy struct {
	Mode         DeliveryMode `json:"mode" yaml:"mode"`
	LaunchIfDown bool         `json:"launch_if_down" yaml:"launch_if_down"`
	TimeoutMS    *uint64      `json:"timeout_ms,omitempty" yaml:"timeout_ms,omitempty"`
}

func DefaultDeliveryPolicy() DeliveryPolicy {
	timeout := uint64(60000)
	return DeliveryPolicy{
		Mode:         DeliveryModeStream,
		LaunchIfDown: true,
		TimeoutMS:    &timeout,
	}
}

type TraceContext struct {
	TraceID      string  `json:"trace_id" yaml:"trace_id"`
	ParentTaskID *string `json:"parent_task_id,omitempty" yaml:"parent_task_id,omitempty"`
}

func DefaultTraceContext() TraceContext {
	return TraceContext{TraceID: NewID()}
}

type TaskCreateRequest struct {
	Sender         string          `json:"sender"`
	TargetAgentID  string          `json:"target_agent_id,omitempty"`
	Intent         string          `json:"intent"`
	Payload        map[string]any  `json:"payload"`
	RuntimeOptions map[string]any  `json:"runtime_options,omitempty"`
	ConversationID string          `json:"conversation_id,omitempty"`
	Delivery       *DeliveryPolicy `json:"delivery,omitempty"`
	Metadata       map[string]any  `json:"metadata,omitempty"`
}

func (r *TaskCreateRequest) Normalize() {
	if r.Sender == "" {
		r.Sender = "local"
	}
	if r.Payload == nil {
		r.Payload = map[string]any{}
	}
	if r.RuntimeOptions == nil {
		r.RuntimeOptions = map[string]any{}
	}
	if r.Metadata == nil {
		r.Metadata = map[string]any{}
	}
	if r.Delivery == nil {
		d := DefaultDeliveryPolicy()
		r.Delivery = &d
	}
	if r.ConversationID == "" {
		r.ConversationID = NewID()
	}
}

type TaskEnvelope struct {
	TaskID         string         `json:"task_id"`
	ConversationID string         `json:"conversation_id"`
	Sender         string         `json:"sender"`
	TargetAgentID  string         `json:"target_agent_id"`
	Intent         string         `json:"intent"`
	Payload        map[string]any `json:"payload"`
	RuntimeOptions map[string]any `json:"runtime_options,omitempty"`
	Delivery       DeliveryPolicy `json:"delivery"`
	Trace          TraceContext   `json:"trace"`
	Metadata       map[string]any `json:"metadata,omitempty"`
}

type RuntimeLease struct {
	LeaseID       string         `json:"lease_id"`
	TargetID      string         `json:"target_id"`
	SubcontextKey string         `json:"subcontext_key,omitempty"`
	ProcessID     string         `json:"process_id,omitempty"`
	Metadata      map[string]any `json:"metadata,omitempty"`
	CreatedAt     time.Time      `json:"created_at"`
	ReleasedAt    *time.Time     `json:"released_at,omitempty"`
}

type RemoteHandle struct {
	TaskID            string         `json:"task_id"`
	TargetID          string         `json:"target_id"`
	Binding           string         `json:"binding"`
	RemoteExecutionID string         `json:"remote_execution_id,omitempty"`
	RemoteSessionID   string         `json:"remote_session_id,omitempty"`
	AdapterState      map[string]any `json:"adapter_state,omitempty"`
}

type RemoteRef struct {
	Binding           string `json:"binding"`
	RemoteExecutionID string `json:"remote_execution_id,omitempty"`
	RemoteSessionID   string `json:"remote_session_id,omitempty"`
}

type SessionBinding struct {
	TargetID        string         `json:"target_id"`
	SubcontextKey   string         `json:"subcontext_key"`
	StickyKey       string         `json:"sticky_key"`
	Adapter         string         `json:"adapter"`
	RemoteSessionID string         `json:"remote_session_id"`
	Metadata        map[string]any `json:"metadata,omitempty"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
	LastUsedAt      time.Time      `json:"last_used_at"`
	LastActivityAt  time.Time      `json:"last_activity_at"`
}

type TaskError struct {
	Reason string `json:"reason"`
	Detail string `json:"detail,omitempty"`
}

type TaskRecord struct {
	TaskID            string         `json:"task_id"`
	ConversationID    string         `json:"conversation_id"`
	Sender            string         `json:"sender"`
	Intent            string         `json:"intent"`
	RequestedAgentID  string         `json:"requested_agent_id,omitempty"`
	ResolvedAgentID   string         `json:"resolved_agent_id,omitempty"`
	RuntimeOptions    map[string]any `json:"runtime_options,omitempty"`
	PayloadArtifactID string         `json:"payload_artifact_id,omitempty"`
	Status            TaskStatus     `json:"status"`
	Remote            *RemoteRef     `json:"remote,omitempty"`
	LastError         *TaskError     `json:"last_error,omitempty"`
	ResultArtifactID  string         `json:"result_artifact_id,omitempty"`
	CreatedAt         time.Time      `json:"created_at"`
	UpdatedAt         time.Time      `json:"updated_at"`
}

type TaskEvent struct {
	EventID   string         `json:"event_id"`
	TaskID    string         `json:"task_id"`
	Seq       int64          `json:"seq"`
	Kind      TaskEventKind  `json:"kind"`
	State     TaskStatus     `json:"state"`
	Source    EventSource    `json:"source"`
	Message   string         `json:"message,omitempty"`
	Data      map[string]any `json:"data,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
}

type ArtifactRef struct {
	ArtifactID string `json:"artifact_id"`
	MediaType  string `json:"media_type"`
	URL        string `json:"url"`
	SizeBytes  int64  `json:"size_bytes"`
	SHA256     string `json:"sha256"`
}

type LaunchRecord struct {
	LaunchID      string    `json:"launch_id"`
	TargetID      string    `json:"target_id"`
	SubcontextKey string    `json:"subcontext_key,omitempty"`
	Command       []string  `json:"command"`
	PID           string    `json:"pid,omitempty"`
	Status        string    `json:"status"`
	ErrorText     string    `json:"error_text,omitempty"`
	StartedAt     time.Time `json:"started_at"`
	EndedAt       time.Time `json:"ended_at,omitempty"`
}

type NetworkEnvelope struct {
	EnvelopeID     string         `json:"envelope_id"`
	ConversationID string         `json:"conversation_id"`
	TaskID         string         `json:"task_id"`
	SenderNode     string         `json:"sender_node"`
	RecipientNode  string         `json:"recipient_node"`
	MessageType    string         `json:"message_type"`
	Body           map[string]any `json:"body"`
	CreatedAt      time.Time      `json:"created_at"`
	Signature      []byte         `json:"signature,omitempty"`
}

type LaunchMode string

const (
	LaunchModeManaged  LaunchMode = "managed"
	LaunchModeOnDemand LaunchMode = "on_demand"
	LaunchModeExternal LaunchMode = "external"
)

type LaunchSpec struct {
	Mode     LaunchMode          `json:"mode" yaml:"mode"`
	Command  []string            `json:"command,omitempty" yaml:"command,omitempty"`
	Commands map[string][]string `json:"commands,omitempty" yaml:"commands,omitempty"`
}

type RuntimeSpec struct {
	TargetID     string         `json:"target_id" yaml:"-"`
	Adapter      string         `json:"adapter" yaml:"adapter"`
	Dialect      string         `json:"dialect,omitempty" yaml:"dialect,omitempty"`
	Endpoint     string         `json:"endpoint,omitempty" yaml:"endpoint,omitempty"`
	Healthcheck  string         `json:"healthcheck,omitempty" yaml:"healthcheck,omitempty"`
	Launch       LaunchSpec     `json:"launch" yaml:"launch"`
	Defaults     map[string]any `json:"defaults,omitempty" yaml:"defaults,omitempty"`
	Capabilities []string       `json:"capabilities,omitempty" yaml:"capabilities,omitempty"`
}

type RouteSpec struct {
	Runtime        string         `json:"runtime,omitempty" yaml:"runtime,omitempty"`
	RuntimeOptions map[string]any `json:"runtime_options,omitempty" yaml:"runtime_options,omitempty"`
}

type RegistryFile struct {
	Runtimes map[string]RuntimeSpec `json:"runtimes" yaml:"runtimes"`
	Routes   map[string]RouteSpec   `json:"routes,omitempty" yaml:"routes,omitempty"`
}

// RuntimeAdapter intentionally uses shared verbs like Submit/Resume/Cancel so
// the orchestrator can stay generic; adapter-specific helper names live behind
// this interface.
type RuntimeAdapter interface {
	AdapterName() string
	Capabilities(ctx context.Context) (map[string]any, error)
	EnsureReady(ctx context.Context, targetID string, options map[string]any) (RuntimeLease, error)
	Submit(ctx context.Context, task TaskEnvelope, lease RuntimeLease) (RemoteHandle, error)
	StreamEvents(ctx context.Context, handle RemoteHandle) (<-chan TaskEvent, <-chan error)
	Resume(ctx context.Context, handle RemoteHandle, payload map[string]any) error
	Cancel(ctx context.Context, handle RemoteHandle) error
	Health(ctx context.Context, targetID string, options map[string]any) (map[string]any, error)
	RehydrateHandle(task TaskRecord, spec RuntimeSpec) (RemoteHandle, error)
	SubcontextKey(spec RuntimeSpec, runtimeOptions map[string]any) string
}

type TransportAdapter interface {
	TransportName() string
	Publish(ctx context.Context, envelope NetworkEnvelope) error
	Subscribe(ctx context.Context) (<-chan NetworkEnvelope, <-chan error)
}

type DiscoveryProvider interface {
	ResolveRuntime(ctx context.Context, targetID string) (RuntimeSpec, error)
	ListRuntimes(ctx context.Context) ([]RuntimeSpec, error)
}

type IdentityProvider interface {
	NodeID(ctx context.Context) (string, error)
	Sign(ctx context.Context, payload []byte) ([]byte, error)
	Verify(ctx context.Context, nodeID string, payload []byte, signature []byte) (bool, error)
}

func ValidateIntent(intent string) error {
	if intent == "" {
		return fmt.Errorf("intent is required")
	}
	return nil
}
