package http

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"github.com/nextlevelbuilder/goclaw/internal/bus"
)

// Worker stream event constants.
const (
	workerEventInit       = "worker.stream.init"
	workerEventText       = "worker.stream.text"
	workerEventTool       = "worker.stream.tool"
	workerEventToolResult = "worker.stream.tool_result"
	workerEventProgress   = "worker.stream.progress"
	workerEventResult     = "worker.stream.result"
	workerEventError      = "worker.stream.error"
	workerDisconnected    = "worker.disconnected"
)

// WorkerStreamPayload is broadcast via the event bus for each worker stream event.
type WorkerStreamPayload struct {
	TeamID     string  `json:"team_id"`
	TaskID     string  `json:"task_id"`
	EventType  string  `json:"event_type"`
	Seq        int     `json:"seq"`
	Text       string  `json:"text,omitempty"`
	ToolName   string  `json:"tool_name,omitempty"`
	ToolInput  string  `json:"tool_input,omitempty"`
	CostUSD    float64 `json:"cost_usd,omitempty"`
	NumTurns   int     `json:"num_turns,omitempty"`
	DurationMs float64 `json:"duration_ms,omitempty"`
	IsError    bool    `json:"is_error,omitempty"`
	Model      string  `json:"model,omitempty"`
	SessionID  string  `json:"session_id,omitempty"`
}

// workerEvent is stored in the ring buffer for reconnection replay.
type workerEvent struct {
	Type      string          `json:"type"`
	Raw       json.RawMessage `json:"raw"`
	Timestamp time.Time       `json:"ts"`
	Seq       int             `json:"seq"`
}

// WorkerSession manages a single Claude CLI --sdk-url WebSocket connection.
// The VPS is the WS server; Claude CLI on the worker machine is the WS client.
type WorkerSession struct {
	taskID    uuid.UUID
	teamID    uuid.UUID
	tenantID  uuid.UUID
	conn      *websocket.Conn
	sessionID string // from system/init
	model     string // from system/init
	startedAt time.Time
	lastEvent time.Time
	prompt    string // task prompt to send after init

	// Channels
	injectCh chan []byte   // VPS→CLI messages (user messages, control responses)
	doneCh   chan struct{} // signals session complete

	// Event broadcasting
	eventPub bus.EventPublisher
	seq      int

	// Ring buffer for reconnection replay
	eventBuf  []workerEvent
	maxEvents int

	// Result fields captured from the result message.
	ResultSubtype string
	ResultText    string
	CostUSD       float64
	NumTurns      int
	DurationMs    float64
	ResultIsError bool

	mu      sync.Mutex
	closed  bool
	writeMu sync.Mutex // serializes WS writes
}

// NewWorkerSession creates a new session for a Claude CLI --sdk-url connection.
func NewWorkerSession(taskID, teamID, tenantID uuid.UUID, conn *websocket.Conn, prompt string, eventPub bus.EventPublisher) *WorkerSession {
	return &WorkerSession{
		taskID:    taskID,
		teamID:    teamID,
		tenantID:  tenantID,
		conn:      conn,
		startedAt: time.Now(),
		lastEvent: time.Now(),
		prompt:    prompt,
		injectCh:  make(chan []byte, 64),
		doneCh:    make(chan struct{}),
		eventPub:  eventPub,
		maxEvents: 500,
	}
}

// ResultCh returns a channel that is closed when the session receives a result message.
func (ws *WorkerSession) ResultCh() <-chan struct{} {
	return ws.doneCh
}

// sendNDJSON marshals msg to JSON, appends newline, and sends as a single WS text message.
func (ws *WorkerSession) sendNDJSON(msg any) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal ndjson: %w", err)
	}
	data = append(data, '\n')

	ws.writeMu.Lock()
	defer ws.writeMu.Unlock()
	return ws.conn.WriteMessage(websocket.TextMessage, data)
}

