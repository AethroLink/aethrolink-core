package adapters

import (
	"testing"

	atypes "github.com/aethrolink/aethrolink-core/pkg/types"
)

func TestACPHermesSubcontextKeyUsesExecutorInsteadOfProfile(t *testing.T) {
	adapter := &ACPAdapter{dialects: defaultACPDialects()}
	spec := atypes.RuntimeSpec{TargetID: "hermes_coder", Adapter: "acp", Dialect: "hermes", Defaults: map[string]any{"executor": "coder"}}
	if got := adapter.SubcontextKey(spec, map[string]any{}); got != "executor:coder" {
		t.Fatalf("expected executor-scoped subcontext, got %q", got)
	}
}
