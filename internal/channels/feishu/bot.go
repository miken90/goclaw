package feishu

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/nextlevelbuilder/goclaw/internal/channels"
)

// messageContext holds parsed information from a Feishu message event.
type messageContext struct {
	ChatID      string
	MessageID   string
	SenderID    string // sender_id.open_id
	ChatType    string // "p2p" or "group"
	Content     string
	ContentType string // "text", "post", "image", etc.
	MentionedBot bool
	RootID      string // thread root message ID
	ParentID    string // parent message ID
	Mentions    []mentionInfo
}

type mentionInfo struct {
	Key    string // @_user_N placeholder
	OpenID string
	Name   string
}

// handleMessageEvent processes an incoming Feishu message event.
func (c *Channel) handleMessageEvent(ctx context.Context, event *MessageEvent) {
	if event == nil {
		return
	}

	msg := &event.Event.Message
	sender := &event.Event.Sender

	messageID := msg.MessageID
	if messageID == "" {
		return
	}

	// 1. Dedup check
	if c.isDuplicate(messageID) {
		slog.Debug("feishu message deduplicated", "message_id", messageID)
		return
	}

	// 2. Parse message
	mc := c.parseMessageEvent(event)
	if mc == nil {
		return
	}

	// 3. Resolve sender name (cached)
	senderName := c.resolveSenderName(ctx, mc.SenderID)

	// 4. Group policy
	if mc.ChatType == "group" {
		if !c.checkGroupPolicy(mc.SenderID) {
			slog.Debug("feishu group message rejected by policy", "sender_id", mc.SenderID, "chat_id", mc.ChatID)
			return
		}

		// 5. RequireMention check — record to history if not mentioned
		requireMention := true
		if c.cfg.RequireMention != nil {
			requireMention = *c.cfg.RequireMention
		}
		if requireMention && !mc.MentionedBot {
			historyKey := mc.ChatID
			if mc.RootID != "" && c.cfg.TopicSessionMode == "enabled" {
				historyKey = fmt.Sprintf("%s:topic:%s", mc.ChatID, mc.RootID)
			}
			c.groupHistory.Record(historyKey, channels.HistoryEntry{
				Sender:    senderName,
				Body:      mc.Content,
				Timestamp: time.Now(),
				MessageID: messageID,
			}, c.historyLimit)

			slog.Debug("feishu group message recorded (no mention)",
				"chat_id", mc.ChatID, "sender", senderName,
			)
			return
		}
	}

	// 6. DM policy (pairing flow)
	if mc.ChatType == "p2p" {
		if !c.checkDMPolicy(mc.SenderID, mc.ChatID) {
			return
		}
	}

	// 7. Build content (strip bot mention from text)
	content := mc.Content
	if content == "" {
		content = "[empty message]"
	}

	// 8. Topic session
	chatID := mc.ChatID
	if mc.RootID != "" && c.cfg.TopicSessionMode == "enabled" {
		chatID = fmt.Sprintf("%s:topic:%s", mc.ChatID, mc.RootID)
	}

	slog.Debug("feishu message received",
		"sender_id", mc.SenderID,
		"sender_name", senderName,
		"chat_id", chatID,
		"chat_type", mc.ChatType,
		"mentioned_bot", mc.MentionedBot,
		"preview", channels.Truncate(content, 50),
	)

	// 9. Build metadata
	peerKind := "direct"
	if mc.ChatType == "group" {
		peerKind = "group"
	}

	metadata := map[string]string{
		"message_id":    messageID,
		"chat_type":     mc.ChatType,
		"sender_name":   senderName,
		"mentioned_bot": fmt.Sprintf("%t", mc.MentionedBot),
		"platform":      "feishu",
	}

	if sender != nil {
		metadata["sender_open_id"] = sender.SenderID.OpenID
	}

	// Build final content with group context (pending history + sender annotation).
	if mc.ChatType == "group" && senderName != "" {
		annotated := fmt.Sprintf("[From: %s]\n%s", senderName, content)
		if c.historyLimit > 0 {
			content = c.groupHistory.BuildContext(chatID, annotated, c.historyLimit)
		} else {
			content = annotated
		}
	}

	// 10. Publish to bus
	c.HandleMessage(mc.SenderID, chatID, content, nil, metadata, peerKind)

	// Clear pending history after sending to agent.
	if mc.ChatType == "group" {
		c.groupHistory.Clear(chatID)
	}
}

