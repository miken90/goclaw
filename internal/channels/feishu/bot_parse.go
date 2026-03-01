package feishu

import (
	"encoding/json"
	"fmt"
	"strings"
)

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

// --- Helpers ---

func safeStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
