package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/nextlevelbuilder/goclaw/internal/providers"
	"github.com/nextlevelbuilder/goclaw/internal/store"
	"github.com/nextlevelbuilder/goclaw/internal/tracing"
)

func (l *Loop) emit(event AgentEvent) {
	if l.onEvent != nil {
		l.onEvent(event)
	}
}

// ID returns the agent's identifier.
func (l *Loop) ID() string { return l.id }

// Model returns the model identifier for this agent loop.
func (l *Loop) Model() string { return l.model }

// IsRunning returns whether the agent is currently processing.
func (l *Loop) IsRunning() bool { return l.activeRuns.Load() > 0 }

// emitLLMSpan records an LLM call span if tracing is active.
// When GOCLAW_TRACE_VERBOSE is set, messages are serialized as InputPreview.
func (l *Loop) emitLLMSpan(ctx context.Context, start time.Time, iteration int, messages []providers.Message, resp *providers.ChatResponse, callErr error) {
	traceID := tracing.TraceIDFromContext(ctx)
	collector := tracing.CollectorFromContext(ctx)
	if collector == nil || traceID == uuid.Nil {
		return
	}

	now := time.Now().UTC()
	dur := int(now.Sub(start).Milliseconds())
	span := store.SpanData{
		TraceID:    traceID,
		SpanType:   "llm_call",
		Name:       fmt.Sprintf("%s/%s #%d", l.provider.Name(), l.model, iteration),
		StartTime:  start,
		EndTime:    &now,
		DurationMS: dur,
		Model:      l.model,
		Provider:   l.provider.Name(),
		Status:     "completed",
		Level:      "DEFAULT",
		CreatedAt:  now,
	}
	if parentID := tracing.ParentSpanIDFromContext(ctx); parentID != uuid.Nil {
		span.ParentSpanID = &parentID
	}
	if l.agentUUID != uuid.Nil {
		span.AgentID = &l.agentUUID
	}

	// Verbose mode: serialize full messages as InputPreview
	if collector.Verbose() && len(messages) > 0 {
		if b, err := json.Marshal(messages); err == nil {
			span.InputPreview = truncateStr(string(b), 50000)
		}
	}

	if callErr != nil {
		span.Status = "error"
		span.Error = callErr.Error()
	} else if resp != nil {
		if resp.Usage != nil {
			span.InputTokens = resp.Usage.PromptTokens
			span.OutputTokens = resp.Usage.CompletionTokens
		}
		span.FinishReason = resp.FinishReason
		span.OutputPreview = truncateStr(resp.Content, 500)
	}

	collector.EmitSpan(span)
}

// emitToolSpan records a tool call span if tracing is active.
func (l *Loop) emitToolSpan(ctx context.Context, start time.Time, toolName, toolCallID, input, output string, isError bool) {
	traceID := tracing.TraceIDFromContext(ctx)
	collector := tracing.CollectorFromContext(ctx)
	if collector == nil || traceID == uuid.Nil {
		return
	}

	now := time.Now().UTC()
	dur := int(now.Sub(start).Milliseconds())
	span := store.SpanData{
		TraceID:       traceID,
		SpanType:      "tool_call",
		Name:          toolName,
		StartTime:     start,
		EndTime:       &now,
		DurationMS:    dur,
		ToolName:      toolName,
		ToolCallID:    toolCallID,
		InputPreview:  truncateStr(input, 500),
		OutputPreview: truncateStr(output, 500),
		Status:        "completed",
		Level:         "DEFAULT",
		CreatedAt:     now,
	}
	if parentID := tracing.ParentSpanIDFromContext(ctx); parentID != uuid.Nil {
		span.ParentSpanID = &parentID
	}
	if l.agentUUID != uuid.Nil {
		span.AgentID = &l.agentUUID
	}
	if isError {
		span.Status = "error"
		span.Error = truncateStr(output, 200)
	}

	collector.EmitSpan(span)
}

// emitAgentSpan records the root "agent" span that parents all LLM/tool spans in this request.
func (l *Loop) emitAgentSpan(ctx context.Context, start time.Time, result *RunResult, runErr error) {
	traceID := tracing.TraceIDFromContext(ctx)
	collector := tracing.CollectorFromContext(ctx)
	if collector == nil || traceID == uuid.Nil {
		return
	}

	agentSpanID := tracing.ParentSpanIDFromContext(ctx)
	if agentSpanID == uuid.Nil {
		return
	}

	now := time.Now().UTC()
	dur := int(now.Sub(start).Milliseconds())
	spanName := l.id
	span := store.SpanData{
		ID:         agentSpanID,
		TraceID:    traceID,
		SpanType:   "agent",
		Name:       spanName,
		StartTime:  start,
		EndTime:    &now,
		DurationMS: dur,
		Model:      l.model,
		Provider:   l.provider.Name(),
		Status:     "completed",
		Level:      "DEFAULT",
		CreatedAt:  now,
	}
	// Nest under parent root span if this is an announce run
	if announceParent := tracing.AnnounceParentSpanIDFromContext(ctx); announceParent != uuid.Nil {
		span.ParentSpanID = &announceParent
		span.Name = "announce:" + spanName
	}
	if l.agentUUID != uuid.Nil {
		span.AgentID = &l.agentUUID
	}
	if runErr != nil {
		span.Status = "error"
		span.Error = runErr.Error()
	} else if result != nil {
		span.OutputPreview = truncateStr(result.Content, 500)
		// Note: token counts are NOT set on agent spans to avoid double-counting
		// with child llm_call spans. Trace aggregation sums only llm_call spans.
	}

	collector.EmitSpan(span)
}

func truncateStr(s string, maxLen int) string {
	s = strings.ToValidUTF8(s, "")
	if len(s) <= maxLen {
		return s
	}
	// Don't cut in the middle of a multi-byte rune
	for maxLen > 0 && !utf8.RuneStart(s[maxLen]) {
		maxLen--
	}
	return s[:maxLen] + "..."
}

// EstimateTokens returns a rough token estimate for a slice of messages.
// Used internally for summarization thresholds and externally for adaptive throttle.
func EstimateTokens(messages []providers.Message) int {
	total := 0
	for _, m := range messages {
		total += utf8.RuneCountInString(m.Content) / 3
	}
	return total
}