// --- Parse ---

func (c *Channel) parseMessageEvent(event *MessageEvent) *messageContext {
	msg := &event.Event.Message
	sender := &event.Event.Sender

	chatID := msg.ChatID
	messageID := msg.MessageID
	chatType := msg.ChatType
	contentType := msg.MessageType
	rootID := msg.RootID
	parentID := msg.ParentID

	senderID := ""
	if sender != nil {
		senderID = sender.SenderID.OpenID
	}

	// Parse content
	content := parseMessageContent(msg.Content, contentType)

	// Parse mentions
	var mentions []mentionInfo
	mentionedBot := false
	for _, m := range msg.Mentions {
		mi := mentionInfo{
			Key:    m.Key,
			OpenID: m.ID.OpenID,
			Name:   m.Name,
		}
		mentions = append(mentions, mi)

		// Check if bot is mentioned
		if c.botOpenID != "" && mi.OpenID == c.botOpenID {
			mentionedBot = true
		}
	}

	// Strip bot mention from content
	if mentionedBot && c.botOpenID != "" {
		content = stripBotMention(content, mentions, c.botOpenID)
	}

	return &messageContext{
		ChatID:       chatID,
		MessageID:    messageID,
		SenderID:     senderID,
		ChatType:     chatType,
		Content:      content,
		ContentType:  contentType,
		MentionedBot: mentionedBot,
		RootID:       rootID,
		ParentID:     parentID,
		Mentions:     mentions,
	}
}

// --- Content parsing ---

func parseMessageContent(rawContent, messageType string) string {
	if rawContent == "" {
		return ""
	}

	switch messageType {
	case "text":
		var textMsg struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal([]byte(rawContent), &textMsg); err == nil {
			return textMsg.Text
		}
		return rawContent

	case "post":
		return parsePostContent(rawContent)

	case "image":
		return "[image]"

	case "file":
		var fileMsg struct {
			FileName string `json:"file_name"`
		}
		if err := json.Unmarshal([]byte(rawContent), &fileMsg); err == nil {
			return fmt.Sprintf("[file: %s]", fileMsg.FileName)
		}
		return "[file]"

	default:
		return fmt.Sprintf("[%s message]", messageType)
	}
}

func parsePostContent(rawContent string) string {
	var post map[string]interface{}
	if err := json.Unmarshal([]byte(rawContent), &post); err != nil {
		return rawContent
	}

	var langContent interface{}
	for _, lang := range []string{"zh_cn", "en_us"} {
		if lc, ok := post[lang]; ok {
			langContent = lc
			break
		}
	}
	if langContent == nil {
		for _, v := range post {
			langContent = v
			break
		}
	}
	if langContent == nil {
		return rawContent
	}

	langMap, ok := langContent.(map[string]interface{})
	if !ok {
		return rawContent
	}

	contentArr, ok := langMap["content"].([]interface{})
	if !ok {
		return rawContent
	}

	var textParts []string
	for _, para := range contentArr {
		paraArr, ok := para.([]interface{})
		if !ok {
			continue
		}
		var lineParts []string
		for _, elem := range paraArr {
			elemMap, ok := elem.(map[string]interface{})
			if !ok {
				continue
			}
			tag, _ := elemMap["tag"].(string)
			switch tag {
			case "text":
				if t, ok := elemMap["text"].(string); ok {
					lineParts = append(lineParts, t)
				}
			case "md":
				if t, ok := elemMap["text"].(string); ok {
					lineParts = append(lineParts, t)
				}
			case "at":
				if name, ok := elemMap["user_name"].(string); ok {
					lineParts = append(lineParts, "@"+name)
				}
			case "a":
				if href, ok := elemMap["href"].(string); ok {
					text, _ := elemMap["text"].(string)
					if text != "" {
						lineParts = append(lineParts, fmt.Sprintf("[%s](%s)", text, href))
					} else {
						lineParts = append(lineParts, href)
					}
				}
			case "img":
				lineParts = append(lineParts, "[image]")
			}
		}
		if len(lineParts) > 0 {
			textParts = append(textParts, strings.Join(lineParts, ""))
		}
	}

	return strings.Join(textParts, "\n")
}

