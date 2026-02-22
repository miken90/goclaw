package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/nextlevelbuilder/goclaw/internal/bus"
	"github.com/nextlevelbuilder/goclaw/internal/store"
)

// ============================================================
// sessions_list
// ============================================================

type SessionsListTool struct {
	sessions store.SessionStore
}

func NewSessionsListTool() *SessionsListTool { return &SessionsListTool{} }

func (t *SessionsListTool) SetSessionStore(s store.SessionStore) { t.sessions = s }

func (t *SessionsListTool) Name() string { return "sessions_list" }
func (t *SessionsListTool) Description() string {
	return "List sessions for this agent with optional filters."
}

func (t *SessionsListTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"limit": map[string]interface{}{
				"type":        "number",
				"description": "Max sessions to return (default 20)",
			},
			"active_minutes": map[string]interface{}{
				"type":        "number",
				"description": "Only show sessions active in the last N minutes",
			},
		},
	}
}

func (t *SessionsListTool) Execute(ctx context.Context, args map[string]interface{}) *Result {
	if t.sessions == nil {
		return ErrorResult("session store not available")
	}

	limit := 20
	if v, ok := args["limit"].(float64); ok && int(v) > 0 {
		limit = int(v)
	}

	var activeMinutes int
	if v, ok := args["active_minutes"].(float64); ok && int(v) > 0 {
		activeMinutes = int(v)
	}

	agentID := resolveAgentIDString(ctx)
	sessions := t.sessions.List(agentID)

	// Filter by active_minutes
	if activeMinutes > 0 {
		cutoff := time.Now().Add(-time.Duration(activeMinutes) * time.Minute)
		var filtered []store.SessionInfo
		for _, s := range sessions {
			if s.Updated.After(cutoff) {
				filtered = append(filtered, s)
			}
		}
		sessions = filtered
	}

	// Apply limit
	if len(sessions) > limit {
		sessions = sessions[:limit]
	}

	type sessionEntry struct {
		Key          string `json:"key"`
		MessageCount int    `json:"message_count"`
		Updated      string `json:"updated"`
	}

	entries := make([]sessionEntry, 0, len(sessions))
	for _, s := range sessions {
		entries = append(entries, sessionEntry{
			Key:          s.Key,
			MessageCount: s.MessageCount,
			Updated:      s.Updated.Format(time.RFC3339),
		})
	}

	out, _ := json.Marshal(map[string]interface{}{
		"count":    len(entries),
		"sessions": entries,
	})
	return SilentResult(string(out))
}

// ============================================================
// session_status
// ============================================================

type SessionStatusTool struct {
	sessions store.SessionStore
}

func NewSessionStatusTool() *SessionStatusTool { return &SessionStatusTool{} }

func (t *SessionStatusTool) SetSessionStore(s store.SessionStore) { t.sessions = s }

func (t *SessionStatusTool) Name() string { return "session_status" }
func (t *SessionStatusTool) Description() string {
	return "Show session status: model, tokens, compaction count, channel, last update."
}

func (t *SessionStatusTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"session_key": map[string]interface{}{
				"type":        "string",
				"description": "Session key to inspect (default: current session)",
			},
		},
	}
}

func (t *SessionStatusTool) Execute(ctx context.Context, args map[string]interface{}) *Result {
	if t.sessions == nil {
		return ErrorResult("session store not available")
	}

	sessionKey, _ := args["session_key"].(string)
	if sessionKey == "" {
		sessionKey = ToolSandboxKeyFromCtx(ctx) // sandboxKey == sessionKey in registry
	}
	if sessionKey == "" {
		return ErrorResult("session_key is required (could not detect current session)")
	}

	// Security: validate session belongs to current agent
	agentID := resolveAgentIDString(ctx)
	if agentID != "" && !strings.HasPrefix(sessionKey, "agent:"+agentID+":") {
		return ErrorResult("access denied: session belongs to a different agent")
	}

	data := t.sessions.GetOrCreate(sessionKey)

	var lines []string
	lines = append(lines, fmt.Sprintf("Session: %s", data.Key))
	if data.Model != "" {
		lines = append(lines, fmt.Sprintf("Model: %s", data.Model))
	}
	if data.Provider != "" {
		lines = append(lines, fmt.Sprintf("Provider: %s", data.Provider))
	}
	if data.Channel != "" {
		lines = append(lines, fmt.Sprintf("Channel: %s", data.Channel))
	}
	lines = append(lines, fmt.Sprintf("Messages: %d", len(data.Messages)))
	lines = append(lines, fmt.Sprintf("Tokens: %d input / %d output", data.InputTokens, data.OutputTokens))
	lines = append(lines, fmt.Sprintf("Compactions: %d", data.CompactionCount))
	if data.Summary != "" {
		lines = append(lines, fmt.Sprintf("Has summary: yes (%d chars)", len(data.Summary)))
	}
	if data.Label != "" {
		lines = append(lines, fmt.Sprintf("Label: %s", data.Label))
	}
	lines = append(lines, fmt.Sprintf("Updated: %s", data.Updated.Format(time.RFC3339)))

	return SilentResult(strings.Join(lines, "\n"))
}

// ============================================================
// sessions_history
// ============================================================

const (
	historyMaxCharsPerMessage = 4000
	historyMaxTotalBytes      = 80 * 1024
)

type SessionsHistoryTool struct {
	sessions store.SessionStore
}

func NewSessionsHistoryTool() *SessionsHistoryTool { return &SessionsHistoryTool{} }

func (t *SessionsHistoryTool) SetSessionStore(s store.SessionStore) { t.sessions = s }

func (t *SessionsHistoryTool) Name() string { return "sessions_history" }
func (t *SessionsHistoryTool) Description() string {
	return "Fetch message history for a session."
}

