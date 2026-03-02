package cmd

import (
	"context"
	"log/slog"

	"github.com/google/uuid"

	"github.com/nextlevelbuilder/goclaw/internal/agent"
	"github.com/nextlevelbuilder/goclaw/internal/bus"
	"github.com/nextlevelbuilder/goclaw/internal/config"
	"github.com/nextlevelbuilder/goclaw/internal/hooks"
	httpapi "github.com/nextlevelbuilder/goclaw/internal/http"
	"github.com/nextlevelbuilder/goclaw/internal/providers"
	"github.com/nextlevelbuilder/goclaw/internal/sandbox"
	"github.com/nextlevelbuilder/goclaw/internal/skills"
	"github.com/nextlevelbuilder/goclaw/internal/store"
	"github.com/nextlevelbuilder/goclaw/internal/store/pg"
	"github.com/nextlevelbuilder/goclaw/internal/tools"
	"github.com/nextlevelbuilder/goclaw/internal/tracing"
	"github.com/nextlevelbuilder/goclaw/pkg/protocol"
)

// wireManagedExtras wires managed-mode components that require PG stores:
// agent resolver (lazy-creates Loops from DB), virtual FS interceptors, memory tools,
// and cache invalidation event subscribers.
// PG store creation and tracing are handled in gateway.go before this is called.
// Returns the ContextFileInterceptor so callers can pass it to AgentsMethods
// for immediate cache invalidation on agents.files.set.
func wireManagedExtras(
	stores *store.Stores,
	agentRouter *agent.Router,
	providerReg *providers.Registry,
	msgBus *bus.MessageBus,
	sessStore store.SessionStore,
	toolsReg *tools.Registry,
	toolPE *tools.PolicyEngine,
	skillsLoader *skills.Loader,
	hasMemory bool,
	traceCollector *tracing.Collector,
	workspace string,
	injectionAction string,
	appCfg *config.Config,
	sandboxMgr sandbox.Manager,
	dynamicLoader *tools.DynamicToolLoader,
) *tools.ContextFileInterceptor {
	// 1. Context file interceptor (created before resolver so callbacks can reference it)
	var contextFileInterceptor *tools.ContextFileInterceptor
	if stores.Agents != nil {
		contextFileInterceptor = tools.NewContextFileInterceptor(stores.Agents, workspace)
	}

	// 2. User seeding callback: seeds per-user context files on first chat
	var ensureUserFiles agent.EnsureUserFilesFunc
	if stores.Agents != nil {
		ensureUserFiles = buildEnsureUserFiles(stores.Agents)
	}

	// 3. Context file loader callback: loads per-user context files dynamically
	var contextFileLoader agent.ContextFileLoaderFunc
	if contextFileInterceptor != nil {
		contextFileLoader = buildContextFileLoader(contextFileInterceptor)
	}

	// 4. Compute global sandbox defaults for resolver
	sandboxEnabled := sandboxMgr != nil
	sandboxContainerDir := ""
	sandboxWorkspaceAccess := ""
	if sandboxEnabled {
		sbCfg := appCfg.Agents.Defaults.Sandbox
		if sbCfg != nil {
			resolved := sbCfg.ToSandboxConfig()
			sandboxContainerDir = resolved.ContainerWorkdir()
			sandboxWorkspaceAccess = string(resolved.WorkspaceAccess)
		}
	}

	// 5. Set up agent resolver: lazy-creates Loops from DB
	resolver := agent.NewManagedResolver(agent.ResolverDeps{
		AgentStore:        stores.Agents,
		ProviderReg:       providerReg,
		Bus:               msgBus,
		Sessions:          sessStore,
		Tools:             toolsReg,
		ToolPolicy:        toolPE,
		Skills:            skillsLoader,
		HasMemory:         hasMemory,
		TraceCollector:    traceCollector,
		EnsureUserFiles:   ensureUserFiles,
		ContextFileLoader: contextFileLoader,
		BootstrapCleanup:  buildBootstrapCleanup(stores.Agents),
		InjectionAction:   injectionAction,
		MaxMessageChars:        appCfg.Gateway.MaxMessageChars,
		CompactionCfg:          appCfg.Agents.Defaults.Compaction,
		ContextPruningCfg:      appCfg.Agents.Defaults.ContextPruning,
		SandboxEnabled:         sandboxEnabled,
		SandboxContainerDir:    sandboxContainerDir,
		SandboxWorkspaceAccess: sandboxWorkspaceAccess,
		DynamicLoader:          dynamicLoader,
		AgentLinkStore:         stores.AgentLinks,
		TeamStore:              stores.Teams,
		BuiltinToolStore:       stores.BuiltinTools,
		OnEvent: func(event agent.AgentEvent) {
			msgBus.Broadcast(bus.Event{
				Name:    protocol.EventAgent,
				Payload: event,
			})
		},
	})
	agentRouter.SetResolver(resolver)

	// Wire virtual FS interceptors: route context + memory file reads/writes to DB.
	// Share ONE ContextFileInterceptor instance between read_file and write_file
	// so they share the same cache.
	if readTool, ok := toolsReg.Get("read_file"); ok {
		if ia, ok := readTool.(tools.InterceptorAware); ok {
			if contextFileInterceptor != nil {
				ia.SetContextFileInterceptor(contextFileInterceptor)
			}
			if stores.Memory != nil {
				ia.SetMemoryInterceptor(tools.NewMemoryInterceptor(stores.Memory, workspace))
			}
		}
	}
	if writeTool, ok := toolsReg.Get("write_file"); ok {
		if ia, ok := writeTool.(tools.InterceptorAware); ok {
			if contextFileInterceptor != nil {
				ia.SetContextFileInterceptor(contextFileInterceptor)
			}
			if stores.Memory != nil {
				ia.SetMemoryInterceptor(tools.NewMemoryInterceptor(stores.Memory, workspace))
			}
		}
	}
	if editTool, ok := toolsReg.Get("edit"); ok {
		if ia, ok := editTool.(tools.InterceptorAware); ok {
			if contextFileInterceptor != nil {
				ia.SetContextFileInterceptor(contextFileInterceptor)
			}
			if stores.Memory != nil {
				ia.SetMemoryInterceptor(tools.NewMemoryInterceptor(stores.Memory, workspace))
			}
		}
	}

	// Wire memory store on memory tools (search + get)
	if stores.Memory != nil {
		if searchTool, ok := toolsReg.Get("memory_search"); ok {
			if ms, ok := searchTool.(tools.MemoryStoreAware); ok {
				ms.SetMemoryStore(stores.Memory)
			}
		}
		if getTool, ok := toolsReg.Get("memory_get"); ok {
			if ms, ok := getTool.(tools.MemoryStoreAware); ok {
				ms.SetMemoryStore(stores.Memory)
			}
		}
		slog.Info("memory layering enabled (Postgres)")
	}

	// --- Cache invalidation event subscribers ---

	// Context file cache: invalidate on agent/context data changes
	if contextFileInterceptor != nil {
		msgBus.Subscribe("cache:bootstrap", func(event bus.Event) {
			if event.Name != protocol.EventCacheInvalidate {
				return
			}
			payload, ok := event.Payload.(bus.CacheInvalidatePayload)
			if !ok {
				return
			}
			if payload.Kind == "bootstrap" || payload.Kind == "agent" {
				if payload.Key != "" {
					agentID, err := uuid.Parse(payload.Key)
					if err == nil {
						contextFileInterceptor.InvalidateAgent(agentID)
					}
				} else {
					contextFileInterceptor.InvalidateAll()
				}
			}
		})
	}

	// Agent router: invalidate Loop cache on agent config changes
	msgBus.Subscribe("cache:agent", func(event bus.Event) {
		if event.Name != protocol.EventCacheInvalidate {
			return
		}
		payload, ok := event.Payload.(bus.CacheInvalidatePayload)
		if !ok || payload.Kind != "agent" {
			return
		}
		if payload.Key != "" {
			agentRouter.InvalidateAgent(payload.Key)
		}
	})

	// Skills cache: bump version on skill changes
	if stores.Skills != nil {
		msgBus.Subscribe("cache:skills", func(event bus.Event) {
			if event.Name != protocol.EventCacheInvalidate {
				return
			}
			payload, ok := event.Payload.(bus.CacheInvalidatePayload)
			if !ok || payload.Kind != "skills" {
				return
			}
			stores.Skills.BumpVersion()
		})
	}

	// Cron cache: invalidate job cache on cron changes
	if ci, ok := stores.Cron.(store.CacheInvalidatable); ok {
		msgBus.Subscribe("cache:cron", func(event bus.Event) {
			if event.Name != protocol.EventCacheInvalidate {
				return
			}
			payload, ok := event.Payload.(bus.CacheInvalidatePayload)
			if !ok || payload.Kind != "cron" {
				return
			}
			ci.InvalidateCache()
		})
	}

	// Custom tools cache: reload global tools on create/update/delete
	if dynamicLoader != nil {
		msgBus.Subscribe("cache:custom_tools", func(event bus.Event) {
			if event.Name != protocol.EventCacheInvalidate {
				return
			}
			payload, ok := event.Payload.(bus.CacheInvalidatePayload)
			if !ok || payload.Kind != "custom_tools" {
				return
			}
			dynamicLoader.ReloadGlobal(context.Background(), toolsReg)
			// Invalidate all agent caches so they re-resolve with updated tools
			agentRouter.InvalidateAll()
		})
	}

	// Builtin tools cache: re-apply disables on settings/enabled changes
	if stores.BuiltinTools != nil {
		msgBus.Subscribe("cache:builtin_tools", func(event bus.Event) {
			if event.Name != protocol.EventCacheInvalidate {
				return
			}
			payload, ok := event.Payload.(bus.CacheInvalidatePayload)
			if !ok || payload.Kind != bus.CacheKindBuiltinTools {
				return
			}
			applyBuiltinToolDisables(context.Background(), stores.BuiltinTools, toolsReg)
			agentRouter.InvalidateAll()
		})
	}

	// Register delegate tool (inter-agent delegation) if link store is available.
	// Uses a callback to bridge tools.DelegateRunRequest → agent.RunRequest,
	// avoiding import cycle between tools and agent packages.
	if stores.AgentLinks != nil && stores.Agents != nil {
		runAgentFn := func(ctx context.Context, agentKey string, req tools.DelegateRunRequest) (*tools.DelegateRunResult, error) {
			loop, err := agentRouter.Get(agentKey)
			if err != nil {
				return nil, err
			}
			result, err := loop.Run(ctx, agent.RunRequest{
				SessionKey:        req.SessionKey,
				Message:           req.Message,
				UserID:            req.UserID,
				Channel:           req.Channel,
				ChatID:            req.ChatID,
				PeerKind:          req.PeerKind,
				RunID:             req.RunID,
				Stream:            req.Stream,
				ExtraSystemPrompt: req.ExtraSystemPrompt,
			})
			if err != nil {
				return nil, err
			}
			dr := &tools.DelegateRunResult{
				Content:    result.Content,
				Iterations: result.Iterations,
			}
			for _, m := range result.Media {
				dr.MediaPaths = append(dr.MediaPaths, m.Path)
			}
			return dr, nil
		}
		delegateMgr := tools.NewDelegateManager(runAgentFn, stores.AgentLinks, stores.Agents, msgBus)
		if stores.Teams != nil {
			delegateMgr.SetTeamStore(stores.Teams)
		}
		delegateMgr.SetSessionStore(stores.Sessions)

		// Hook engine (quality gates)
		hookEngine := hooks.NewEngine()
		hookEngine.RegisterEvaluator(hooks.HookTypeCommand, hooks.NewCommandEvaluator(workspace))
		agentEvalFn := func(ctx context.Context, agentKey, task string) (string, error) {
			result, err := delegateMgr.Delegate(hooks.WithSkipHooks(ctx, true), tools.DelegateOpts{
				TargetAgentKey: agentKey, Task: task, Mode: "sync",
			})
			if err != nil {
				return "", err
			}
			return result.Content, nil
		}
		hookEngine.RegisterEvaluator(hooks.HookTypeAgent, hooks.NewAgentEvaluator(agentEvalFn))
		delegateMgr.SetHookEngine(hookEngine)

		// Evaluate-optimize loop tool
		toolsReg.Register(tools.NewEvaluateLoopTool(delegateMgr))

		// Handoff tool (agent-to-agent conversation transfer)
		toolsReg.Register(tools.NewHandoffTool(delegateMgr, stores.Teams, stores.Sessions, msgBus))

		// Inject delegation capability into existing SpawnTool
		if st, ok := toolsReg.Get("spawn"); ok {
			if spawnTool, ok := st.(*tools.SpawnTool); ok {
				spawnTool.SetDelegateManager(delegateMgr)
				slog.Info("spawn tool: delegation enabled")
			}
		}

		// Register delegate_search tool (hybrid FTS + semantic agent discovery)
		var delegateEmbProvider store.EmbeddingProvider
		if agentStore, ok := stores.Agents.(*pg.PGAgentStore); ok {
			memCfg := appCfg.Agents.Defaults.Memory
			if embProvider := resolveEmbeddingProvider(appCfg, memCfg); embProvider != nil {
				agentStore.SetEmbeddingProvider(embProvider)
				delegateEmbProvider = embProvider
				slog.Info("managed mode: agent embeddings enabled")

				// Backfill embeddings for existing agents with frontmatter
				go func() {
					count, err := agentStore.BackfillAgentEmbeddings(context.Background())
					if err != nil {
						slog.Warn("agent embeddings backfill failed", "error", err)
					} else if count > 0 {
						slog.Info("agent embeddings backfill complete", "updated", count)
					}
				}()
			}
		}
		toolsReg.Register(tools.NewDelegateSearchTool(stores.AgentLinks, delegateEmbProvider))
		slog.Info("managed mode: delegate + delegate_search tools registered")
	}

	// Register team tools (team_tasks + team_message) if team store is available.
	if stores.Teams != nil && stores.Agents != nil {
		teamMgr := tools.NewTeamToolManager(stores.Teams, stores.Agents, msgBus)
		toolsReg.Register(tools.NewTeamTasksTool(teamMgr))
		toolsReg.Register(tools.NewTeamMessageTool(teamMgr))
		slog.Info("managed mode: team tools registered")
	}

	slog.Info("managed mode: resolver + interceptors + cache subscribers wired")
	return contextFileInterceptor
}

