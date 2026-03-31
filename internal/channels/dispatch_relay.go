package channels

import (
	"log/slog"
	"strings"

	"github.com/nextlevelbuilder/goclaw/internal/bus"
)

// relayCrossBotMentions scans an outbound message for @mentions of other bots
// in the same platform (e.g. Telegram). When found, it publishes an InboundMessage
// to the mentioned bot's agent so it can respond in the same group chat.
//
// This works around Telegram's limitation where bots cannot see messages from
// other bots — since GoClaw controls all bots, it relays internally.
func (m *Manager) relayCrossBotMentions(msg bus.OutboundMessage) {
	if msg.Content == "" {
		return
	}

	// Only relay for group chats (negative chat IDs in Telegram).
	// Skip DM channels to avoid unexpected cross-agent triggers.
	if !strings.HasPrefix(msg.ChatID, "-") {
		return
	}

	lowerContent := strings.ToLower(msg.Content)

	m.mu.RLock()
	defer m.mu.RUnlock()

	for name, ch := range m.channels {
		// Skip the sending channel itself.
		if name == msg.Channel {
			continue
		}

		// Only relay to channels that expose a bot username (BotIdentityChannel).
		bic, ok := ch.(BotIdentityChannel)
		if !ok {
			continue
		}

		botUsername := bic.BotUsername()
		if botUsername == "" {
			continue
		}

		// Check if this bot is @mentioned in the message content.
		if !strings.Contains(lowerContent, "@"+strings.ToLower(botUsername)) {
			continue
		}

		// Resolve the agent ID from the channel's BaseChannel.
		agentID := ch.(interface{ AgentID() string }).AgentID()
		if agentID == "" {
			continue
		}

		slog.Info("cross-bot relay: routing mention to agent",
			"from_channel", msg.Channel,
			"to_channel", name,
			"bot_username", botUsername,
			"agent_id", agentID,
			"chat_id", msg.ChatID,
		)

		// Build sender label from the sending channel's bot username.
		senderLabel := "bot"
		if srcBic, ok := m.channels[msg.Channel].(BotIdentityChannel); ok {
			senderLabel = "@" + srcBic.BotUsername()
		}

		m.bus.TryPublishInbound(bus.InboundMessage{
			Channel:  name,
			SenderID: "relay:" + msg.Channel,
			ChatID:   msg.ChatID,
			AgentID:  agentID,
			PeerKind: "group",
			Content:  "[" + senderLabel + "]\n" + msg.Content,
			Metadata: map[string]string{
				"relay_from": msg.Channel,
			},
		})
	}
}