func (t *SessionsHistoryTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"session_key": map[string]interface{}{
				"type":        "string",
				"description": "Session key to fetch history from",
			},
			"limit": map[string]interface{}{
				"type":        "number",
				"description": "Max messages to return (default 20)",
			},
			"include_tools": map[string]interface{}{
				"type":        "boolean",
				"description": "Include tool call/result messages (default false)",
			},
		},
		"required": []string{"session_key"},
	}
}

func (t *SessionsHistoryTool) Execute(ctx context.Context, args map[string]interface{}) *Result {
	if t.sessions == nil {
		return ErrorResult("session store not available")
	}

	sessionKey, _ := args["session_key"].(string)
	if sessionKey == "" {
		return ErrorResult("session_key is required")
	}

	limit := 20
	if v, ok := args["limit"].(float64); ok && int(v) > 0 {
		limit = int(v)
	}

	includeTools, _ := args["include_tools"].(bool)

	// Security: validate session belongs to current agent
	agentID := resolveAgentIDString(ctx)
	if agentID != "" && !strings.HasPrefix(sessionKey, "agent:"+agentID+":") {
		return ErrorResult("access denied: session belongs to a different agent")
	}

	history := t.sessions.GetHistory(sessionKey)
	if history == nil {
		return SilentResult(`{"session_key":"` + sessionKey + `","messages":[],"count":0}`)
	}

	// Filter tool messages
	type msgEntry struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	var entries []msgEntry
	for _, m := range history {
		if !includeTools && m.Role == "tool" {
			continue
		}
		// Skip assistant messages that are only tool calls with no text
		if !includeTools && m.Role == "assistant" && len(m.ToolCalls) > 0 && strings.TrimSpace(m.Content) == "" {
			continue
		}

		content := m.Content
		// Truncate per-message
		if utf8.RuneCountInString(content) > historyMaxCharsPerMessage {
			runes := []rune(content)
			content = string(runes[:historyMaxCharsPerMessage]) + "... [truncated]"
		}

		entries = append(entries, msgEntry{Role: m.Role, Content: content})
	}

	// Keep last N
	if len(entries) > limit {
		entries = entries[len(entries)-limit:]
	}

	out, _ := json.Marshal(map[string]interface{}{
		"session_key": sessionKey,
		"messages":    entries,
		"count":       len(entries),
	})

	// Cap total bytes
	if len(out) > historyMaxTotalBytes {
		return SilentResult(fmt.Sprintf(
			`{"session_key":"%s","error":"history too large (%d bytes), use smaller limit","count":%d}`,
			sessionKey, len(out), len(entries),
		))
	}

	return SilentResult(string(out))
}

// ============================================================
// sessions_send
// ============================================================

type SessionsSendTool struct {
	sessions store.SessionStore
	msgBus   *bus.MessageBus
}

func NewSessionsSendTool() *SessionsSendTool { return &SessionsSendTool{} }

func (t *SessionsSendTool) SetSessionStore(s store.SessionStore) { t.sessions = s }
func (t *SessionsSendTool) SetMessageBus(b *bus.MessageBus)        { t.msgBus = b }

func (t *SessionsSendTool) Name() string { return "sessions_send" }
func (t *SessionsSendTool) Description() string {
	return "Send a message into another session. Use session_key or label to identify the target."
}

func (t *SessionsSendTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"session_key": map[string]interface{}{
				"type":        "string",
				"description": "Target session key",
			},
			"label": map[string]interface{}{
				"type":        "string",
				"description": "Target session label (alternative to session_key)",
			},
			"message": map[string]interface{}{
				"type":        "string",
				"description": "Message to send",
			},
		},
		"required": []string{"message"},
	}
}

func (t *SessionsSendTool) Execute(ctx context.Context, args map[string]interface{}) *Result {
	if t.sessions == nil {
		return ErrorResult("session store not available")
	}
	if t.msgBus == nil {
		return ErrorResult("message bus not available")
	}

	sessionKey, _ := args["session_key"].(string)
	label, _ := args["label"].(string)
	message, _ := args["message"].(string)

	if message == "" {
		return ErrorResult("message is required")
	}
	if sessionKey == "" && label == "" {
		return ErrorResult("either session_key or label is required")
	}

	agentID := resolveAgentIDString(ctx)

	// Resolve by label if needed
	if sessionKey == "" && label != "" {
		sessions := t.sessions.List(agentID)
		for _, s := range sessions {
			// Check if label matches by loading session data
			data := t.sessions.GetOrCreate(s.Key)
			if data.Label == label {
				sessionKey = s.Key
				break
			}
		}
		if sessionKey == "" {
			return ErrorResult(fmt.Sprintf("no session found with label: %s", label))
		}
	}

	// Security: validate target session belongs to same agent
	if agentID != "" && !strings.HasPrefix(sessionKey, "agent:"+agentID+":") {
		return ErrorResult("access denied: target session belongs to a different agent")
	}

	// Publish as an inbound message (same mechanism as channels)
	t.msgBus.PublishInbound(bus.InboundMessage{
		Channel:  "system",
		SenderID: "session_send_tool",
		ChatID:   sessionKey,
		Content:  message,
		PeerKind: "direct",
	})

	return SilentResult(fmt.Sprintf(`{"status":"accepted","session_key":"%s"}`, sessionKey))
}

// ============================================================
// helpers
// ============================================================

func resolveAgentIDString(ctx context.Context) string {
	id := store.AgentIDFromContext(ctx)
	if id.String() == "00000000-0000-0000-0000-000000000000" {
		return "" // standalone mode
	}
	return id.String()
}
