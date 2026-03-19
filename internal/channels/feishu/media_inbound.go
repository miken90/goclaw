package feishu

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nextlevelbuilder/goclaw/internal/channels/media"
)

// resolveMediaFromMessage extracts and downloads media from a Feishu message.
// Returns a list of MediaInfo for each media item found.
func (c *Channel) resolveMediaFromMessage(ctx context.Context, messageID, messageType, rawContent string) []media.MediaInfo {
	maxBytes := int64(c.cfg.MediaMaxMB) * 1024 * 1024
	if maxBytes <= 0 {
		maxBytes = int64(defaultMediaMaxMB) * 1024 * 1024
	}

	var results []media.MediaInfo

	switch messageType {
	case "image":
		imageKey := extractJSONField(rawContent, "image_key")
		if imageKey == "" {
			return nil
		}
		data, _, err := c.downloadMessageResource(ctx, messageID, imageKey, "image")
		if err != nil {
			slog.Debug("feishu download image failed", "message_id", messageID, "error", err)
			return nil
		}
		if int64(len(data)) > maxBytes {
			slog.Debug("feishu image too large", "size", len(data), "max", maxBytes)
			return nil
		}
		path, err := saveMediaToTemp(data, "img", ".png")
		if err != nil {
			slog.Debug("feishu save image failed", "error", err)
			return nil
		}
		results = append(results, media.MediaInfo{
			Type: media.TypeImage, FilePath: path, ContentType: "image/png",
		})

	case "file":
		fileKey := extractJSONField(rawContent, "file_key")
		if fileKey == "" {
			return nil
		}
		data, fileName, err := c.downloadMessageResource(ctx, messageID, fileKey, "file")
		if err != nil {
			slog.Debug("feishu download file failed", "message_id", messageID, "error", err)
			return nil
		}
		if int64(len(data)) > maxBytes {
			slog.Debug("feishu file too large", "size", len(data), "max", maxBytes)
			return nil
		}
		ext := filepath.Ext(fileName)
		if ext == "" {
			ext = ".bin"
		}
		path, err := saveMediaToTemp(data, "file", ext)
		if err != nil {
			slog.Debug("feishu save file failed", "error", err)
			return nil
		}
		results = append(results, media.MediaInfo{
			Type: media.TypeDocument, FilePath: path,
			ContentType: media.DetectMIMEType(fileName), FileName: fileName,
		})

	case "audio":
		fileKey := extractJSONField(rawContent, "file_key")
		if fileKey == "" {
			return nil
		}
		data, _, err := c.downloadMessageResource(ctx, messageID, fileKey, "file")
		if err != nil {
			slog.Debug("feishu download audio failed", "error", err)
			return nil
		}
		if int64(len(data)) > maxBytes {
			return nil
		}
		path, err := saveMediaToTemp(data, "audio", ".opus")
		if err != nil {
			return nil
		}
		results = append(results, media.MediaInfo{
			Type: media.TypeAudio, FilePath: path, ContentType: "audio/ogg",
		})

	case "video":
		fileKey := extractJSONField(rawContent, "file_key")
		if fileKey == "" {
			return nil
		}
		data, _, err := c.downloadMessageResource(ctx, messageID, fileKey, "file")
		if err != nil {
			slog.Debug("feishu download video failed", "error", err)
			return nil
		}
		if int64(len(data)) > maxBytes {
			return nil
		}
		path, err := saveMediaToTemp(data, "video", ".mp4")
		if err != nil {
			return nil
		}
		results = append(results, media.MediaInfo{
			Type: media.TypeVideo, FilePath: path, ContentType: "video/mp4",
		})

	case "sticker":
		fileKey := extractJSONField(rawContent, "file_key")
		if fileKey == "" {
			return nil
		}
		data, _, err := c.downloadMessageResource(ctx, messageID, fileKey, "image")
		if err != nil {
			return nil
		}
		if int64(len(data)) > maxBytes {
			return nil
		}
		path, err := saveMediaToTemp(data, "sticker", ".png")
		if err != nil {
			return nil
		}
		results = append(results, media.MediaInfo{
			Type: media.TypeImage, FilePath: path, ContentType: "image/png",
		})
	}

	return results
}

// saveMediaToTemp writes media bytes to a temp file and returns the path.
func saveMediaToTemp(data []byte, prefix, ext string) (string, error) {
	if ext == "" {
		ext = ".bin"
	}
	fileName := fmt.Sprintf("feishu_%s_%d%s", prefix, time.Now().UnixMilli(), ext)
	path := filepath.Join(os.TempDir(), fileName)
	if err := os.WriteFile(path, data, 0644); err != nil {
		return "", err
	}
	return path, nil
}

// extractJSONField is a simple helper to extract a string field from JSON content.
// Used for parsing media keys from message content without full struct parsing.
func extractJSONField(jsonStr, field string) string {
	key := `"` + field + `":"`
	idx := strings.Index(jsonStr, key)
	if idx < 0 {
		return ""
	}
	start := idx + len(key)
	end := strings.Index(jsonStr[start:], `"`)
	if end < 0 {
		return ""
	}
	return jsonStr[start : start+end]
}
