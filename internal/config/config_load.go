package config

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/titanous/json5"
)

// Default returns a Config with sensible defaults.
func Default() *Config {
	return &Config{
		Agents: AgentsConfig{
			Defaults: AgentDefaults{
				Workspace:           "~/.goclaw/workspace",
				RestrictToWorkspace: true,
				Provider:            "anthropic",
				Model:               "claude-sonnet-4-5-20250929",
				MaxTokens:           8192,
				Temperature:         0.7,
				MaxToolIterations:   20,
				ContextWindow:       200000,
				Subagents: &SubagentsConfig{
					MaxConcurrent: 20,
					MaxSpawnDepth: 1,
				},
			},
		},
		Channels: ChannelsConfig{
			Telegram: TelegramConfig{
				StreamMode:    "none",
				ReactionLevel: "full",
			},
		},
		Gateway: GatewayConfig{
			Host:            "0.0.0.0",
			Port:            18790,
			MaxMessageChars: 32000,
			RateLimitRPM:    20,
		},
		Tools: ToolsConfig{
			Web: WebToolsConfig{
				DuckDuckGo: DuckDuckGoConfig{Enabled: true, MaxResults: 5},
			},
			Browser: BrowserToolConfig{
				Enabled:  true,
				Headless: true,
			},
			ExecApproval: ExecApprovalCfg{
				Security: "full",
				Ask:      "off",
			},
		},
		Sessions: SessionsConfig{
			Storage: "~/.goclaw/sessions",
		},
	}
}

// Load reads config from a JSON file, then overlays env vars.
func Load(path string) (*Config, error) {
	cfg := Default()

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return nil, fmt.Errorf("read config: %w", err)
	}

	if err := json5.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	cfg.applyEnvOverrides()
	cfg.applyContextPruningDefaults()
	return cfg, nil
}

