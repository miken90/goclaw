package channels

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/nextlevelbuilder/goclaw/internal/providers"
	"github.com/nextlevelbuilder/goclaw/internal/store"
)

// CompactionConfig configures LLM-based history compaction.
type CompactionConfig struct {
	Threshold  int                // trigger compaction when entries exceed this (default 50)
	KeepRecent int                // keep this many recent raw messages (default 15)
	Provider   providers.Provider // LLM provider for summarization
	Model      string             // model to use for summarization
}

// MaybeCompact checks if compaction is needed for a history key and triggers it in background.
// Called from Record() after appending. Thread-safe via sync.Map compaction guard.
func (ph *PendingHistory) MaybeCompact(historyKey string, currentCount int, cfg *CompactionConfig) {
	if ph.store == nil || cfg == nil || cfg.Provider == nil {
		return
	}
	threshold := cfg.Threshold
	if threshold <= 0 {
		threshold = DefaultGroupHistoryLimit
	}
	if currentCount <= threshold {
		return
	}
	// Guard: only one compaction per key at a time
	if _, loaded := ph.compacting.LoadOrStore(historyKey, true); loaded {
		return
	}
	go ph.runCompaction(historyKey, cfg)
}

// runCompaction performs LLM-based summarization of old messages.
// Follows pattern from internal/agent/loop_compact.go.
func (ph *PendingHistory) runCompaction(historyKey string, cfg *CompactionConfig) {
	defer ph.compacting.Delete(historyKey)

	// Step 1: Force-flush buffer to ensure DB is consistent
	ph.flushNow()

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	// Step 2: Read entries from DB
	entries, err := ph.store.ListByKey(ctx, ph.channelName, historyKey)
	if err != nil {
		slog.Warn("compaction.list_failed", "channel", ph.channelName, "key", historyKey, "error", err)
		return
	}

	threshold := cfg.Threshold
	if threshold <= 0 {
		threshold = DefaultGroupHistoryLimit
	}
	if len(entries) <= threshold {
		return // cleared or below threshold
	}

	// Step 3: Split entries
	keepRecent := cfg.KeepRecent
	if keepRecent <= 0 {
		keepRecent = 15
	}
	if keepRecent >= len(entries) {
		return
	}
	splitIdx := len(entries) - keepRecent

	toSummarize := entries[:splitIdx]
	deleteIDs := make([]uuid.UUID, len(toSummarize))
	for i, e := range toSummarize {
		deleteIDs[i] = e.ID
	}

	// Step 4: Build text and call LLM
	var sb strings.Builder
	for _, e := range toSummarize {
		prefix := e.Sender
		if e.IsSummary {
			prefix = "[previous summary]"
		}
		ts := ""
		if !e.CreatedAt.IsZero() {
			ts = fmt.Sprintf(" [%s]", e.CreatedAt.Format("15:04"))
		}
		fmt.Fprintf(&sb, "%s%s: %s\n", prefix, ts, e.Body)
	}

	resp, err := cfg.Provider.Chat(ctx, providers.ChatRequest{
		Messages: []providers.Message{{
			Role:    "user",
			Content: "Summarize these group chat messages concisely, preserving key topics, decisions, names, and important context:\n\n" + sb.String(),
		}},
		Model:   cfg.Model,
		Options: map[string]any{"max_tokens": 512, "temperature": 0.3},
	})
	if err != nil {
		slog.Warn("compaction.llm_failed", "channel", ph.channelName, "key", historyKey, "error", err)
		return
	}

	// Step 5: Compact in DB (atomic tx: delete old + insert summary)
	summary := &store.PendingMessage{
		ChannelName: ph.channelName,
		HistoryKey:  historyKey,
		Sender:      "[summary]",
		Body:        resp.Content,
		IsSummary:   true,
	}
	if err := ph.store.Compact(ctx, deleteIDs, summary); err != nil {
		slog.Warn("compaction.db_failed", "channel", ph.channelName, "key", historyKey, "error", err)
		return
	}

	// Step 6: Update RAM from DB
	ph.mu.Lock()
	if _, exists := ph.entries[historyKey]; !exists {
		// Key was Clear()ed during compaction — remove stale summary
		ph.mu.Unlock()
		_ = ph.store.DeleteByKey(ctx, ph.channelName, historyKey)
		slog.Info("compaction.cleared_stale", "channel", ph.channelName, "key", historyKey)
		return
	}
	// Re-read from DB to get complete current state (summary + kept + new entries)
	fresh, err := ph.store.ListByKey(ctx, ph.channelName, historyKey)
	if err == nil {
		rebuilt := make([]HistoryEntry, 0, len(fresh))
		for _, f := range fresh {
			rebuilt = append(rebuilt, HistoryEntry{
				Sender:    f.Sender,
				Body:      f.Body,
				Timestamp: f.CreatedAt,
				MessageID: f.PlatformMsgID,
			})
		}
		ph.entries[historyKey] = rebuilt
	}
	ph.mu.Unlock()

	slog.Info("compaction.done",
		"channel", ph.channelName,
		"key", historyKey,
		"summarized", len(toSummarize),
		"kept", keepRecent,
		"total_after", len(fresh),
	)
}
