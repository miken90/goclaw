package telegram

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"strings"

	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"

	"github.com/nextlevelbuilder/goclaw/internal/bus"
)

// Error patterns for graceful handling (matching TS error constants in send.ts).
var (
	parseErrRe           = regexp.MustCompile(`(?i)can't parse entities|parse entities|find end of the entity`)
	messageNotModifiedRe = regexp.MustCompile(`(?i)message is not modified`)
)

// Send delivers an outbound message to a Telegram chat.
// Supports text-only messages and messages with media attachments.
// Reads metadata for reply-to-message and forum thread routing.
func (c *Channel) Send(ctx context.Context, msg bus.OutboundMessage) error {
	if !c.IsRunning() {
		return fmt.Errorf("telegram bot not running")
	}

	// Use localKey for sync.Map lookups (composite key with topic suffix).
	localKey := msg.ChatID
	if lk := msg.Metadata["local_key"]; lk != "" {
		localKey = lk
	}

	// Parse raw Telegram chat ID (strips :topic:N suffix).
	chatID, err := parseRawChatID(localKey)
	if err != nil {
		return fmt.Errorf("invalid chat ID: %w", err)
	}

	// Parse reply/thread IDs from metadata.
	var replyToMsgID, threadID int
	if v := msg.Metadata["reply_to_message_id"]; v != "" {
		fmt.Sscanf(v, "%d", &replyToMsgID)
	}
	if v := msg.Metadata["message_thread_id"]; v != "" {
		fmt.Sscanf(v, "%d", &threadID)
	}

	// Stop thinking animation
	if stop, ok := c.stopThinking.Load(localKey); ok {
		if cf, ok := stop.(*thinkingCancel); ok {
			cf.Cancel()
		}
		c.stopThinking.Delete(localKey)
	}

	// NO_REPLY cleanup: content is empty when agent suppresses reply (prompt injection, etc.).
	// Clean up placeholder, then return without sending any message.
	if msg.Content == "" && len(msg.Media) == 0 {
		if pID, ok := c.placeholders.Load(localKey); ok {
			c.placeholders.Delete(localKey)
			_ = c.deleteMessage(ctx, chatID, pID.(int))
		}
		return nil
	}

	// Handle media attachments if present
	if len(msg.Media) > 0 {
		// Delete placeholder since we're sending media
		if pID, ok := c.placeholders.Load(localKey); ok {
			c.placeholders.Delete(localKey)
			_ = c.deleteMessage(ctx, chatID, pID.(int))
		}
		return c.sendMediaMessage(ctx, chatID, msg, replyToMsgID, threadID)
	}

	// Text-only message
	htmlContent := markdownToTelegramHTML(msg.Content)

	// Try to edit the placeholder message (either "Thinking..." or a DraftStream message).
	// If edit succeeds, we're done. If content is too long or edit fails, delete the
	// placeholder and fall through to send new chunked messages.
	if pID, ok := c.placeholders.Load(localKey); ok {
		c.placeholders.Delete(localKey)
		if len(htmlContent) <= telegramMaxMessageLen {
			if err := c.editMessage(ctx, chatID, pID.(int), htmlContent); err == nil {
				return nil
			}
		}
		// Delete the placeholder since we'll send new message(s) instead
		_ = c.deleteMessage(ctx, chatID, pID.(int))
	}

	// Chunk long messages to respect Telegram's limit.
	// TS ref: only reply to the first chunk (src/channels/plugins/outbound/telegram.ts).
	chunks := chunkHTML(htmlContent, telegramMaxMessageLen)
	for i, chunk := range chunks {
		replyTo := 0
		if i == 0 {
			replyTo = replyToMsgID // only first chunk replies to user's message
		}
		if err := c.sendHTML(ctx, chatID, chunk, replyTo, threadID); err != nil {
			return err
		}
	}
	return nil
}