// applyEnvOverrides overlays env vars onto the config.
// Env vars take precedence over file values.
func (c *Config) applyEnvOverrides() {
	envStr := func(key string, dst *string) {
		if v := os.Getenv(key); v != "" {
			*dst = v
		}
	}
	envStr("GOCLAW_ANTHROPIC_API_KEY", &c.Providers.Anthropic.APIKey)
	envStr("GOCLAW_OPENAI_API_KEY", &c.Providers.OpenAI.APIKey)
	envStr("GOCLAW_OPENROUTER_API_KEY", &c.Providers.OpenRouter.APIKey)
	envStr("GOCLAW_GROQ_API_KEY", &c.Providers.Groq.APIKey)
	envStr("GOCLAW_DEEPSEEK_API_KEY", &c.Providers.DeepSeek.APIKey)
	envStr("GOCLAW_GEMINI_API_KEY", &c.Providers.Gemini.APIKey)
	envStr("GOCLAW_MISTRAL_API_KEY", &c.Providers.Mistral.APIKey)
	envStr("GOCLAW_XAI_API_KEY", &c.Providers.XAI.APIKey)
	envStr("GOCLAW_MINIMAX_API_KEY", &c.Providers.MiniMax.APIKey)
	envStr("GOCLAW_COHERE_API_KEY", &c.Providers.Cohere.APIKey)
	envStr("GOCLAW_PERPLEXITY_API_KEY", &c.Providers.Perplexity.APIKey)
	envStr("GOCLAW_GATEWAY_TOKEN", &c.Gateway.Token)
	envStr("GOCLAW_TELEGRAM_TOKEN", &c.Channels.Telegram.Token)
	envStr("GOCLAW_ZALO_TOKEN", &c.Channels.Zalo.Token)
	envStr("GOCLAW_FEISHU_APP_ID", &c.Channels.Feishu.AppID)
	envStr("GOCLAW_FEISHU_APP_SECRET", &c.Channels.Feishu.AppSecret)
	envStr("GOCLAW_FEISHU_ENCRYPT_KEY", &c.Channels.Feishu.EncryptKey)
	envStr("GOCLAW_FEISHU_VERIFICATION_TOKEN", &c.Channels.Feishu.VerificationToken)

	// TTS secrets
	envStr("GOCLAW_TTS_OPENAI_API_KEY", &c.Tts.OpenAI.APIKey)
	envStr("GOCLAW_TTS_ELEVENLABS_API_KEY", &c.Tts.ElevenLabs.APIKey)
	envStr("GOCLAW_TTS_MINIMAX_API_KEY", &c.Tts.MiniMax.APIKey)
	envStr("GOCLAW_TTS_MINIMAX_GROUP_ID", &c.Tts.MiniMax.GroupID)

	// Auto-enable channels if credentials are provided via env
	if c.Channels.Telegram.Token != "" {
		c.Channels.Telegram.Enabled = true
	}
	if c.Channels.Zalo.Token != "" {
		c.Channels.Zalo.Enabled = true
	}
	if c.Channels.Feishu.AppID != "" && c.Channels.Feishu.AppSecret != "" {
		c.Channels.Feishu.Enabled = true
	}

	// Allow overriding default provider/model
	envStr("GOCLAW_PROVIDER", &c.Agents.Defaults.Provider)
	envStr("GOCLAW_MODEL", &c.Agents.Defaults.Model)

	// Workspace & sessions
	envStr("GOCLAW_WORKSPACE", &c.Agents.Defaults.Workspace)
	envStr("GOCLAW_SESSIONS_STORAGE", &c.Sessions.Storage)

	// Gateway host/port
	envStr("GOCLAW_HOST", &c.Gateway.Host)
	if v := os.Getenv("GOCLAW_PORT"); v != "" {
		if port, err := strconv.Atoi(v); err == nil && port > 0 {
			c.Gateway.Port = port
		}
	}

	// Database
	envStr("GOCLAW_POSTGRES_DSN", &c.Database.PostgresDSN)
	envStr("GOCLAW_MODE", &c.Database.Mode)

	// Telemetry
	envStr("GOCLAW_TELEMETRY_ENDPOINT", &c.Telemetry.Endpoint)
	envStr("GOCLAW_TELEMETRY_PROTOCOL", &c.Telemetry.Protocol)
	envStr("GOCLAW_TELEMETRY_SERVICE_NAME", &c.Telemetry.ServiceName)
	if v := os.Getenv("GOCLAW_TELEMETRY_ENABLED"); v != "" {
		c.Telemetry.Enabled = v == "true" || v == "1"
	}
	if v := os.Getenv("GOCLAW_TELEMETRY_INSECURE"); v != "" {
		c.Telemetry.Insecure = v == "true" || v == "1"
	}

	// Owner IDs from env (comma-separated)
	if v := os.Getenv("GOCLAW_OWNER_IDS"); v != "" {
		c.Gateway.OwnerIDs = strings.Split(v, ",")
	}

	// Tailscale (tsnet)
	envStr("GOCLAW_TSNET_HOSTNAME", &c.Tailscale.Hostname)
	envStr("GOCLAW_TSNET_AUTH_KEY", &c.Tailscale.AuthKey)
	envStr("GOCLAW_TSNET_DIR", &c.Tailscale.StateDir)
}

// applyContextPruningDefaults auto-enables context pruning when the Anthropic
// provider is configured, matching TS applyContextPruningDefaults() in
// src/config/defaults.ts.
//
// Go port does not have OAuth vs API-key distinction — we always treat it as
// API-key mode (heartbeat 30m).
func (c *Config) applyContextPruningDefaults() {
	// Only apply when Anthropic is configured.
	if c.Providers.Anthropic.APIKey == "" {
		return
	}

	defaults := &c.Agents.Defaults

	// Auto-enable context pruning if mode not explicitly set.
	if defaults.ContextPruning == nil {
		defaults.ContextPruning = &ContextPruningConfig{
			Mode: "cache-ttl",
		}
	} else if defaults.ContextPruning.Mode == "" {
		defaults.ContextPruning.Mode = "cache-ttl"
	}
}

