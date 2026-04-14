package agents

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/aethrolink/aethrolink-core/internal/storage"
	atypes "github.com/aethrolink/aethrolink-core/pkg/types"
)

var ErrAgentNotFound = errors.New("agent not found")

type Service struct {
	store *storage.SQLiteStore
}

func NewService(store *storage.SQLiteStore) *Service {
	return &Service{store: store}
}

func (s *Service) Register(ctx context.Context, req atypes.AgentRegistrationRequest) (atypes.AgentRecord, error) {
	req.Normalize()
	now := atypes.NowUTC()
	leaseTTL := time.Duration(req.LeaseTTLSeconds) * time.Second
	record := atypes.AgentRecord{
		AgentID:        req.AgentID,
		DisplayName:    req.DisplayName,
		RuntimeKind:    req.RuntimeKind,
		TransportKind:  req.TransportKind,
		Endpoint:       req.Endpoint,
		RuntimeID:      req.RuntimeID,
		Adapter:        req.Adapter,
		Dialect:        req.Dialect,
		Healthcheck:    req.Healthcheck,
		Launch:         req.Launch,
		Defaults:       cloneMap(req.Defaults),
		Capabilities:   append([]string(nil), req.Capabilities...),
		StickyMode:     req.StickyMode,
		Metadata:       cloneMap(req.Metadata),
		Status:         atypes.AgentStatusOnline,
		RegisteredAt:   now,
		UpdatedAt:      now,
		LastSeenAt:     now,
		LeaseExpiresAt: now.Add(leaseTTL),
	}
	if existing, err := s.store.GetAgent(ctx, record.AgentID); err == nil {
		record.RegisteredAt = existing.RegisteredAt
	}
	if err := s.store.UpsertAgent(ctx, record); err != nil {
		return atypes.AgentRecord{}, err
	}
	return s.Get(ctx, record.AgentID)
}

func (s *Service) Heartbeat(ctx context.Context, agentID string, req atypes.AgentHeartbeatRequest) (atypes.AgentRecord, error) {
	req.Normalize()
	if _, err := s.store.GetAgent(ctx, agentID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return atypes.AgentRecord{}, ErrAgentNotFound
		}
		return atypes.AgentRecord{}, err
	}
	now := atypes.NowUTC()
	if err := s.store.TouchAgentLease(ctx, agentID, now, now.Add(time.Duration(req.LeaseTTLSeconds)*time.Second)); err != nil {
		return atypes.AgentRecord{}, err
	}
	return s.Get(ctx, agentID)
}

func (s *Service) Unregister(ctx context.Context, agentID string) error {
	if _, err := s.store.GetAgent(ctx, agentID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrAgentNotFound
		}
		return err
	}
	return s.store.MarkAgentOffline(ctx, agentID)
}

func (s *Service) Get(ctx context.Context, agentID string) (atypes.AgentRecord, error) {
	agent, err := s.store.GetAgent(ctx, agentID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return atypes.AgentRecord{}, ErrAgentNotFound
		}
		return atypes.AgentRecord{}, err
	}
	return agent.WithEffectiveStatus(atypes.NowUTC()), nil
}

func (s *Service) List(ctx context.Context) ([]atypes.AgentRecord, error) {
	agents, err := s.store.ListAgents(ctx)
	if err != nil {
		return nil, err
	}
	now := atypes.NowUTC()
	for i := range agents {
		agents[i] = agents[i].WithEffectiveStatus(now)
	}
	return agents, nil
}

func (s *Service) ResolveRuntime(ctx context.Context, runtimeID string) (atypes.RuntimeSpec, error) {
	agents, err := s.List(ctx)
	if err != nil {
		return atypes.RuntimeSpec{}, err
	}
	for _, agent := range agents {
		if agent.RuntimeID == runtimeID && agent.Status == atypes.AgentStatusOnline {
			return agentToRuntimeSpec(agent), nil
		}
	}
	return atypes.RuntimeSpec{}, fmt.Errorf("runtime not found: %s", runtimeID)
}

func (s *Service) ListRuntimes(ctx context.Context) ([]atypes.RuntimeSpec, error) {
	agents, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]atypes.RuntimeSpec, 0, len(agents))
	for _, agent := range agents {
		if agent.Status != atypes.AgentStatusOnline || agent.RuntimeID == "" {
			continue
		}
		out = append(out, agentToRuntimeSpec(agent))
	}
	return out, nil
}

func agentToRuntimeSpec(agent atypes.AgentRecord) atypes.RuntimeSpec {
	return atypes.RuntimeSpec{
		RuntimeID:    agent.RuntimeID,
		Adapter:      agent.Adapter,
		Dialect:      agent.Dialect,
		Endpoint:     agent.Endpoint,
		Healthcheck:  agent.Healthcheck,
		Launch:       agent.Launch,
		Defaults:     cloneMap(agent.Defaults),
		Capabilities: append([]string(nil), agent.Capabilities...),
	}
}

func cloneMap(in map[string]any) map[string]any {
	if in == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		if nested, ok := value.(map[string]any); ok {
			out[key] = cloneMap(nested)
			continue
		}
		out[key] = value
	}
	return out
}
