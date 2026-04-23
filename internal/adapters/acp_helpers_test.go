package adapters

import (
	"testing"
	"time"

	atypes "github.com/aethrolink/aethrolink-core/pkg/types"
)

func TestNormalizeACPWorkspaceBindingDefaultsCWD(t *testing.T) {
	binding := normalizeACPWorkspaceBinding(atypes.WorkspaceBinding{})
	if binding.CWD != "." {
		t.Fatalf("expected default cwd '.', got %q", binding.CWD)
	}
}

func TestNormalizeACPWorkspaceBindingKeepsExplicitCWD(t *testing.T) {
	binding := normalizeACPWorkspaceBinding(atypes.WorkspaceBinding{CWD: "/tmp/work"})
	if binding.CWD != "/tmp/work" {
		t.Fatalf("expected explicit cwd to survive, got %q", binding.CWD)
	}
}

func TestACPPromptTimeoutUsesIdleTimeoutWhenLongEnough(t *testing.T) {
	if got := acpPromptTimeout(2 * time.Minute); got != 2*time.Minute {
		t.Fatalf("expected prompt timeout to keep long idle timeout, got %s", got)
	}
}

func TestACPPromptTimeoutKeepsOneMinuteFloor(t *testing.T) {
	if got := acpPromptTimeout(15 * time.Second); got != time.Minute {
		t.Fatalf("expected one minute floor, got %s", got)
	}
}