// Save writes the config to a JSON file.
func Save(path string, cfg *Config) error {
	cfg.mu.RLock()
	defer cfg.mu.RUnlock()

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	return os.WriteFile(path, data, 0600)
}

// Hash returns a SHA-256 hash of the config for optimistic concurrency.
func (c *Config) Hash() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	data, _ := json.Marshal(c)
	h := sha256.Sum256(data)
	return fmt.Sprintf("%x", h[:8])
}

// WorkspacePath returns the expanded workspace path.
func (c *Config) WorkspacePath() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return ExpandHome(c.Agents.Defaults.Workspace)
}

// ResolveAgent returns the effective config for a given agent ID,
// merging defaults with per-agent overrides.
func (c *Config) ResolveAgent(agentID string) AgentDefaults {
	c.mu.RLock()
	defer c.mu.RUnlock()

	d := c.Agents.Defaults
	if spec, ok := c.Agents.List[agentID]; ok {
		if spec.Provider != "" {
			d.Provider = spec.Provider
		}
		if spec.Model != "" {
			d.Model = spec.Model
		}
		if spec.MaxTokens > 0 {
			d.MaxTokens = spec.MaxTokens
		}
		if spec.Temperature > 0 {
			d.Temperature = spec.Temperature
		}
		if spec.MaxToolIterations > 0 {
			d.MaxToolIterations = spec.MaxToolIterations
		}
		if spec.ContextWindow > 0 {
			d.ContextWindow = spec.ContextWindow
		}
		if spec.Workspace != "" {
			d.Workspace = spec.Workspace
		}
		if spec.Sandbox != nil {
			d.Sandbox = spec.Sandbox
		}
	}

	return d
}

// ResolveDefaultAgentID returns the ID of the agent marked as default,
// or "default" if none is explicitly marked.
func (c *Config) ResolveDefaultAgentID() string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	for id, spec := range c.Agents.List {
		if spec.Default {
			return id
		}
	}
	return DefaultAgentID
}

// ResolveDisplayName returns the display name for an agent.
// Falls back to "GoClaw" if not configured.
func (c *Config) ResolveDisplayName(agentID string) string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if spec, ok := c.Agents.List[agentID]; ok && spec.DisplayName != "" {
		return spec.DisplayName
	}
	return "GoClaw"
}

const secretMask = "***"

// MaskedCopy returns a deep copy of the config with all secret fields masked.
// Used by config.get to avoid exposing secrets to WebSocket clients.
func (c *Config) MaskedCopy() *Config {
	c.mu.RLock()
	defer c.mu.RUnlock()

	// Deep copy via JSON round-trip
	data, err := json.Marshal(c)
	if err != nil {
		return &Config{}
	}
	cp := Default()
	if err := json.Unmarshal(data, cp); err != nil {
		return &Config{}
	}

	// Mask provider API keys
	maskNonEmpty(&cp.Providers.Anthropic.APIKey)
	maskNonEmpty(&cp.Providers.OpenAI.APIKey)
	maskNonEmpty(&cp.Providers.OpenRouter.APIKey)
	maskNonEmpty(&cp.Providers.Groq.APIKey)
	maskNonEmpty(&cp.Providers.DeepSeek.APIKey)
	maskNonEmpty(&cp.Providers.Gemini.APIKey)
	maskNonEmpty(&cp.Providers.Mistral.APIKey)
	maskNonEmpty(&cp.Providers.XAI.APIKey)
	maskNonEmpty(&cp.Providers.MiniMax.APIKey)
	maskNonEmpty(&cp.Providers.Cohere.APIKey)
	maskNonEmpty(&cp.Providers.Perplexity.APIKey)

	// Mask gateway token
	maskNonEmpty(&cp.Gateway.Token)

	// Mask channel secrets
	maskNonEmpty(&cp.Channels.Telegram.Token)
	maskNonEmpty(&cp.Channels.Discord.Token)
	maskNonEmpty(&cp.Channels.Slack.BotToken)
	maskNonEmpty(&cp.Channels.Slack.AppToken)
	maskNonEmpty(&cp.Channels.Zalo.Token)
	maskNonEmpty(&cp.Channels.Zalo.WebhookSecret)
	maskNonEmpty(&cp.Channels.Feishu.AppID)
	maskNonEmpty(&cp.Channels.Feishu.AppSecret)
	maskNonEmpty(&cp.Channels.Feishu.EncryptKey)
	maskNonEmpty(&cp.Channels.Feishu.VerificationToken)

	// Mask TTS API keys
	maskNonEmpty(&cp.Tts.OpenAI.APIKey)
	maskNonEmpty(&cp.Tts.ElevenLabs.APIKey)
	maskNonEmpty(&cp.Tts.MiniMax.APIKey)

	// Mask web tool keys
	maskNonEmpty(&cp.Tools.Web.Brave.APIKey)

	// Mask Tailscale auth key
	maskNonEmpty(&cp.Tailscale.AuthKey)

	return cp
}

