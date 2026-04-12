package config

import (
	"context"
	"path/filepath"
	"testing"
)

func TestLoadRegistryAndRouteIntent(t *testing.T) {
	registry, err := LoadRegistry(filepath.Join("..", "..", "examples", "registry.yaml"))
	if err != nil {
		t.Fatalf("load registry: %v", err)
	}
	spec, err := registry.ResolveRuntime(context.Background(), "hermes")
	if err != nil {
		t.Fatalf("resolve runtime: %v", err)
	}
	if spec.Adapter != "hermes" {
		t.Fatalf("expected hermes adapter, got %s", spec.Adapter)
	}
	route, ok := registry.RouteForIntent("code.patch")
	if !ok {
		t.Fatalf("expected route for code.patch")
	}
	if route.Runtime != "hermes" {
		t.Fatalf("expected hermes route, got %s", route.Runtime)
	}
}
