package channels

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/nextlevelbuilder/goclaw/internal/bus"
	"github.com/nextlevelbuilder/goclaw/pkg/protocol"
)

// HandleAgentEvent routes agent lifecycle events to streaming/reaction channels.
// Called from the bus event subscriber — must be non-blocking.
// eventType: "run.started", "chunk", "tool.call", "tool.result", "run.completed", "run.failed"
func (m *Manager) HandleAgentEvent(eventType, runID string, payload any) {
	val, ok := m.runs.Load(runID)
	if !ok {
		return
	}
	rc := val.(*RunContext)

	m.mu.RLock()
	ch, exists := m.channels[rc.ChannelName]
	m.mu.RUnlock()
	if !exists {
		return
	}

	ctx := context.Background()

	// Forward to StreamingChannel
	if sc, ok := ch.(StreamingChannel); ok {
		switch eventType {
		case protocol.AgentEventRunStarted:
			if err := sc.OnStreamStart(ctx, rc.ChatID); err != nil {
				slog.Debug("stream start failed", "channel", rc.ChannelName, "error", err)
			}
		case protocol.AgentEventToolCall:
			// Agent is executing a tool — mark tool phase so the next chunk
			// (new LLM iteration) resets the stream buffer.
			// Also clear the current DraftStream so the next iteration starts
			// a fresh streaming message (matching TS onAssistantMessageStart pattern).
			rc.mu.Lock()
			rc.inToolPhase = true
			rc.mu.Unlock()
			if err := sc.OnStreamEnd(ctx, rc.ChatID, ""); err != nil {
				slog.Debug("stream tool-phase end failed", "channel", rc.ChannelName, "error", err)
			}

			// Show tool status in streaming preview (edit placeholder with tool name).
			toolName := extractPayloadString(payload, "name")
			if toolName != "" && rc.ToolStatusEnabled {
				statusText := formatToolStatus(toolName)
				outMeta := copyRoutingMeta(rc.Metadata)
				outMeta["placeholder_update"] = "true"
				m.bus.PublishOutbound(bus.OutboundMessage{
					Channel:  rc.ChannelName,
					ChatID:   rc.ChatID,
					Content:  statusText,
					Metadata: outMeta,
				})
			}
		case protocol.ChatEventChunk:
			// Accumulate chunk deltas into full text.
			// When entering a new LLM iteration (first chunk after tool.call),
			// reset the buffer so we don't concatenate text from previous iterations.
			content := extractPayloadString(payload, "content")
			if content != "" {
				rc.mu.Lock()
				if rc.inToolPhase {
					// New LLM iteration — reset buffer and start fresh stream
					rc.streamBuffer = ""
					rc.inToolPhase = false
					rc.mu.Unlock()
					// Create new DraftStream for this iteration
					if err := sc.OnStreamStart(ctx, rc.ChatID); err != nil {
						slog.Debug("stream restart failed", "channel", rc.ChannelName, "error", err)
					}
					rc.mu.Lock()
				}
				rc.streamBuffer += content
				fullText := rc.streamBuffer
				rc.mu.Unlock()
				if err := sc.OnChunkEvent(ctx, rc.ChatID, fullText); err != nil {
					slog.Debug("stream chunk failed", "channel", rc.ChannelName, "error", err)
				}
			}
		case protocol.AgentEventRunCompleted:
			rc.mu.Lock()
			finalText := rc.streamBuffer
			rc.mu.Unlock()
			if err := sc.OnStreamEnd(ctx, rc.ChatID, finalText); err != nil {
				slog.Debug("stream end failed", "channel", rc.ChannelName, "error", err)
			}
		case protocol.AgentEventRunFailed:
			// Clean up streaming state
			_ = sc.OnStreamEnd(ctx, rc.ChatID, "")
		}
	}

	// Handle block.reply: deliver intermediate assistant text to non-streaming channels.
	// Gated by BlockReplyEnabled (resolved from gateway + per-channel config at RegisterRun time).
	// Streaming channels already deliver via chunks, so skip to avoid double-delivery.
	if eventType == protocol.AgentEventBlockReply {
		if !rc.BlockReplyEnabled {
			return
		}
		content := extractPayloadString(payload, "content")
		if content == "" {
			return
		}
		rc.mu.Lock()
		streaming := rc.Streaming
		rc.mu.Unlock()

		if streaming {
			return // streaming already delivered via chunks
		}

		// Build outbound metadata: copy routing fields but strip reply_to_message_id
		// (block replies are standalone) and placeholder_key (reserve for final message).
		var outMeta map[string]string
		if rc.Metadata != nil {
			outMeta = make(map[string]string)
			for _, k := range []string{"message_thread_id", "local_key", "group_id"} {
				if v := rc.Metadata[k]; v != "" {
					outMeta[k] = v
				}
			}
			if len(outMeta) == 0 {
				outMeta = nil
			}
		}

		m.bus.PublishOutbound(bus.OutboundMessage{
			Channel:  rc.ChannelName,
			ChatID:   rc.ChatID,
			Content:  content,
			Metadata: outMeta,
		})
		return
	}

	// Handle LLM retry: update placeholder to notify user
	if eventType == protocol.AgentEventRunRetrying {
		attempt := extractPayloadString(payload, "attempt")
		maxAttempts := extractPayloadString(payload, "maxAttempts")
		retryMsg := fmt.Sprintf("Provider busy, retrying... (%s/%s)", attempt, maxAttempts)
		m.bus.PublishOutbound(bus.OutboundMessage{
			Channel: rc.ChannelName,
			ChatID:  rc.ChatID,
			Content: retryMsg,
			Metadata: map[string]string{
				"placeholder_update": "true",
			},
		})
	}

	// Forward to ReactionChannel
	if reactionCh, ok := ch.(ReactionChannel); ok {
		status := ""
		switch eventType {
		case protocol.AgentEventRunStarted:
			status = "thinking"
		case protocol.AgentEventToolCall:
			// Use tool-specific reaction statuses to activate existing variants
			// (web → ⚡, coding → 👨‍💻) that are already defined in channel reaction maps.
			toolName := extractPayloadString(payload, "name")
			status = resolveToolReactionStatus(toolName)
		case protocol.AgentEventRunCompleted:
			status = "done"
		case protocol.AgentEventRunFailed:
			status = "error"
		}
		if status != "" {
			if err := reactionCh.OnReactionEvent(ctx, rc.ChatID, rc.MessageID, status); err != nil {
				slog.Debug("reaction event failed", "channel", rc.ChannelName, "status", status, "error", err)
			}
		}
	}

	// Clean up on terminal events
	if eventType == protocol.AgentEventRunCompleted || eventType == protocol.AgentEventRunFailed {
		m.runs.Delete(runID)
	}
}