// StripSecrets zeros out all secret fields in the config.
// Used before saving to disk to ensure secrets never persist in config.json.
func (c *Config) StripSecrets() {
	// Provider API keys
	c.Providers.Anthropic.APIKey = ""
	c.Providers.OpenAI.APIKey = ""
	c.Providers.OpenRouter.APIKey = ""
	c.Providers.Groq.APIKey = ""
	c.Providers.DeepSeek.APIKey = ""
	c.Providers.Gemini.APIKey = ""
	c.Providers.Mistral.APIKey = ""
	c.Providers.XAI.APIKey = ""
	c.Providers.MiniMax.APIKey = ""
	c.Providers.Cohere.APIKey = ""
	c.Providers.Perplexity.APIKey = ""

	// Gateway token
	c.Gateway.Token = ""

	// Channel secrets
	c.Channels.Telegram.Token = ""
	c.Channels.Discord.Token = ""
	c.Channels.Slack.BotToken = ""
	c.Channels.Slack.AppToken = ""
	c.Channels.Zalo.Token = ""
	c.Channels.Zalo.WebhookSecret = ""
	c.Channels.Feishu.AppID = ""
	c.Channels.Feishu.AppSecret = ""
	c.Channels.Feishu.EncryptKey = ""
	c.Channels.Feishu.VerificationToken = ""

	// TTS API keys
	c.Tts.OpenAI.APIKey = ""
	c.Tts.ElevenLabs.APIKey = ""
	c.Tts.MiniMax.APIKey = ""

	// Web tool keys
	c.Tools.Web.Brave.APIKey = ""

	// Tailscale auth key
	c.Tailscale.AuthKey = ""
}