// sendMediaMessage sends a message with media attachments.
// Ref: TS src/telegram/send.ts → sendMessageTelegram with mediaUrl
func (c *Channel) sendMediaMessage(ctx context.Context, chatID int64, msg bus.OutboundMessage, replyTo, threadID int) error {
	chatIDObj := tu.ID(chatID)

	for _, media := range msg.Media {
		// Determine caption (use message content for first media, or media caption)
		caption := media.Caption
		if caption == "" && msg.Content != "" {
			caption = msg.Content
			msg.Content = "" // only use for first media
		}

		// Split caption if too long (Telegram limit: 1024 chars)
		var followUpText string
		if len(caption) > telegramCaptionMaxLen {
			followUpText = caption[telegramCaptionMaxLen:]
			caption = caption[:telegramCaptionMaxLen]
		}

		// Send based on content type
		ct := strings.ToLower(media.ContentType)
		switch {
		case strings.HasPrefix(ct, "image/"):
			if err := c.sendPhoto(ctx, chatIDObj, media.URL, caption, replyTo, threadID); err != nil {
				return err
			}
		case strings.HasPrefix(ct, "video/"):
			if err := c.sendVideo(ctx, chatIDObj, media.URL, caption, replyTo, threadID); err != nil {
				return err
			}
		case strings.HasPrefix(ct, "audio/"):
			if err := c.sendAudio(ctx, chatIDObj, media.URL, caption, replyTo, threadID); err != nil {
				return err
			}
		default:
			if err := c.sendDocument(ctx, chatIDObj, media.URL, caption, replyTo, threadID); err != nil {
				return err
			}
		}
		// Only reply to the first media item
		replyTo = 0

		// Send follow-up text if caption was split
		if followUpText != "" {
			htmlContent := markdownToTelegramHTML(followUpText)
			chunks := chunkHTML(htmlContent, telegramMaxMessageLen)
			for _, chunk := range chunks {
				if err := c.sendHTML(ctx, chatID, chunk, 0, threadID); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// sendHTML sends a single HTML message, falling back to plain text if Telegram rejects the HTML.
// replyTo and threadID are optional (0 = omit). General topic (1) is handled by resolveThreadIDForSend.
func (c *Channel) sendHTML(ctx context.Context, chatID int64, html string, replyTo, threadID int) error {
	tgMsg := tu.Message(tu.ID(chatID), html)
	tgMsg.ParseMode = telego.ModeHTML

	// TS ref: buildTelegramThreadParams() — General topic (1) must be omitted.
	if sendThreadID := resolveThreadIDForSend(threadID); sendThreadID > 0 {
		tgMsg.MessageThreadID = sendThreadID
	}
	if replyTo > 0 {
		tgMsg.ReplyParameters = &telego.ReplyParameters{MessageID: replyTo}
	}

	if _, err := c.bot.SendMessage(ctx, tgMsg); err != nil {
		if parseErrRe.MatchString(err.Error()) {
			slog.Warn("HTML parse failed, falling back to plain text", "error", err)
			tgMsg.ParseMode = ""
			_, err = c.bot.SendMessage(ctx, tgMsg)
			return err
		}
		return err
	}
	return nil
}

// sendPhoto sends a photo message.
func (c *Channel) sendPhoto(ctx context.Context, chatID telego.ChatID, filePath, caption string, replyTo, threadID int) error {
	file, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("open photo %s: %w", filePath, err)
	}
	defer file.Close()

	params := &telego.SendPhotoParams{
		ChatID:  chatID,
		Photo:   telego.InputFile{File: file},
		Caption: caption,
	}
	if caption != "" {
		params.ParseMode = telego.ModeHTML
	}
	if sendThreadID := resolveThreadIDForSend(threadID); sendThreadID > 0 {
		params.MessageThreadID = sendThreadID
	}
	if replyTo > 0 {
		params.ReplyParameters = &telego.ReplyParameters{MessageID: replyTo}
	}

	_, err = c.bot.SendPhoto(ctx, params)
	return err
}

// sendVideo sends a video message.
func (c *Channel) sendVideo(ctx context.Context, chatID telego.ChatID, filePath, caption string, replyTo, threadID int) error {
	file, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("open video %s: %w", filePath, err)
	}
	defer file.Close()

	params := &telego.SendVideoParams{
		ChatID:  chatID,
		Video:   telego.InputFile{File: file},
		Caption: caption,
	}
	if caption != "" {
		params.ParseMode = telego.ModeHTML
	}
	if sendThreadID := resolveThreadIDForSend(threadID); sendThreadID > 0 {
		params.MessageThreadID = sendThreadID
	}
	if replyTo > 0 {
		params.ReplyParameters = &telego.ReplyParameters{MessageID: replyTo}
	}

	_, err = c.bot.SendVideo(ctx, params)
	return err
}

// sendAudio sends an audio message.
func (c *Channel) sendAudio(ctx context.Context, chatID telego.ChatID, filePath, caption string, replyTo, threadID int) error {
	file, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("open audio %s: %w", filePath, err)
	}
	defer file.Close()

	params := &telego.SendAudioParams{
		ChatID:  chatID,
		Audio:   telego.InputFile{File: file},
		Caption: caption,
	}
	if caption != "" {
		params.ParseMode = telego.ModeHTML
	}
	if sendThreadID := resolveThreadIDForSend(threadID); sendThreadID > 0 {
		params.MessageThreadID = sendThreadID
	}
	if replyTo > 0 {
		params.ReplyParameters = &telego.ReplyParameters{MessageID: replyTo}
	}

	_, err = c.bot.SendAudio(ctx, params)
	return err
}

// sendDocument sends a document/file message.
func (c *Channel) sendDocument(ctx context.Context, chatID telego.ChatID, filePath, caption string, replyTo, threadID int) error {
	file, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("open document %s: %w", filePath, err)
	}
	defer file.Close()

	params := &telego.SendDocumentParams{
		ChatID:   chatID,
		Document: telego.InputFile{File: file},
		Caption:  caption,
	}
	if caption != "" {
		params.ParseMode = telego.ModeHTML
	}
	if sendThreadID := resolveThreadIDForSend(threadID); sendThreadID > 0 {
		params.MessageThreadID = sendThreadID
	}
	if replyTo > 0 {
		params.ReplyParameters = &telego.ReplyParameters{MessageID: replyTo}
	}

	_, err = c.bot.SendDocument(ctx, params)
	return err
}

// editMessage edits an existing message's text.
func (c *Channel) editMessage(ctx context.Context, chatID int64, messageID int, htmlText string) error {
	editMsg := tu.EditMessageText(tu.ID(chatID), messageID, htmlText)
	editMsg.ParseMode = telego.ModeHTML

	_, err := c.bot.EditMessageText(ctx, editMsg)
	if err != nil {
		// Ignore "message is not modified" errors (idempotent edit)
		if messageNotModifiedRe.MatchString(err.Error()) {
			return nil
		}
		return err
	}
	return nil
}

// deleteMessage deletes a message from the chat.
func (c *Channel) deleteMessage(ctx context.Context, chatID int64, messageID int) error {
	return c.bot.DeleteMessage(ctx, &telego.DeleteMessageParams{
		ChatID:    tu.ID(chatID),
		MessageID: messageID,
	})
}
