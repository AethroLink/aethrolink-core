package adapters

import (
	"sync"

	atypes "github.com/aethrolink/aethrolink-core/pkg/types"
)

type Registry struct {
	mu    sync.RWMutex
	items map[string]atypes.RuntimeAdapter
}

func NewRegistry() *Registry {
	return &Registry{items: map[string]atypes.RuntimeAdapter{}}
}

func (r *Registry) Register(kind string, adapter atypes.RuntimeAdapter) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.items[kind] = adapter
}

func (r *Registry) Get(kind string) (atypes.RuntimeAdapter, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	adapter, ok := r.items[kind]
	return adapter, ok
}

func asString(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	value, _ := m[key].(string)
	return value
}