// StripMaskedSecrets strips only fields that still contain the mask value "***".
// Real values (user-entered via UI) are preserved. Used in standalone mode
// so that secrets entered via the config UI persist in config.json.
func (c *Config) StripMaskedSecrets() {
	stripIfMasked := func(s *string) {
		if *s == secretMask {
			*s = ""
		}
	}

	// Provider API keys
	stripIfMasked(&c.Providers.Anthropic.APIKey)
	stripIfMasked(&c.Providers.OpenAI.APIKey)
	stripIfMasked(&c.Providers.OpenRouter.APIKey)
	stripIfMasked(&c.Providers.Groq.APIKey)
	stripIfMasked(&c.Providers.DeepSeek.APIKey)
	stripIfMasked(&c.Providers.Gemini.APIKey)
	stripIfMasked(&c.Providers.Mistral.APIKey)
	stripIfMasked(&c.Providers.XAI.APIKey)
	stripIfMasked(&c.Providers.MiniMax.APIKey)
	stripIfMasked(&c.Providers.Cohere.APIKey)
	stripIfMasked(&c.Providers.Perplexity.APIKey)

	// Gateway token
	stripIfMasked(&c.Gateway.Token)

	// Channel secrets
	stripIfMasked(&c.Channels.Telegram.Token)
	stripIfMasked(&c.Channels.Discord.Token)
	stripIfMasked(&c.Channels.Slack.BotToken)
	stripIfMasked(&c.Channels.Slack.AppToken)
	stripIfMasked(&c.Channels.Zalo.Token)
	stripIfMasked(&c.Channels.Zalo.WebhookSecret)
	stripIfMasked(&c.Channels.Feishu.AppID)
	stripIfMasked(&c.Channels.Feishu.AppSecret)
	stripIfMasked(&c.Channels.Feishu.EncryptKey)
	stripIfMasked(&c.Channels.Feishu.VerificationToken)

	// TTS API keys
	stripIfMasked(&c.Tts.OpenAI.APIKey)
	stripIfMasked(&c.Tts.ElevenLabs.APIKey)
	stripIfMasked(&c.Tts.MiniMax.APIKey)

	// Web tool keys
	stripIfMasked(&c.Tools.Web.Brave.APIKey)

	// Tailscale auth key
	stripIfMasked(&c.Tailscale.AuthKey)
}

// ApplyDBSecrets overlays secrets from the config_secrets table onto the config.
// Called before ApplyEnvOverrides() — env vars take highest precedence.
// Precedence chain: config.json defaults → DB secrets → env vars.
func (c *Config) ApplyDBSecrets(secrets map[string]string) {
	apply := func(key string, dst *string) {
		if v, ok := secrets[key]; ok && v != "" {
			*dst = v
		}
	}

	apply("gateway.token", &c.Gateway.Token)
	apply("tts.openai.api_key", &c.Tts.OpenAI.APIKey)
	apply("tts.elevenlabs.api_key", &c.Tts.ElevenLabs.APIKey)
	apply("tts.minimax.api_key", &c.Tts.MiniMax.APIKey)
	apply("tts.minimax.group_id", &c.Tts.MiniMax.GroupID)
	apply("tools.web.brave.api_key", &c.Tools.Web.Brave.APIKey)
	apply("tailscale.auth_key", &c.Tailscale.AuthKey)
}

// ExtractDBSecrets returns the config_secrets key-value pairs from the config.
// Used by managed mode to save secrets to the config_secrets table.
func (c *Config) ExtractDBSecrets() map[string]string {
	secrets := make(map[string]string)

	collect := func(key, value string) {
		if value != "" && value != secretMask {
			secrets[key] = value
		}
	}

	collect("gateway.token", c.Gateway.Token)
	collect("tts.openai.api_key", c.Tts.OpenAI.APIKey)
	collect("tts.elevenlabs.api_key", c.Tts.ElevenLabs.APIKey)
	collect("tts.minimax.api_key", c.Tts.MiniMax.APIKey)
	collect("tts.minimax.group_id", c.Tts.MiniMax.GroupID)
	collect("tools.web.brave.api_key", c.Tools.Web.Brave.APIKey)
	collect("tailscale.auth_key", c.Tailscale.AuthKey)

	return secrets
}

// ApplyEnvOverrides re-applies environment variable overrides onto the config.
// Call this after modifying config to restore runtime secrets from env vars.
func (c *Config) ApplyEnvOverrides() {
	c.applyEnvOverrides()
	c.applyContextPruningDefaults()
}

func maskNonEmpty(s *string) {
	if *s != "" {
		*s = secretMask
	}
}

// ExpandHome replaces leading ~ with the user home directory.
func ExpandHome(path string) string {
	if path == "" || path[0] != '~' {
		return path
	}
	home, _ := os.UserHomeDir()
	if len(path) > 1 && path[1] == '/' {
		return home + path[1:]
	}
	return home
}
