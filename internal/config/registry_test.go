package config

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadRegistryAndRouteIntent(t *testing.T) {
	registryPath := filepath.Join(t.TempDir(), "registry.yaml")
	content := []byte("runtimes:\n  mock_hermes:\n    adapter: mock_hermes\n    launch:\n      mode: managed\n      commands:\n        coder: [\"go\", \"run\", \"./cmd/fake-acp-client-agent\"]\n    defaults:\n      executor: coder\n    capabilities:\n      - code.patch\nroutes:\n  code.patch:\n    runtime: mock_hermes\n")
	if err := os.WriteFile(registryPath, content, 0o644); err != nil {
		t.Fatalf("write registry: %v", err)
	}
	registry, err := LoadRegistry(registryPath)
	if err != nil {
		t.Fatalf("load registry: %v", err)
	}
	spec, err := registry.ResolveRuntime(context.Background(), "mock_hermes")
	if err != nil {
		t.Fatalf("resolve runtime: %v", err)
	}
	if spec.Adapter != "mock_hermes" {
		t.Fatalf("expected mock_hermes adapter, got %s", spec.Adapter)
	}
	route, ok := registry.RouteForIntent("code.patch")
	if !ok {
		t.Fatalf("expected route for code.patch")
	}
	if route.Runtime != "mock_hermes" {
		t.Fatalf("expected mock_hermes route, got %s", route.Runtime)
	}
}

func TestLoadLiveRegistryConfig(t *testing.T) {
	registry, err := LoadRegistry(filepath.Join("..", "..", "configs", "registry.yaml"))
	if err != nil {
		t.Fatalf("load live registry: %v", err)
	}
	spec, err := registry.ResolveRuntime(context.Background(), "core")
	if err != nil {
		t.Fatalf("resolve live runtime: %v", err)
	}
	if spec.Adapter != "acp" {
		t.Fatalf("expected acp adapter, got %s", spec.Adapter)
	}
	if spec.Dialect != "hermes" {
		t.Fatalf("expected hermes dialect, got %s", spec.Dialect)
	}
	if got := spec.Defaults["executor"]; got != "aethrolink-agent" {
		t.Fatalf("expected aethrolink-agent executor, got %#v", got)
	}
	route, ok := registry.RouteForIntent("media.video.render")
	if !ok {
		t.Fatalf("expected route for media.video.render")
	}
	if route.Runtime != "media" {
		t.Fatalf("expected media route, got %s", route.Runtime)
	}
}

func TestLoadRegistrySupportsACPDialect(t *testing.T) {
	registryPath := filepath.Join(t.TempDir(), "registry.yaml")
	content := []byte("runtimes:\n  hermes_real_test:\n    adapter: acp\n    dialect: hermes\n    launch:\n      mode: managed\n      command: [\"hermes\", \"-p\", \"coder\", \"acp\"]\n    capabilities:\n      - code.patch\n")
	if err := os.WriteFile(registryPath, content, 0o644); err != nil {
		t.Fatalf("write registry: %v", err)
	}
	registry, err := LoadRegistry(registryPath)
	if err != nil {
		t.Fatalf("load registry: %v", err)
	}
	spec, err := registry.ResolveRuntime(context.Background(), "hermes_real_test")
	if err != nil {
		t.Fatalf("resolve runtime: %v", err)
	}
	if spec.Adapter != "acp" {
		t.Fatalf("expected acp adapter, got %s", spec.Adapter)
	}
	if spec.Dialect != "hermes" {
		t.Fatalf("expected hermes dialect, got %s", spec.Dialect)
	}
}
