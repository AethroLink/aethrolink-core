package adaptersupport

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"time"

	"github.com/aethrolink/aethrolink-core/internal/storage"
	atypes "github.com/aethrolink/aethrolink-core/pkg/types"
)

const defaultSessionIdleTimeout = time.Hour

// SessionCoordinator serializes access to sticky conversational runtimes and
// persists their session bindings in SQLite.
type SessionCoordinator struct {
	store  *storage.SQLiteStore
	mu     sync.Mutex
	active map[string]string
}

// NewSessionCoordinator constructs the shared sticky-session coordinator used by
// adapters that bind local tasks to long-lived remote sessions.
func NewSessionCoordinator(store *storage.SQLiteStore) *SessionCoordinator {
	return &SessionCoordinator{store: store, active: map[string]string{}}
}

// SessionScope returns the in-process lock scope for a sticky runtime session.
func SessionScope(targetID, subcontextKey, stickyKey string) string {
	return targetID + "::" + subcontextKey + "::" + stickyKey
}

// TryAcquire rejects concurrent prompts targeting the same sticky session scope.
func (c *SessionCoordinator) TryAcquire(scope, taskID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if activeTaskID, ok := c.active[scope]; ok && activeTaskID != taskID {
		return fmt.Errorf("session scope busy: %s", scope)
	}
	c.active[scope] = taskID
	return nil
}

// Release clears an active task from a sticky session scope when that task ends.
func (c *SessionCoordinator) Release(scope, taskID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if activeTaskID, ok := c.active[scope]; ok && activeTaskID == taskID {
		delete(c.active, scope)
	}
}

// LoadBinding loads a persisted sticky session binding if one already exists.
func (c *SessionCoordinator) LoadBinding(ctx context.Context, targetID, subcontextKey, stickyKey string) (atypes.SessionBinding, bool, error) {
	binding, err := c.store.GetSessionBinding(ctx, targetID, subcontextKey, stickyKey)
	if err == nil {
		return binding, true, nil
	}
	if err == sql.ErrNoRows {
		return atypes.SessionBinding{}, false, nil
	}
	return atypes.SessionBinding{}, false, err
}

// PersistBinding upserts the sticky-session binding after a runtime accepts or
// refreshes a session.
func (c *SessionCoordinator) PersistBinding(ctx context.Context, targetID, subcontextKey, stickyKey, adapter, remoteSessionID string, metadata map[string]any, touchedAt time.Time) error {
	binding, exists, err := c.LoadBinding(ctx, targetID, subcontextKey, stickyKey)
	if err != nil {
		return err
	}
	if !exists {
		binding.CreatedAt = touchedAt
	}
	binding.TargetID = targetID
	binding.SubcontextKey = subcontextKey
	binding.StickyKey = stickyKey
	binding.Adapter = adapter
	binding.RemoteSessionID = remoteSessionID
	binding.Metadata = cloneSessionMetadata(metadata)
	binding.UpdatedAt = touchedAt
	binding.LastUsedAt = touchedAt
	binding.LastActivityAt = touchedAt
	return c.store.UpsertSessionBinding(ctx, binding)
}

// TouchActivity records the latest observed runtime activity for a sticky session.
func (c *SessionCoordinator) TouchActivity(ctx context.Context, targetID, subcontextKey, stickyKey string, touchedAt time.Time) error {
	return c.store.TouchSessionBindingActivity(ctx, targetID, subcontextKey, stickyKey, touchedAt)
}

// SessionIdleTimeout resolves the sticky-session idle timeout from runtime options.
func SessionIdleTimeout(runtimeOptions map[string]any) time.Duration {
	if runtimeOptions == nil {
		return defaultSessionIdleTimeout
	}
	if raw, ok := runtimeOptions["session_idle_timeout_ms"]; ok {
		switch value := raw.(type) {
		case int:
			if value > 0 {
				return time.Duration(value) * time.Millisecond
			}
		case int64:
			if value > 0 {
				return time.Duration(value) * time.Millisecond
			}
		case float64:
			if value > 0 {
				return time.Duration(value) * time.Millisecond
			}
		}
	}
	return defaultSessionIdleTimeout
}

// SessionBindingStale reports whether a persisted binding should be discarded
// before attempting session reuse.
func SessionBindingStale(binding atypes.SessionBinding, idleTimeout time.Duration, now time.Time) bool {
	if binding.RemoteSessionID == "" {
		return true
	}
	if idleTimeout <= 0 {
		return false
	}
	return now.Sub(binding.LastActivityAt) > idleTimeout
}

func cloneSessionMetadata(in map[string]any) map[string]any {
	if in == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		if nested, ok := value.(map[string]any); ok {
			out[key] = cloneSessionMetadata(nested)
			continue
		}
		out[key] = value
	}
	return out
}
