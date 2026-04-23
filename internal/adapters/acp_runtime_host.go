package adapters

import (
	"context"
	"fmt"

	"github.com/aethrolink/aethrolink-core/internal/runtime"
	atypes "github.com/aethrolink/aethrolink-core/pkg/types"
)

type acpRuntimeHost struct {
	manager *runtime.Manager
}

func newACPRuntimeHost(manager *runtime.Manager) atypes.LocalRuntimeHost {
	return &acpRuntimeHost{manager: manager}
}

func (h *acpRuntimeHost) EnsureRunning(ctx context.Context, spec atypes.RuntimeSpec, subcontextKey string) (atypes.RuntimeLease, error) {
	if len(spec.Launch.Command) == 0 {
		return atypes.RuntimeLease{}, fmt.Errorf("missing launch command for target %s", spec.TargetID)
	}
	lease, _, err := h.manager.EnsureStdioWorker(ctx, spec.TargetID, subcontextKey, spec.Launch.Command)
	return lease, err
}

func (h *acpRuntimeHost) Health(ctx context.Context, spec atypes.RuntimeSpec, subcontextKey string) (map[string]any, error) {
	return h.manager.Health(spec.TargetID, subcontextKey), nil
}

func (h *acpRuntimeHost) Stop(ctx context.Context, targetID string, subcontextKey string) error {
	return h.manager.Stop(ctx, targetID, subcontextKey)
}
