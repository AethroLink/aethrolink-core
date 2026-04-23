package runtime

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aethrolink/aethrolink-core/internal/storage"
	atypes "github.com/aethrolink/aethrolink-core/pkg/types"
)

type ManagedProcess struct {
	cmd        *exec.Cmd
	exited     atomic.Bool
	waitErrMsg atomic.Value
	stderrMu   sync.Mutex
	stderrTail string
}

func (p *ManagedProcess) PID() string {
	if p == nil || p.cmd == nil || p.cmd.Process == nil {
		return ""
	}
	return fmt.Sprintf("%d", p.cmd.Process.Pid)
}

func (p *ManagedProcess) IsAlive() bool {
	if p == nil || p.cmd == nil || p.cmd.Process == nil {
		return false
	}
	if p.exited.Load() {
		return false
	}
	if p.cmd.ProcessState == nil {
		return true
	}
	return !p.cmd.ProcessState.Exited()
}

func (p *ManagedProcess) Kill() error {
	if p == nil || p.cmd == nil || p.cmd.Process == nil {
		return nil
	}
	return p.cmd.Process.Kill()
}

func (p *ManagedProcess) WaitErr() error {
	if p == nil {
		return nil
	}
	raw := p.waitErrMsg.Load()
	if raw == nil {
		return nil
	}
	msg, _ := raw.(string)
	if msg == "" {
		return nil
	}
	return fmt.Errorf("%s", msg)
}

func (p *ManagedProcess) AppendStderr(text string) {
	if p == nil || text == "" {
		return
	}
	p.stderrMu.Lock()
	defer p.stderrMu.Unlock()
	combined := strings.TrimSpace(p.stderrTail + "\n" + text)
	lines := strings.Split(combined, "\n")
	if len(lines) > 8 {
		lines = lines[len(lines)-8:]
	}
	p.stderrTail = strings.Join(lines, "\n")
}

func (p *ManagedProcess) StderrTail() string {
	if p == nil {
		return ""
	}
	p.stderrMu.Lock()
	defer p.stderrMu.Unlock()
	return p.stderrTail
}

func monitorManagedProcess(cmd *exec.Cmd) *ManagedProcess {
	proc := &ManagedProcess{cmd: cmd}
	go func() {
		err := cmd.Wait()
		if err != nil {
			proc.waitErrMsg.Store(err.Error())
		} else {
			proc.waitErrMsg.Store("")
		}
		proc.exited.Store(true)
	}()
	return proc
}

type StdioWorker struct {
	process     *ManagedProcess
	stdin       io.WriteCloser
	writeMu     sync.Mutex
	pendingMu   sync.Mutex
	pending     map[uint64]chan map[string]any
	closed      chan struct{}
	subsMu      sync.Mutex
	subscribers map[int]chan map[string]any
	nextSubID   int
	nextReqID   atomic.Uint64
}

// spawnStdioWorker starts a long-lived JSON-RPC-over-stdio worker and attaches
// the read loop that demultiplexes responses vs unsolicited events.
func spawnStdioWorker(command []string) (*StdioWorker, error) {
	if len(command) == 0 {
		return nil, fmt.Errorf("empty stdio command")
	}
	cmd := exec.Command(command[0], command[1:]...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdio stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdio stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("stdio stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start stdio worker: %w", err)
	}
	proc := monitorManagedProcess(cmd)
	worker := &StdioWorker{
		process:     proc,
		stdin:       stdin,
		pending:     map[uint64]chan map[string]any{},
		closed:      make(chan struct{}),
		subscribers: map[int]chan map[string]any{},
	}
	go worker.readLoop(stdout)
	go readProcessStderr(stderr, proc)
	return worker, nil
}