// sendInitialize sends the control_request{subtype:"initialize"} that triggers the CLI handshake.
// Must be called immediately after WS upgrade.
func (ws *WorkerSession) sendInitialize() error {
	msg := map[string]any{
		"type":       "control_request",
		"request_id": fmt.Sprintf("init-%d", time.Now().UnixNano()),
		"request": map[string]any{
			"subtype": "initialize",
		},
	}
	slog.Info("worker.stream.send_initialize", "task_id", ws.taskID)
	return ws.sendNDJSON(msg)
}

// readLoop reads WS messages, splits by newline (NDJSON), and dispatches each line.
func (ws *WorkerSession) readLoop() {
	defer func() {
		ws.mu.Lock()
		wasClosed := ws.closed
		ws.mu.Unlock()
		if !wasClosed {
			ws.broadcastEvent(workerDisconnected, nil)
		}
	}()

	for {
		_, raw, err := ws.conn.ReadMessage()
		if err != nil {
			ws.mu.Lock()
			isClosed := ws.closed
			ws.mu.Unlock()
			if !isClosed {
				slog.Warn("worker.stream.read_error", "task_id", ws.taskID, "error", err)
			}
			return
		}

		scanner := bufio.NewScanner(strings.NewReader(string(raw)))
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}
			ws.handleLine(json.RawMessage(line))
		}
	}
}

