package config

import (
	"context"
	"fmt"
	"os"
	"sort"
	"sync"

	atypes "github.com/aethrolink/aethrolink-core/pkg/types"
	"gopkg.in/yaml.v3"
)

type RegistryDiscovery struct {
	mu       sync.RWMutex
	runtimes map[string]atypes.RuntimeSpec
	routes   map[string]atypes.RouteSpec
}

func LoadRegistry(path string) (*RegistryDiscovery, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read registry: %w", err)
	}
	var file atypes.RegistryFile
	if err := yaml.Unmarshal(content, &file); err != nil {
		return nil, fmt.Errorf("parse registry: %w", err)
	}
	d := &RegistryDiscovery{
		runtimes: make(map[string]atypes.RuntimeSpec, len(file.Runtimes)),
		routes:   make(map[string]atypes.RouteSpec, len(file.Routes)),
	}
	for id, spec := range file.Runtimes {
		spec.RuntimeID = id
		if spec.Defaults == nil {
			spec.Defaults = map[string]any{}
		}
		d.runtimes[id] = spec
	}
	for intent, route := range file.Routes {
		if route.RuntimeOptions == nil {
			route.RuntimeOptions = map[string]any{}
		}
		d.routes[intent] = route
	}
	return d, nil
}

func (d *RegistryDiscovery) ResolveRuntime(_ context.Context, runtimeID string) (atypes.RuntimeSpec, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	spec, ok := d.runtimes[runtimeID]
	if !ok {
		return atypes.RuntimeSpec{}, fmt.Errorf("runtime not found: %s", runtimeID)
	}
	return cloneRuntimeSpec(spec), nil
}

func (d *RegistryDiscovery) ListRuntimes(_ context.Context) ([]atypes.RuntimeSpec, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	ids := make([]string, 0, len(d.runtimes))
	for id := range d.runtimes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]atypes.RuntimeSpec, 0, len(ids))
	for _, id := range ids {
		out = append(out, cloneRuntimeSpec(d.runtimes[id]))
	}
	return out, nil
}

func (d *RegistryDiscovery) RouteForIntent(intent string) (atypes.RouteSpec, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	route, ok := d.routes[intent]
	if !ok {
		return atypes.RouteSpec{}, false
	}
	return atypes.RouteSpec{Runtime: route.Runtime, RuntimeOptions: cloneMap(route.RuntimeOptions)}, true
}

func cloneRuntimeSpec(spec atypes.RuntimeSpec) atypes.RuntimeSpec {
	spec.Defaults = cloneMap(spec.Defaults)
	if spec.Capabilities != nil {
		spec.Capabilities = append([]string(nil), spec.Capabilities...)
	}
	if spec.Launch.Command != nil {
		spec.Launch.Command = append([]string(nil), spec.Launch.Command...)
	}
	if spec.Launch.Commands != nil {
		commands := make(map[string][]string, len(spec.Launch.Commands))
		for key, value := range spec.Launch.Commands {
			commands[key] = append([]string(nil), value...)
		}
		spec.Launch.Commands = commands
	}
	return spec
}

func cloneMap(in map[string]any) map[string]any {
	if in == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		switch typed := value.(type) {
		case map[string]any:
			out[key] = cloneMap(typed)
		default:
			out[key] = typed
		}
	}
	return out
}
