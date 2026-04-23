package adapters

import (
	"context"

	atypes "github.com/aethrolink/aethrolink-core/pkg/types"
)

func (a *ACPAdapter) Resume(ctx context.Context, handle atypes.RemoteHandle, payload map[string]any) error {
	dialect, err := a.dialectFromHandle(handle)
	if err != nil {
		return err
	}
	resumePayload, err := dialect.ResumePayload(handle.RemoteSessionID, payload)
	if err != nil {
		return err
	}
	return a.transport.Resume(ctx, handle.TargetID, dialect.SubcontextKey(handle.AdapterState), handle.RemoteSessionID, resumePayload)
}

func (a *ACPAdapter) Cancel(ctx context.Context, handle atypes.RemoteHandle) error {
	dialect, err := a.dialectFromHandle(handle)
	if err != nil {
		return err
	}
	return a.transport.Cancel(ctx, handle.TargetID, dialect.SubcontextKey(handle.AdapterState), handle.RemoteSessionID)
}

func (a *ACPAdapter) Health(ctx context.Context, targetID string, options map[string]any) (map[string]any, error) {
	spec, err := a.discovery.ResolveRuntime(ctx, targetID)
	if err != nil {
		return nil, err
	}
	dialect, err := a.resolveACPDialect(spec)
	if err != nil {
		return nil, err
	}
	resolvedOptions := mergeAdapterOptions(spec.Defaults, options)
	return a.host.Health(ctx, spec, dialect.SubcontextKey(resolvedOptions))
}

func (a *ACPAdapter) RehydrateHandle(task atypes.TaskRecord, spec atypes.RuntimeSpec) (atypes.RemoteHandle, error) {
	dialect, err := a.resolveACPDialect(spec)
	if err != nil {
		return atypes.RemoteHandle{}, err
	}
	handle := atypes.RemoteHandle{TaskID: task.TaskID, TargetID: task.ResolvedAgentID}
	if task.Remote != nil {
		handle.Binding = task.Remote.Binding
		handle.RemoteExecutionID = task.Remote.RemoteExecutionID
		handle.RemoteSessionID = task.Remote.RemoteSessionID
	}
	handle.AdapterState = dialect.RehydrateState(task)
	return handle, nil
}

func (a *ACPAdapter) SubcontextKey(spec atypes.RuntimeSpec, runtimeOptions map[string]any) string {
	dialect, err := a.resolveACPDialect(spec)
	if err != nil {
		return ""
	}
	return dialect.SubcontextKey(mergeAdapterOptions(spec.Defaults, runtimeOptions))
}