// handleLine parses a single NDJSON line and dispatches based on the type field.
func (ws *WorkerSession) handleLine(raw json.RawMessage) {
	ws.lastEvent = time.Now()

	var envelope struct {
		Type    string `json:"type"`
		Subtype string `json:"subtype"`
		Request struct {
			Subtype string `json:"subtype"`
		} `json:"request"`
		Response struct {
			Subtype string `json:"subtype"`
		} `json:"response"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		slog.Warn("worker.stream.parse_error", "task_id", ws.taskID, "error", err)
		return
	}

	slog.Debug("worker.stream.recv", "type", envelope.Type, "subtype", envelope.Subtype, "task_id", ws.taskID)

	// Store in ring buffer.
	ws.mu.Lock()
	ws.seq++
	seq := ws.seq
	evt := workerEvent{Type: envelope.Type, Raw: raw, Timestamp: time.Now(), Seq: seq}
	if len(ws.eventBuf) >= ws.maxEvents {
		ws.eventBuf = ws.eventBuf[1:]
	}
	ws.eventBuf = append(ws.eventBuf, evt)
	ws.mu.Unlock()

	switch envelope.Type {
	case "system":
		subtype := envelope.Subtype
		if subtype == "" {
			subtype = envelope.Request.Subtype
		}
		ws.handleSystem(raw, subtype)
	case "assistant":
		ws.handleAssistant(raw)
	case "user":
		ws.handleUserToolResult(raw)
	case "tool_progress":
		ws.handleToolProgress(raw)
	case "result":
		ws.handleResult(raw)
	case "control_request":
		ws.handleControlRequest(raw, envelope.Request.Subtype)
	case "control_response":
		ws.handleControlResponse(raw, envelope.Response.Subtype)
	case "keep_alive":
		// ignored
	default:
		slog.Debug("worker.stream.unknown_type", "type", envelope.Type, "task_id", ws.taskID)
	}
}

// handleSystem processes system messages. On subtype=init, captures session metadata
// and sends the task prompt.
func (ws *WorkerSession) handleSystem(raw json.RawMessage, subtype string) {
	if subtype != "init" {
		return
	}

	var msg struct {
		SessionID        string `json:"session_id"`
		Model            string `json:"model"`
		ClaudeCodeVer    string `json:"claude_code_version"`
		PermissionMode   string `json:"permissionMode"`
		Tools            []any  `json:"tools"`
	}
	if err := json.Unmarshal(raw, &msg); err != nil {
		slog.Warn("worker.stream.system_parse", "task_id", ws.taskID, "error", err)
		return
	}

	ws.mu.Lock()
	ws.sessionID = msg.SessionID
	ws.model = msg.Model
	ws.mu.Unlock()

	slog.Info("worker.stream.init",
		"task_id", ws.taskID,
		"session_id", msg.SessionID,
		"model", msg.Model,
		"claude_code_version", msg.ClaudeCodeVer,
		"permission_mode", msg.PermissionMode,
		"tools_count", len(msg.Tools),
	)

	ws.broadcastEvent(workerEventInit, WorkerStreamPayload{
		TeamID:    ws.teamID.String(),
		TaskID:    ws.taskID.String(),
		EventType: workerEventInit,
		Seq:       ws.seq,
		Model:     msg.Model,
		SessionID: msg.SessionID,
	})

	// Send the task prompt as a user message.
	userMsg := map[string]any{
		"type": "user",
		"message": map[string]any{
			"role":    "user",
			"content": ws.prompt,
		},
		"parent_tool_use_id": nil,
		"session_id":         msg.SessionID,
	}
	if err := ws.sendNDJSON(userMsg); err != nil {
		slog.Warn("worker.stream.send_prompt_error", "task_id", ws.taskID, "error", err)
	}
}

// handleAssistant extracts content blocks (text, tool_use, thinking) from assistant messages.
func (ws *WorkerSession) handleAssistant(raw json.RawMessage) {
	var msg struct {
		Message struct {
			Content []struct {
				Type  string          `json:"type"`
				Text  string          `json:"text,omitempty"`
				Name  string          `json:"name,omitempty"`
				ID    string          `json:"id,omitempty"`
				Input json.RawMessage `json:"input,omitempty"`
			} `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(raw, &msg); err != nil {
		slog.Warn("worker.stream.assistant_parse", "task_id", ws.taskID, "error", err)
		return
	}

	for _, block := range msg.Message.Content {
		switch block.Type {
		case "text":
			ws.broadcastEvent(workerEventText, WorkerStreamPayload{
				TeamID:    ws.teamID.String(),
				TaskID:    ws.taskID.String(),
				EventType: workerEventText,
				Seq:       ws.seq,
				Text:      block.Text,
			})
		case "tool_use":
			inputStr := string(block.Input)
			ws.broadcastEvent(workerEventTool, WorkerStreamPayload{
				TeamID:    ws.teamID.String(),
				TaskID:    ws.taskID.String(),
				EventType: workerEventTool,
				Seq:       ws.seq,
				ToolName:  block.Name,
				ToolInput: inputStr,
			})
		case "thinking":
			// Thinking blocks are logged but not broadcast.
		}
	}
}

// handleUserToolResult processes user messages from CLI (tool_result feedback).
func (ws *WorkerSession) handleUserToolResult(raw json.RawMessage) {
	ws.broadcastEvent(workerEventToolResult, WorkerStreamPayload{
		TeamID:    ws.teamID.String(),
		TaskID:    ws.taskID.String(),
		EventType: workerEventToolResult,
		Seq:       ws.seq,
	})
}

// handleToolProgress processes tool_progress messages.
func (ws *WorkerSession) handleToolProgress(raw json.RawMessage) {
	var msg struct {
		ToolName    string  `json:"tool_name"`
		ElapsedTime float64 `json:"elapsed_time"`
	}
	if err := json.Unmarshal(raw, &msg); err != nil {
		return
	}

	ws.broadcastEvent(workerEventProgress, WorkerStreamPayload{
		TeamID:    ws.teamID.String(),
		TaskID:    ws.taskID.String(),
		EventType: workerEventProgress,
		Seq:       ws.seq,
		ToolName:  msg.ToolName,
	})
}

// handleResult processes the result message — the critical completion event.
func (ws *WorkerSession) handleResult(raw json.RawMessage) {
	var msg struct {
		Subtype      string  `json:"subtype"`
		Result       string  `json:"result"`
		TotalCostUSD float64 `json:"total_cost_usd"`
		NumTurns     float64 `json:"num_turns"`
		DurationMs   float64 `json:"duration_ms"`
		IsError      bool    `json:"is_error"`
	}
	if err := json.Unmarshal(raw, &msg); err != nil {
		slog.Warn("worker.stream.result_parse", "task_id", ws.taskID, "error", err)
		return
	}

	ws.mu.Lock()
	ws.ResultSubtype = msg.Subtype
	ws.ResultText = msg.Result
	ws.CostUSD = msg.TotalCostUSD
	ws.NumTurns = int(msg.NumTurns)
	ws.DurationMs = msg.DurationMs
	ws.ResultIsError = msg.IsError
	ws.mu.Unlock()

	slog.Info("worker.stream.result",
		"task_id", ws.taskID,
		"subtype", msg.Subtype,
		"cost_usd", msg.TotalCostUSD,
		"num_turns", int(msg.NumTurns),
		"duration_ms", msg.DurationMs,
		"is_error", msg.IsError,
	)

	ws.broadcastEvent(workerEventResult, WorkerStreamPayload{
		TeamID:     ws.teamID.String(),
		TaskID:     ws.taskID.String(),
		EventType:  workerEventResult,
		Seq:        ws.seq,
		Text:       msg.Result,
		CostUSD:    msg.TotalCostUSD,
		NumTurns:   int(msg.NumTurns),
		DurationMs: msg.DurationMs,
		IsError:    msg.IsError,
	})

	// Signal completion.
	select {
	case <-ws.doneCh:
		// already closed
	default:
		close(ws.doneCh)
	}

	// Send end_session so Claude CLI exits gracefully.
	endMsg := map[string]any{
		"type":       "control_request",
		"request_id": uuid.NewString(),
		"request": map[string]any{
			"subtype": "end_session",
		},
	}
	if err := ws.sendNDJSON(endMsg); err != nil {
		slog.Warn("worker.stream.end_session_error", "task_id", ws.taskID, "error", err)
	}
}

// handleControlRequest auto-approves can_use_tool requests.
func (ws *WorkerSession) handleControlRequest(raw json.RawMessage, subtype string) {
	if subtype != "can_use_tool" {
		slog.Info("worker.stream.control_request", "subtype", subtype, "task_id", ws.taskID)
		return
	}

	var msg struct {
		RequestID string `json:"request_id"`
		Request   struct {
			ToolName string         `json:"tool_name"`
			Input    map[string]any `json:"input"`
		} `json:"request"`
	}
	if err := json.Unmarshal(raw, &msg); err != nil {
		slog.Warn("worker.stream.control_parse", "task_id", ws.taskID, "error", err)
		return
	}

	slog.Debug("worker.stream.auto_approve_tool", "tool", msg.Request.ToolName, "request_id", msg.RequestID, "task_id", ws.taskID)

	resp := map[string]any{
		"type": "control_response",
		"response": map[string]any{
			"subtype":    "success",
			"request_id": msg.RequestID,
			"response": map[string]any{
				"behavior":     "allow",
				"updatedInput": msg.Request.Input,
			},
		},
	}
	if err := ws.sendNDJSON(resp); err != nil {
		slog.Warn("worker.stream.approve_send_error", "task_id", ws.taskID, "error", err)
	}
}

// handleControlResponse processes control_response messages from CLI.
func (ws *WorkerSession) handleControlResponse(raw json.RawMessage, subtype string) {
	if subtype != "success" {
		slog.Warn("worker.stream.control_response_error", "subtype", subtype, "task_id", ws.taskID)
		return
	}

	var msg struct {
		Response struct {
			Response struct {
				Models []any `json:"models"`
			} `json:"response"`
		} `json:"response"`
	}
	if err := json.Unmarshal(raw, &msg); err != nil {
		return
	}

	slog.Info("worker.stream.initialized", "task_id", ws.taskID, "models_count", len(msg.Response.Response.Models))

	// Send the task prompt immediately after initialize succeeds.
	// Per the protocol (confirmed by spike), the prompt must be sent BEFORE system/init.
	// CLI waits for a user message to start processing; system/init arrives after.
	ws.mu.Lock()
	promptSent := ws.sessionID != "" // if sessionID set, prompt already sent via handleSystem
	ws.mu.Unlock()
	if !promptSent {
		slog.Info("worker.stream.send_prompt_after_init", "task_id", ws.taskID)
		userMsg := map[string]any{
			"type": "user",
			"message": map[string]any{
				"role":    "user",
				"content": ws.prompt,
			},
			"parent_tool_use_id": nil,
			"session_id":         "",
		}
		if err := ws.sendNDJSON(userMsg); err != nil {
			slog.Warn("worker.stream.send_prompt_error", "task_id", ws.taskID, "error", err)
		}
	}
}

// writeLoop reads from injectCh and sends messages to the WS connection.
// Also sends keepalive pings every 30 seconds.
func (ws *WorkerSession) writeLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case msg, ok := <-ws.injectCh:
			if !ok {
				return
			}
			ws.writeMu.Lock()
			err := ws.conn.WriteMessage(websocket.TextMessage, msg)
			ws.writeMu.Unlock()
			if err != nil {
				slog.Warn("worker.stream.write_error", "task_id", ws.taskID, "error", err)
				return
			}

		case <-ticker.C:
			ws.writeMu.Lock()
			err := ws.conn.WriteMessage(websocket.PingMessage, nil)
			ws.writeMu.Unlock()
			if err != nil {
				return
			}

		case <-ws.doneCh:
			return
		}
	}
}