func (w *StdioWorker) readLoop(stdout io.Reader) {
	defer close(w.closed)
	defer func() {
		w.pendingMu.Lock()
		for id, ch := range w.pending {
			delete(w.pending, id)
			close(ch)
		}
		w.pendingMu.Unlock()
	}()
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var msg map[string]any
		if err := json.Unmarshal(line, &msg); err != nil {
			continue
		}
		// Messages with an id are direct RPC responses; everything else is treated
		// as a broadcast event for active subscribers.
		if idValue, ok := msg["id"]; ok {
			if idf, ok := idValue.(float64); ok {
				id := uint64(idf)
				w.pendingMu.Lock()
				ch := w.pending[id]
				delete(w.pending, id)
				w.pendingMu.Unlock()
				if ch != nil {
					select {
					case ch <- msg:
					default:
					}
					close(ch)
				}
				continue
			}
		}
		w.subsMu.Lock()
		for _, ch := range w.subscribers {
			select {
			case ch <- msg:
			default:
			}
		}
		w.subsMu.Unlock()
	}
}

func (w *StdioWorker) Request(method string, params map[string]any) (map[string]any, error) {
	return w.RequestWithTimeout(method, params, 10*time.Second)
}

func (w *StdioWorker) RequestWithTimeout(method string, params map[string]any, timeout time.Duration) (map[string]any, error) {
	id := w.nextReqID.Add(1)
	respCh := make(chan map[string]any, 1)
	w.pendingMu.Lock()
	w.pending[id] = respCh
	w.pendingMu.Unlock()
	payload := map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	w.writeMu.Lock()
	_, err = w.stdin.Write(append(encoded, '\n'))
	w.writeMu.Unlock()
	if err != nil {
		return nil, err
	}
	select {
	case resp, ok := <-respCh:
		if !ok {
			return nil, w.exitErr()
		}
		if errVal, ok := resp["error"]; ok {
			return nil, fmt.Errorf("rpc error: %v", errVal)
		}
		result, _ := resp["result"].(map[string]any)
		if result == nil {
			return map[string]any{}, nil
		}
		return result, nil
	case <-time.After(timeout):
		return nil, fmt.Errorf("rpc timeout")
	case <-w.closed:
		return nil, w.exitErr()
	}
}

func (w *StdioWorker) Subscribe() (<-chan map[string]any, func()) {
	w.subsMu.Lock()
	defer w.subsMu.Unlock()
	id := w.nextSubID
	w.nextSubID++
	ch := make(chan map[string]any, 64)
	w.subscribers[id] = ch
	cancel := func() {
		w.subsMu.Lock()
		defer w.subsMu.Unlock()
		if existing, ok := w.subscribers[id]; ok {
			delete(w.subscribers, id)
			close(existing)
		}
	}
	return ch, cancel
}

func (w *StdioWorker) IsAlive() bool { return w.process.IsAlive() }
func (w *StdioWorker) PID() string   { return w.process.PID() }
func (w *StdioWorker) Kill() error   { return w.process.Kill() }

func (w *StdioWorker) exitErr() error {
	if w == nil || w.process == nil {
		return fmt.Errorf("stdio worker exited")
	}
	if tail := strings.TrimSpace(w.process.StderrTail()); tail != "" {
		return fmt.Errorf("stdio worker exited: %s", tail)
	}
	if err := w.process.WaitErr(); err != nil {
		return fmt.Errorf("stdio worker exited: %w", err)
	}
	return fmt.Errorf("stdio worker exited")
}

func readProcessStderr(stderr io.Reader, proc *ManagedProcess) {
	scanner := bufio.NewScanner(stderr)
	for scanner.Scan() {
		proc.AppendStderr(scanner.Text())
	}
}

type runtimeEntry struct {
	lease   atypes.RuntimeLease
	process *ManagedProcess
	worker  *StdioWorker
}

type Manager struct {
	store   *storage.SQLiteStore
	mu      sync.Mutex
	entries map[string]runtimeEntry
}

