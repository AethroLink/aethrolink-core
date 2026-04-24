package core

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aethrolink/aethrolink-core/internal/storage"
	atypes "github.com/aethrolink/aethrolink-core/pkg/types"
)

type staticDiscovery struct {
	resolve atypes.RuntimeSpec
	list    []atypes.RuntimeSpec
	err     error
}

// ResolveRuntime returns the configured runtime so routing tests stay focused on core policy.
func (d staticDiscovery) ResolveRuntime(context.Context, string) (atypes.RuntimeSpec, error) {
	if d.err != nil {
		return atypes.RuntimeSpec{}, d.err
	}
	return d.resolve, nil
}

// ListRuntimes returns a defensive copy to keep tests from mutating fixture state.
func (d staticDiscovery) ListRuntimes(context.Context) ([]atypes.RuntimeSpec, error) {
	return append([]atypes.RuntimeSpec(nil), d.list...), nil
}

func TestNewOrchestratorFailsWhenRestartReconciliationFails(t *testing.T) {
	t.Helper()
	tmp := t.TempDir()
	store, err := storage.Open(filepath.Join(tmp, "aethrolink.db"), filepath.Join(tmp, "artifacts"), "http://127.0.0.1")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	// A closed store forces boot reconciliation to fail instead of being skipped.
	_, err = NewOrchestrator(nil, store, nil, nil)
	if err == nil {
		t.Fatal("expected constructor to fail when restart reconciliation fails")
	}
	if !strings.Contains(err.Error(), "mark interrupted threads on restart") {
		t.Fatalf("expected reconciliation error, got %v", err)
	}
}

func TestRouteRequestRejectsExplicitRemoteRuntimeBeforeRelayTransport(t *testing.T) {
	discovery := staticDiscovery{resolve: atypes.RuntimeSpec{TargetID: "researcher", Owner: atypes.TargetOwnerRemote, Capabilities: []string{"research.summary"}}}
	orchestrator := &Orchestrator{discovery: discovery}

	_, _, err := orchestrator.routeRequest(context.Background(), atypes.TaskCreateRequest{TargetAgentID: "researcher", Intent: "research.summary"})
	if !errors.Is(err, ErrTargetAgentNotFound) {
		t.Fatalf("expected remote target to be rejected before dispatch, got %v", err)
	}
}

func TestRouteRequestIgnoresRemoteRuntimesDuringIntentRouting(t *testing.T) {
	discovery := staticDiscovery{list: []atypes.RuntimeSpec{{TargetID: "researcher", Owner: atypes.TargetOwnerRemote, Capabilities: []string{"research.summary"}}}}
	orchestrator := &Orchestrator{discovery: discovery}

	_, _, err := orchestrator.routeRequest(context.Background(), atypes.TaskCreateRequest{Intent: "research.summary"})
	if !errors.Is(err, ErrRouteNotFound) {
		t.Fatalf("expected remote target to be ignored during dispatch routing, got %v", err)
	}
}
