package tools

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/nextlevelbuilder/goclaw/internal/bus"
	"github.com/nextlevelbuilder/goclaw/internal/store"
)

const teamCacheTTL = 5 * time.Minute

// teamCacheEntry wraps cached team data + members with a timestamp for TTL expiration.
type teamCacheEntry struct {
	team     *store.TeamData
	members  []store.TeamMemberData // loaded together with team to avoid separate DB call
	cachedAt time.Time
}

// agentCacheEntry wraps cached agent data with a timestamp for TTL expiration.
type agentCacheEntry struct {
	agent    *store.AgentData
	cachedAt time.Time
}

// TeamToolManager is the shared backend for team_tasks tool and workspace interceptor.
// It resolves the calling agent's team from context and provides access to
// the team store, agent store, and message bus.
// Includes a TTL cache for team data to avoid DB queries on every tool call.
type TeamToolManager struct {
	teamStore     store.TeamStore
	agentStore    store.AgentStore
	msgBus        *bus.MessageBus
	dataDir       string   // base data directory for workspace path resolution
	teamCache     sync.Map // agentID (uuid.UUID) → *teamCacheEntry
	agentCache    sync.Map // agentID (uuid.UUID) → *agentCacheEntry
	agentKeyCache sync.Map // agentKey (string) → *agentCacheEntry
	workerStreams WorkerStreamController // optional; set via SetWorkerStreamController
}

func NewTeamToolManager(teamStore store.TeamStore, agentStore store.AgentStore, msgBus *bus.MessageBus, dataDir string) *TeamToolManager {
	return &TeamToolManager{teamStore: teamStore, agentStore: agentStore, msgBus: msgBus, dataDir: dataDir}
}

// ============================================================
// TeamToolBackend exported wrappers
// These thin wrappers satisfy the TeamToolBackend interface
// while keeping the unexported originals for internal use
// (WorkspaceInterceptor, PostTurnProcessor, etc.).
// ============================================================

func (m *TeamToolManager) Store() store.TeamStore                { return m.teamStore }
func (m *TeamToolManager) DataDir() string                       { return m.dataDir }
func (m *TeamToolManager) TryPublishInbound(msg bus.InboundMessage) bool {
	if m.msgBus == nil {
		return false
	}
	return m.msgBus.TryPublishInbound(msg)
}

// SetWorkerStreamController sets the worker stream controller for interrupt/inject actions.
func (m *TeamToolManager) SetWorkerStreamController(wsc WorkerStreamController) {
	m.workerStreams = wsc
}

func (m *TeamToolManager) InterruptWorkerSession(_ context.Context, taskID uuid.UUID) error {
	if m.workerStreams == nil {
		return fmt.Errorf("worker stream controller not configured")
	}
	return m.workerStreams.Interrupt(taskID)
}

func (m *TeamToolManager) InjectWorkerMessage(_ context.Context, taskID uuid.UUID, content string) error {
	if m.workerStreams == nil {
		return fmt.Errorf("worker stream controller not configured")
	}
	msg := []byte(`{"type":"user","message":{"role":"user","content":` + strconv.Quote(content) + `},"parent_tool_use_id":null}` + "\n")
	return m.workerStreams.InjectMessage(taskID, msg)
}