// NewManager owns the live in-process runtime registry. It is intentionally
// small: enough to reuse/stop workers and record leases, without taking on
// broader orchestration policy.
func NewManager(store *storage.SQLiteStore) *Manager {
	return &Manager{store: store, entries: map[string]runtimeEntry{}}
}

func (m *Manager) Store() *storage.SQLiteStore { return m.store }

func entryKey(targetID, subcontextKey string) string {
	if subcontextKey == "" {
		subcontextKey = "default"
	}
	return targetID + "::" + subcontextKey
}

func (m *Manager) EnsureProcess(ctx context.Context, targetID, subcontextKey string, command []string, healthcheckURL string) (atypes.RuntimeLease, error) {
	m.mu.Lock()
	if existing, ok := m.entries[entryKey(targetID, subcontextKey)]; ok {
		if existing.process == nil || existing.process.IsAlive() {
			m.mu.Unlock()
			return existing.lease, nil
		}
	}
	m.mu.Unlock()

	// Even command-less runtimes still get a lease entry so the rest of the stack
	// can treat "ready" consistently across local and externally-managed runtimes.
	lease := atypes.RuntimeLease{LeaseID: atypes.NewID(), TargetID: targetID, SubcontextKey: subcontextKey, Metadata: map[string]any{}, CreatedAt: atypes.NowUTC()}
	if len(command) == 0 {
		if err := m.store.InsertLease(ctx, lease); err != nil {
			return atypes.RuntimeLease{}, err
		}
		m.mu.Lock()
		m.entries[entryKey(targetID, subcontextKey)] = runtimeEntry{lease: lease}
		m.mu.Unlock()
		return lease, nil
	}
	// stdio runtimes are keyed by runtime+subcontext so executor/session scopes can be
	// reused independently without bleeding state into each other.
	launchID, err := m.store.InsertLaunchHistory(ctx, targetID, subcontextKey, command, "", "launching", "")
	if err != nil {
		return atypes.RuntimeLease{}, err
	}
	cmd := exec.Command(command[0], command[1:]...)
	if err := cmd.Start(); err != nil {
		_ = m.store.FinishLaunchHistory(ctx, launchID, "failed", err.Error())
		return atypes.RuntimeLease{}, err
	}
	proc := monitorManagedProcess(cmd)
	lease.ProcessID = proc.PID()
	if err := m.store.InsertLease(ctx, lease); err != nil {
		return atypes.RuntimeLease{}, err
	}
	if err := waitUntilHealthy(healthcheckURL); err != nil {
		_ = m.store.FinishLaunchHistory(ctx, launchID, "failed", err.Error())
		_ = proc.Kill()
		return atypes.RuntimeLease{}, err
	}
	if err := m.store.FinishLaunchHistory(ctx, launchID, "ready", ""); err != nil {
		return atypes.RuntimeLease{}, err
	}
	m.mu.Lock()
	m.entries[entryKey(targetID, subcontextKey)] = runtimeEntry{lease: lease, process: proc}
	m.mu.Unlock()
	return lease, nil
}