func stripBotMention(text string, mentions []mentionInfo, botOpenID string) string {
	for _, m := range mentions {
		if m.OpenID == botOpenID && m.Key != "" {
			text = strings.ReplaceAll(text, m.Key, "")
		}
	}
	return strings.TrimSpace(text)
}

// --- Sender name resolution ---

func (c *Channel) resolveSenderName(ctx context.Context, openID string) string {
	if openID == "" {
		return ""
	}

	// Check cache
	if entry, ok := c.senderCache.Load(openID); ok {
		e := entry.(*senderCacheEntry)
		if time.Now().Before(e.expiresAt) {
			return e.name
		}
		c.senderCache.Delete(openID)
	}

	// Fetch from API
	name := c.fetchSenderName(ctx, openID)
	if name != "" {
		c.senderCache.Store(openID, &senderCacheEntry{
			name:      name,
			expiresAt: time.Now().Add(senderCacheTTL),
		})
	}
	return name
}

func (c *Channel) fetchSenderName(ctx context.Context, openID string) string {
	name, err := c.client.GetUser(ctx, openID, "open_id")
	if err != nil {
		slog.Debug("feishu fetch sender name failed", "open_id", openID, "error", err)
		return ""
	}
	return name
}

// --- Policy checks ---

func (c *Channel) checkGroupPolicy(senderID string) bool {
	groupPolicy := c.cfg.GroupPolicy
	if groupPolicy == "" {
		groupPolicy = "open"
	}

	switch groupPolicy {
	case "disabled":
		return false
	case "allowlist":
		if c.IsAllowed(senderID) {
			return true
		}
		for _, allowed := range c.groupAllowList {
			if senderID == allowed || strings.TrimPrefix(allowed, "@") == senderID {
				return true
			}
		}
		return false
	default: // "open"
		return true
	}
}

func (c *Channel) checkDMPolicy(senderID, chatID string) bool {
	dmPolicy := c.cfg.DMPolicy
	if dmPolicy == "" {
		dmPolicy = "pairing"
	}

	switch dmPolicy {
	case "disabled":
		slog.Debug("feishu DM rejected: disabled", "sender_id", senderID)
		return false
	case "open":
		return true
	case "allowlist":
		if !c.IsAllowed(senderID) {
			slog.Debug("feishu DM rejected by allowlist", "sender_id", senderID)
			return false
		}
		return true
	default: // "pairing"
		paired := false
		if c.pairingService != nil {
			paired = c.pairingService.IsPaired(senderID, c.Name())
		}
		inAllowList := c.HasAllowList() && c.IsAllowed(senderID)

		if paired || inAllowList {
			return true
		}

		c.sendPairingReply(senderID, chatID)
		return false
	}
}

func (c *Channel) sendPairingReply(senderID, chatID string) {
	if c.pairingService == nil {
		return
	}

	// Debounce
	if lastSent, ok := c.pairingDebounce.Load(senderID); ok {
		if time.Since(lastSent.(time.Time)) < pairingDebounceTime {
			return
		}
	}

	code, err := c.pairingService.RequestPairing(senderID, c.Name(), chatID, "default")
	if err != nil {
		slog.Debug("feishu pairing request failed", "sender_id", senderID, "error", err)
		return
	}

	replyText := fmt.Sprintf(
		"GoClaw: access not configured.\n\nYour Feishu open_id: %s\n\nPairing code: %s\n\nAsk the bot owner to approve with:\n  goclaw pairing approve %s",
		senderID, code, code,
	)

	receiveIDType := resolveReceiveIDType(chatID)
	if err := c.sendText(context.Background(), chatID, receiveIDType, replyText); err != nil {
		slog.Warn("failed to send feishu pairing reply", "error", err)
	} else {
		c.pairingDebounce.Store(senderID, time.Now())
		slog.Info("feishu pairing reply sent", "sender_id", senderID, "code", code)
	}
}

// --- Helpers ---

func safeStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
