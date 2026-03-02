package tools

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/nextlevelbuilder/goclaw/internal/bus"
	"github.com/nextlevelbuilder/goclaw/internal/store"
)

// TeamToolManager is the shared backend for team_tasks and team_message tools.
// It resolves the calling agent's team from context and provides access to
// the team store, agent store, and message bus.
type TeamToolManager struct {
	teamStore  store.TeamStore
	agentStore store.AgentStore
	msgBus     *bus.MessageBus
}

func NewTeamToolManager(teamStore store.TeamStore, agentStore store.AgentStore, msgBus *bus.MessageBus) *TeamToolManager {
	return &TeamToolManager{teamStore: teamStore, agentStore: agentStore, msgBus: msgBus}
}

// resolveTeam returns the team that the calling agent belongs to.
func (m *TeamToolManager) resolveTeam(ctx context.Context) (*store.TeamData, uuid.UUID, error) {
	agentID := store.AgentIDFromContext(ctx)
	if agentID == uuid.Nil {
		return nil, uuid.Nil, fmt.Errorf("no agent context — team tools require managed mode")
	}

	team, err := m.teamStore.GetTeamForAgent(ctx, agentID)
	if err != nil {
		return nil, uuid.Nil, fmt.Errorf("failed to resolve team: %w", err)
	}
	if team == nil {
		return nil, uuid.Nil, fmt.Errorf("this agent is not part of any team")
	}

	return team, agentID, nil
}

// resolveAgentByKey looks up an agent by key and returns its UUID.
func (m *TeamToolManager) resolveAgentByKey(key string) (uuid.UUID, error) {
	ag, err := m.agentStore.GetByKey(context.Background(), key)
	if err != nil {
		return uuid.Nil, fmt.Errorf("agent %q not found: %w", key, err)
	}
	return ag.ID, nil
}

// agentKeyFromID returns the agent_key for a given UUID.
func (m *TeamToolManager) agentKeyFromID(ctx context.Context, id uuid.UUID) string {
	ag, err := m.agentStore.GetByID(ctx, id)
	if err != nil {
		return id.String()
	}
	return ag.AgentKey
}

// broadcastTeamEvent sends a real-time event via the message bus for team activity visibility.
func (m *TeamToolManager) broadcastTeamEvent(name string, payload map[string]string) {
	if m.msgBus == nil {
		return
	}
	m.msgBus.Broadcast(bus.Event{
		Name:    name,
		Payload: payload,
	})
}