func (m *Manager) EnsureStdioWorker(ctx context.Context, targetID, subcontextKey string, command []string) (atypes.RuntimeLease, *StdioWorker, error) {
	m.mu.Lock()
	if existing, ok := m.entries[entryKey(targetID, subcontextKey)]; ok {
		if existing.worker != nil && existing.worker.IsAlive() {
			m.mu.Unlock()
			return existing.lease, existing.worker, nil
		}
	}
	m.mu.Unlock()

	launchID, err := m.store.InsertLaunchHistory(ctx, targetID, subcontextKey, command, "", "launching", "")
	if err != nil {
		return atypes.RuntimeLease{}, nil, err
	}
	worker, err := spawnStdioWorker(command)
	if err != nil {
		_ = m.store.FinishLaunchHistory(ctx, launchID, "failed", err.Error())
		return atypes.RuntimeLease{}, nil, err
	}
	// Some ACP bridges fail immediately after startup (for example missing
	// pairing/auth). Give the subprocess a brief grace window so launch failures
	// are surfaced during readiness instead of later as broken pipes on submit.
	time.Sleep(200 * time.Millisecond)
	if !worker.IsAlive() {
		waitErr := worker.process.WaitErr()
		message := "stdio worker exited during startup"
		if waitErr != nil {
			message = waitErr.Error()
		}
		_ = m.store.FinishLaunchHistory(ctx, launchID, "failed", message)
		return atypes.RuntimeLease{}, nil, fmt.Errorf("%s", message)
	}
	lease := atypes.RuntimeLease{LeaseID: atypes.NewID(), TargetID: targetID, SubcontextKey: subcontextKey, ProcessID: worker.PID(), Metadata: map[string]any{}, CreatedAt: atypes.NowUTC()}
	if err := m.store.InsertLease(ctx, lease); err != nil {
		return atypes.RuntimeLease{}, nil, err
	}
	if err := m.store.FinishLaunchHistory(ctx, launchID, "ready", ""); err != nil {
		return atypes.RuntimeLease{}, nil, err
	}
	m.mu.Lock()
	m.entries[entryKey(targetID, subcontextKey)] = runtimeEntry{lease: lease, worker: worker}
	m.mu.Unlock()
	return lease, worker, nil
}

func (m *Manager) GetStdioWorker(targetID, subcontextKey string) *StdioWorker {
	m.mu.Lock()
	defer m.mu.Unlock()
	entry, ok := m.entries[entryKey(targetID, subcontextKey)]
	if !ok || entry.worker == nil || !entry.worker.IsAlive() {
		return nil
	}
	return entry.worker
}

func (m *Manager) Stop(ctx context.Context, targetID, subcontextKey string) error {
	m.mu.Lock()
	entry, ok := m.entries[entryKey(targetID, subcontextKey)]
	if ok {
		delete(m.entries, entryKey(targetID, subcontextKey))
	}
	m.mu.Unlock()
	if !ok {
		return nil
	}
	if entry.worker != nil {
		_ = entry.worker.Kill()
	}
	if entry.process != nil {
		_ = entry.process.Kill()
	}
	return m.store.ReleaseLease(ctx, entry.lease.LeaseID)
}

func (m *Manager) StopAll(ctx context.Context) error {
	m.mu.Lock()
	entries := make([]runtimeEntry, 0, len(m.entries))
	for _, entry := range m.entries {
		entries = append(entries, entry)
	}
	m.entries = map[string]runtimeEntry{}
	m.mu.Unlock()
	for _, entry := range entries {
		if entry.worker != nil {
			_ = entry.worker.Kill()
		}
		if entry.process != nil {
			_ = entry.process.Kill()
		}
		_ = m.store.ReleaseLease(ctx, entry.lease.LeaseID)
	}
	return nil
}

func (m *Manager) Health(targetID, subcontextKey string) map[string]any {
	m.mu.Lock()
	defer m.mu.Unlock()
	entry, ok := m.entries[entryKey(targetID, subcontextKey)]
	if !ok {
		return map[string]any{"target_id": targetID, "healthy": false}
	}
	healthy := true
	processID := ""
	if entry.worker != nil {
		healthy = entry.worker.IsAlive()
		processID = entry.worker.PID()
	}
	if entry.process != nil {
		healthy = entry.process.IsAlive()
		processID = entry.process.PID()
	}
	out := map[string]any{"target_id": targetID, "healthy": healthy, "lease_id": entry.lease.LeaseID}
	if processID != "" {
		out["process_id"] = processID
	}
	return out
}

func waitUntilHealthy(healthcheckURL string) error {
	if healthcheckURL == "" {
		time.Sleep(100 * time.Millisecond)
		return nil
	}
	client := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := client.Get(healthcheckURL)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				return nil
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("healthcheck timeout")
}