// extractPayloadString extracts a string field from a payload (map[string]string or map[string]interface{}).
func extractPayloadString(payload any, key string) string {
	switch p := payload.(type) {
	case map[string]string:
		return p[key]
	case map[string]any:
		if v, ok := p[key].(string); ok {
			return v
		}
	}
	return ""
}

// copyRoutingMeta copies channel routing metadata (thread_id, local_key, group_id)
// from RunContext.Metadata into a new map suitable for outbound messages.
func copyRoutingMeta(src map[string]string) map[string]string {
	out := make(map[string]string)
	for _, k := range []string{"message_thread_id", "local_key", "group_id"} {
		if v := src[k]; v != "" {
			out[k] = v
		}
	}
	return out
}

// toolStatusMap maps builtin tool names to user-friendly status messages.
var toolStatusMap = map[string]string{
	// Filesystem
	"read_file":  "📝 Reading file...",
	"write_file": "📝 Writing file...",
	"list_files": "📝 Listing files...",
	"edit":       "📝 Editing file...",
	// Runtime
	"exec": "⚡ Running code...",
	// Web
	"web_search": "🔍 Searching the web...",
	"web_fetch":  "🔍 Fetching web content...",
	// Memory
	"memory_search":          "🧠 Searching memory...",
	"memory_get":             "🧠 Retrieving memory...",
	"knowledge_graph_search": "🧠 Querying knowledge graph...",
	// Media
	"read_image":    "👁 Analyzing image...",
	"read_document": "📄 Reading document...",
	"read_audio":    "🎧 Processing audio...",
	"read_video":    "🎬 Processing video...",
	"create_image":  "🎨 Creating image...",
	"create_video":  "🎬 Creating video...",
	"create_audio":  "🎵 Creating audio...",
	"tts":           "🔊 Generating speech...",
	// Browser
	"browser": "🌐 Browsing...",
	// Delegation & teams
	"spawn":        "👥 Delegating task...",
	"handoff":      "🔄 Handing off...",
	"team_tasks":   "📋 Managing team tasks...",
	"team_message": "💬 Sending team message...",
	// Sessions
	"sessions_list":    "📋 Listing sessions...",
	"session_status":   "📋 Checking session...",
	"sessions_history": "📋 Reading history...",
	"sessions_send":    "📤 Sending message...",
	// Other
	"message":         "📤 Sending message...",
	"cron":            "⏰ Managing schedule...",
	"skill_search":    "🔍 Searching skills...",
	"use_skill":       "🧩 Using skill...",
	"delegate_search": "🔍 Searching delegates...",
	"evaluate_loop":   "🔄 Evaluating...",
	"mcp_tool_search": "🔌 Searching MCP tools...",
}

// toolPrefixStatus maps tool name prefixes to status messages (fallback for dynamic tools).
var toolPrefixStatus = []struct {
	prefix string
	status string
}{
	{"mcp_", "🔌 Using external tool..."},
}

// formatToolStatus returns a user-friendly status message for a tool name.
func formatToolStatus(toolName string) string {
	if s, ok := toolStatusMap[toolName]; ok {
		return s
	}
	for _, p := range toolPrefixStatus {
		if strings.HasPrefix(toolName, p.prefix) {
			return p.status
		}
	}
	return "🔧 Running " + toolName + "..."
}

// resolveToolReactionStatus maps a tool name to a reaction status string.
// Returns tool-specific statuses ("web", "coding") that activate existing
// but previously unused reaction variants in channel implementations.
func resolveToolReactionStatus(toolName string) string {
	switch {
	case strings.HasPrefix(toolName, "web") || toolName == "browser":
		return "web"
	case toolName == "exec":
		return "coding"
	default:
		return "tool"
	}
}
