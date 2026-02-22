package agent

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/nextlevelbuilder/goclaw/internal/bootstrap"
	"github.com/nextlevelbuilder/goclaw/internal/bus"
	"github.com/nextlevelbuilder/goclaw/internal/config"
	"github.com/nextlevelbuilder/goclaw/internal/providers"
	"github.com/nextlevelbuilder/goclaw/internal/skills"
	"github.com/nextlevelbuilder/goclaw/internal/store"
	"github.com/nextlevelbuilder/goclaw/internal/tools"
	"github.com/nextlevelbuilder/goclaw/internal/tracing"
)

// ResolverDeps holds shared dependencies for the managed-mode agent resolver.
type ResolverDeps struct {
	AgentStore  store.AgentStore
	ProviderReg *providers.Registry
	Bus         bus.EventPublisher
	Sessions    store.SessionStore
	Tools       *tools.Registry
	ToolPolicy  *tools.PolicyEngine
	Skills      *skills.Loader
	HasMemory      bool
	OnEvent        func(AgentEvent)
	TraceCollector *tracing.Collector

	// Per-user file seeding + dynamic context loading (managed mode)
	EnsureUserFiles   EnsureUserFilesFunc
	ContextFileLoader ContextFileLoaderFunc

	// Security
	InjectionAction string // "log", "warn", "block", "off"
	MaxMessageChars int

	// Global defaults (from config.json) — per-agent DB overrides take priority
	CompactionCfg          *config.CompactionConfig
	ContextPruningCfg      *config.ContextPruningConfig
	SandboxEnabled         bool
	SandboxContainerDir    string
	SandboxWorkspaceAccess string

	// Dynamic custom tools (managed mode)
	DynamicLoader *tools.DynamicToolLoader // nil if not managed
}

// NewManagedResolver creates a ResolverFunc that builds Loops from DB agent data.
// This is the core of managed mode: agents are defined in Postgres, not config.json.
func NewManagedResolver(deps ResolverDeps) ResolverFunc {
	return func(agentKey string) (Agent, error) {
		ctx := context.Background()

		// Support lookup by UUID (e.g. from cron jobs that store agent_id as UUID)
		var ag *store.AgentData
		var err error
		if id, parseErr := uuid.Parse(agentKey); parseErr == nil {
			ag, err = deps.AgentStore.GetByID(ctx, id)
		} else {
			ag, err = deps.AgentStore.GetByKey(ctx, agentKey)
		}
		if err != nil {
			return nil, fmt.Errorf("agent not found: %s", agentKey)
		}

		// Resolve provider
		provider, err := deps.ProviderReg.Get(ag.Provider)
		if err != nil {
			// Fallback to any available provider
			names := deps.ProviderReg.List()
			if len(names) == 0 {
				return nil, fmt.Errorf("no providers configured for agent %s", agentKey)
			}
			provider, _ = deps.ProviderReg.Get(names[0])
			slog.Warn("agent provider not found, using fallback",
				"agent", agentKey, "wanted", ag.Provider, "using", names[0])
		}

		if provider == nil {
			return nil, fmt.Errorf("no provider available for agent %s", agentKey)
		}

		// Load bootstrap files from DB
		contextFiles := bootstrap.LoadFromStore(ctx, deps.AgentStore, ag.ID)

		contextWindow := ag.ContextWindow
		if contextWindow <= 0 {
			contextWindow = 200000
		}
		maxIter := ag.MaxToolIterations
		if maxIter <= 0 {
			maxIter = 20
		}

		// Per-agent config overrides (fallback to global defaults from config.json)
		compactionCfg := deps.CompactionCfg
		if c := ag.ParseCompactionConfig(); c != nil {
			compactionCfg = c
		}
		contextPruningCfg := deps.ContextPruningCfg
		if c := ag.ParseContextPruning(); c != nil {
			contextPruningCfg = c
		}
		sandboxEnabled := deps.SandboxEnabled
		sandboxContainerDir := deps.SandboxContainerDir
		sandboxWorkspaceAccess := deps.SandboxWorkspaceAccess
		if c := ag.ParseSandboxConfig(); c != nil {
			resolved := c.ToSandboxConfig()
			sandboxContainerDir = resolved.ContainerWorkdir()
			sandboxWorkspaceAccess = string(resolved.WorkspaceAccess)
		}

		// Per-agent custom tools (clone registry if agent has custom tools)
		toolsReg := deps.Tools
		if deps.DynamicLoader != nil {
			if agentReg, err := deps.DynamicLoader.LoadForAgent(ctx, deps.Tools, ag.ID); err != nil {
				slog.Warn("failed to load custom tools", "agent", agentKey, "error", err)
			} else if agentReg != nil {
				toolsReg = agentReg
			}
		}

		loop := NewLoop(LoopConfig{
			ID:                ag.AgentKey,
			AgentUUID:         ag.ID,
			AgentType:         ag.AgentType,
			Provider:          provider,
			Model:             ag.Model,
			ContextWindow:     contextWindow,
			MaxIterations:     maxIter,
			Workspace:         ag.Workspace,
			Bus:               deps.Bus,
			Sessions:          deps.Sessions,
			Tools:             toolsReg,
			ToolPolicy:        deps.ToolPolicy,
			AgentToolPolicy:   ag.ParseToolsConfig(),
			SkillsLoader:      deps.Skills,
			HasMemory:         deps.HasMemory,
			ContextFiles:      contextFiles,
			EnsureUserFiles:   deps.EnsureUserFiles,
			ContextFileLoader: deps.ContextFileLoader,
			OnEvent:           deps.OnEvent,
			TraceCollector:    deps.TraceCollector,
			InjectionAction:   deps.InjectionAction,
			MaxMessageChars:        deps.MaxMessageChars,
			CompactionCfg:          compactionCfg,
			ContextPruningCfg:      contextPruningCfg,
			SandboxEnabled:         sandboxEnabled,
			SandboxContainerDir:    sandboxContainerDir,
			SandboxWorkspaceAccess: sandboxWorkspaceAccess,
		})

		slog.Info("resolved agent from DB", "agent", agentKey, "model", ag.Model, "provider", ag.Provider)
		return loop, nil
	}
}

// InvalidateAgent removes an agent from the router cache, forcing re-resolution.
// Used when agent config is updated via API.
func (r *Router) InvalidateAgent(agentKey string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.agents, agentKey)
	slog.Debug("invalidated agent cache", "agent", agentKey)
}

// InvalidateAll clears the entire agent cache, forcing all agents to re-resolve.
// Used when global tools change (custom tools reload).
func (r *Router) InvalidateAll() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.agents = make(map[string]*agentEntry)
	slog.Debug("invalidated all agent caches")
}
