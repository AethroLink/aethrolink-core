package adapters

import (
	"context"
	"sync"

	"github.com/aethrolink/aethrolink-core/internal/adaptersupport"
	"github.com/aethrolink/aethrolink-core/internal/runtime"
	atypes "github.com/aethrolink/aethrolink-core/pkg/types"
)

// acpRun keeps each live ACP execution wired to its event and error streams.
type acpRun struct {
	events chan atypes.TaskEvent
	errs   chan error
}

// ACPAdapter is the orchestrator-facing adapter facade. Internally it composes
// a local runtime host, ACP session transport, sticky-session store, and
// runtime-specific dialects.
type ACPAdapter struct {
	discovery atypes.DiscoveryProvider
	host      atypes.LocalRuntimeHost
	transport atypes.LocalSessionTransport
	runtime   *runtime.Manager
	sessions  *adaptersupport.SessionCoordinator
	dialects  map[string]acpDialect
	mu        sync.Mutex
	runs      map[string]*acpRun
}

func NewACPAdapter(discovery atypes.DiscoveryProvider, runtimeManager *runtime.Manager) *ACPAdapter {
	return &ACPAdapter{
		discovery: discovery,
		host:      newACPRuntimeHost(runtimeManager),
		transport: newACPSessionTransport(runtimeManager),
		runtime:   runtimeManager,
		sessions:  adaptersupport.NewSessionCoordinator(runtimeManager.Store()),
		dialects:  defaultACPDialects(),
		runs:      map[string]*acpRun{},
	}
}

func (a *ACPAdapter) AdapterName() string { return "acp" }

func (a *ACPAdapter) Capabilities(context.Context) (map[string]any, error) {
	return map[string]any{"adapter": "acp", "mode": "acp_bridge"}, nil
}