// SendUserMessage queues a user message to be sent to the CLI.
func (ws *WorkerSession) SendUserMessage(content string) error {
	ws.mu.Lock()
	sid := ws.sessionID
	ws.mu.Unlock()

	msg := map[string]any{
		"type": "user",
		"message": map[string]any{
			"role":    "user",
			"content": content,
		},
		"parent_tool_use_id": nil,
		"session_id":         sid,
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal user message: %w", err)
	}
	data = append(data, '\n')

	select {
	case ws.injectCh <- data:
		return nil
	default:
		return fmt.Errorf("inject channel full")
	}
}

// SendInterrupt sends a control_request{subtype:"interrupt"} directly to the CLI.
// Bypasses injectCh for urgency.
func (ws *WorkerSession) SendInterrupt() error {
	msg := map[string]any{
		"type":       "control_request",
		"request_id": uuid.NewString(),
		"request": map[string]any{
			"subtype": "interrupt",
		},
	}
	slog.Info("worker.stream.send_interrupt", "task_id", ws.taskID)
	return ws.sendNDJSON(msg)
}

// SendControlResponse sends a control_response for permission requests.
func (ws *WorkerSession) SendControlResponse(requestID, behavior string, input map[string]any) error {
	msg := map[string]any{
		"type": "control_response",
		"response": map[string]any{
			"subtype":    "success",
			"request_id": requestID,
			"response": map[string]any{
				"behavior":     behavior,
				"updatedInput": input,
			},
		},
	}
	return ws.sendNDJSON(msg)
}

// Close shuts down the session, closing the WS connection and signaling doneCh.
func (ws *WorkerSession) Close() {
	ws.mu.Lock()
	defer ws.mu.Unlock()

	if ws.closed {
		return
	}
	ws.closed = true

	select {
	case <-ws.doneCh:
	default:
		close(ws.doneCh)
	}

	ws.conn.Close()
}

// broadcastEvent sends a worker stream event via the event bus.
func (ws *WorkerSession) broadcastEvent(eventType string, payload any) {
	if ws.eventPub == nil {
		return
	}
	ws.eventPub.Broadcast(bus.Event{
		Name:     eventType,
		Payload:  payload,
		TenantID: ws.tenantID,
	})
}