// wireManagedHTTP creates managed-mode HTTP handlers (agents + skills + traces + MCP + custom tools + channel instances + providers + delegations + builtin tools).
func wireManagedHTTP(stores *store.Stores, token string, msgBus *bus.MessageBus, toolsReg *tools.Registry, providerReg *providers.Registry, isOwner func(string) bool) (*httpapi.AgentsHandler, *httpapi.SkillsHandler, *httpapi.TracesHandler, *httpapi.MCPHandler, *httpapi.CustomToolsHandler, *httpapi.ChannelInstancesHandler, *httpapi.ProvidersHandler, *httpapi.DelegationsHandler, *httpapi.BuiltinToolsHandler) {
	var agentsH *httpapi.AgentsHandler
	var skillsH *httpapi.SkillsHandler
	var tracesH *httpapi.TracesHandler
	var mcpH *httpapi.MCPHandler
	var customToolsH *httpapi.CustomToolsHandler
	var channelInstancesH *httpapi.ChannelInstancesHandler
	var providersH *httpapi.ProvidersHandler
	var delegationsH *httpapi.DelegationsHandler
	var builtinToolsH *httpapi.BuiltinToolsHandler

	if stores != nil && stores.Agents != nil {
		var summoner *httpapi.AgentSummoner
		if providerReg != nil {
			summoner = httpapi.NewAgentSummoner(stores.Agents, providerReg, msgBus)
		}
		agentsH = httpapi.NewAgentsHandler(stores.Agents, token, msgBus, summoner, isOwner)
	}

	if stores != nil && stores.Skills != nil {
		if pgSkills, ok := stores.Skills.(*pg.PGSkillStore); ok {
			dirs := pgSkills.Dirs()
			if len(dirs) > 0 {
				skillsH = httpapi.NewSkillsHandler(pgSkills, dirs[0], token)
			}
		}
	}

	if stores != nil && stores.Tracing != nil {
		tracesH = httpapi.NewTracesHandler(stores.Tracing, token)
	}

	if stores != nil && stores.MCP != nil {
		mcpH = httpapi.NewMCPHandler(stores.MCP, token)
	}

	if stores != nil && stores.CustomTools != nil {
		customToolsH = httpapi.NewCustomToolsHandler(stores.CustomTools, token, msgBus, toolsReg)
	}

	if stores != nil && stores.ChannelInstances != nil {
		channelInstancesH = httpapi.NewChannelInstancesHandler(stores.ChannelInstances, token, msgBus)
	}

	if stores != nil && stores.Providers != nil {
		providersH = httpapi.NewProvidersHandler(stores.Providers, token, providerReg)
	}

	if stores != nil && stores.Teams != nil {
		delegationsH = httpapi.NewDelegationsHandler(stores.Teams, token)
	}

	if stores != nil && stores.BuiltinTools != nil {
		builtinToolsH = httpapi.NewBuiltinToolsHandler(stores.BuiltinTools, token, msgBus)
	}

	return agentsH, skillsH, tracesH, mcpH, customToolsH, channelInstancesH, providersH, delegationsH, builtinToolsH
}
