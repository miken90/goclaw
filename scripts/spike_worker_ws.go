// spike_worker_ws is a minimal WebSocket server to test Claude CLI --sdk-url.
//
// Usage:
//   go run scripts/spike_worker_ws.go
//   # Then in another terminal (Windows):
//   claude --sdk-url ws://localhost:9876 --print --output-format stream-json --input-format stream-json --dangerously-skip-permissions -p ""
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

// sendNDJSON sends a JSON object as a single NDJSON line over the WS connection.
func sendNDJSON(conn *websocket.Conn, mu *sync.Mutex, msg any) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	mu.Lock()
	defer mu.Unlock()
	return conn.WriteMessage(websocket.TextMessage, data)
}

func handleWS(w http.ResponseWriter, r *http.Request) {
	log.Printf("[CONNECT] Claude CLI connected from %s", r.RemoteAddr)
	log.Printf("[HEADERS] Authorization: %s", r.Header.Get("Authorization"))

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[ERROR] upgrade failed: %v", err)
		return
	}
	defer conn.Close()

	var mu sync.Mutex
	var sessionID string
	var gotInit bool
	var sentPrompt bool
	var msgCount int

	// Send initialize control_request immediately after connection.
	// Per the protocol, server must send this before the first user message.
	initRequestID := fmt.Sprintf("init-%d", time.Now().UnixNano())
	log.Printf("[SEND] Sending initialize control_request...")
	if err := sendNDJSON(conn, &mu, map[string]any{
		"type":       "control_request",
		"request_id": initRequestID,
		"request": map[string]any{
			"subtype": "initialize",
		},
	}); err != nil {
		log.Printf("[ERROR] Failed to send initialize: %v", err)
		return
	}

	// Keepalive ticker
	keepalive := time.NewTicker(30 * time.Second)
	defer keepalive.Stop()
	go func() {
		for range keepalive.C {
			if err := sendNDJSON(conn, &mu, map[string]string{"type": "keep_alive"}); err != nil {
				return
			}
		}
	}()

	// Read loop
	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			log.Printf("[DISCONNECT] %v", err)
			return
		}

		// NDJSON: may contain multiple lines
		scanner := bufio.NewScanner(strings.NewReader(string(raw)))
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}

			msgCount++
			var msg map[string]any
			if err := json.Unmarshal([]byte(line), &msg); err != nil {
				log.Printf("[PARSE ERROR] %s", line[:min(200, len(line))])
				continue
			}

			msgType, _ := msg["type"].(string)
			subtype, _ := msg["subtype"].(string)

			switch msgType {
			case "system":
				if subtype == "init" {
					sessionID, _ = msg["session_id"].(string)
					model, _ := msg["model"].(string)
					version, _ := msg["claude_code_version"].(string)
					tools, _ := msg["tools"].([]any)
					permMode, _ := msg["permissionMode"].(string)

					log.Printf("[INIT] ✅ session=%s model=%s version=%s tools=%d permissionMode=%s",
						sessionID, model, version, len(tools), permMode)
					gotInit = true

					// Send the task prompt as a user message (if not already sent)
					if !sentPrompt {
						sentPrompt = true
						log.Printf("[SEND] Sending user message (task prompt)...")
						prompt := "List the files in the current directory using the Bash tool with 'ls -la'. Then summarize what you see in 2 sentences. Do not modify any files."
						if err := sendNDJSON(conn, &mu, map[string]any{
							"type": "user",
							"message": map[string]any{
								"role":    "user",
								"content": prompt,
							},
							"parent_tool_use_id": nil,
							"session_id":         sessionID,
						}); err != nil {
							log.Printf("[ERROR] Failed to send user message: %v", err)
						}
					}
				} else if subtype == "status" {
					status, _ := msg["status"].(string)
					log.Printf("[STATUS] %s", status)
				} else {
					log.Printf("[SYSTEM/%s] %s", subtype, truncate(line, 200))
				}

			case "assistant":
				message, _ := msg["message"].(map[string]any)
				if message != nil {
					content, _ := message["content"].([]any)
					for _, block := range content {
						b, _ := block.(map[string]any)
						blockType, _ := b["type"].(string)
						switch blockType {
						case "text":
							text, _ := b["text"].(string)
							log.Printf("[ASSISTANT/text] %s", truncate(text, 300))
						case "tool_use":
							name, _ := b["name"].(string)
							id, _ := b["id"].(string)
							input, _ := json.Marshal(b["input"])
							log.Printf("[ASSISTANT/tool_use] %s (id=%s) input=%s", name, id, truncate(string(input), 200))
						case "tool_result":
							log.Printf("[ASSISTANT/tool_result] %s", truncate(fmt.Sprint(b["content"]), 200))
						case "thinking":
							log.Printf("[ASSISTANT/thinking] (len=%d)", len(fmt.Sprint(b["thinking"])))
						default:
							log.Printf("[ASSISTANT/%s] %s", blockType, truncate(line, 100))
						}
					}
				}

			case "stream_event":
				// High frequency — just count
				if msgCount%10 == 0 {
					log.Printf("[STREAM] %d events received so far...", msgCount)
				}

			case "tool_progress":
				toolName, _ := msg["tool_name"].(string)
				elapsed, _ := msg["elapsed_time_seconds"].(float64)
				log.Printf("[TOOL_PROGRESS] %s elapsed=%.0fs", toolName, elapsed)

			case "tool_use_summary":
				summary, _ := msg["summary"].(string)
				log.Printf("[TOOL_SUMMARY] %s", truncate(summary, 200))

			case "result":
				isError, _ := msg["is_error"].(bool)
				result, _ := msg["result"].(string)
				costUSD, _ := msg["total_cost_usd"].(float64)
				numTurns, _ := msg["num_turns"].(float64)
				durationMs, _ := msg["duration_ms"].(float64)

				log.Printf("[RESULT] ✅ subtype=%s is_error=%v cost=$%.4f turns=%.0f duration=%.0fs",
					subtype, isError, costUSD, numTurns, durationMs/1000)
				log.Printf("[RESULT/text] %s", truncate(result, 500))
				log.Printf("")
				log.Printf("=== SPIKE COMPLETE ===")
				log.Printf("Total messages received: %d", msgCount)
				log.Printf("Init received: %v", gotInit)
				log.Printf("Session ID: %s", sessionID)

				// Don't close — let Claude CLI close the connection gracefully
				return

			case "control_request":
				request, _ := msg["request"].(map[string]any)
				reqSubtype, _ := request["subtype"].(string)
				requestID, _ := msg["request_id"].(string)

				if reqSubtype == "can_use_tool" {
					toolName, _ := request["tool_name"].(string)
					input, _ := request["input"].(map[string]any)
					log.Printf("[PERMISSION] 🔐 tool=%s input=%s", toolName, truncate(fmt.Sprint(input), 200))

					// Auto-approve
					if err := sendNDJSON(conn, &mu, map[string]any{
						"type": "control_response",
						"response": map[string]any{
							"subtype":    "success",
							"request_id": requestID,
							"response": map[string]any{
								"behavior":     "allow",
								"updatedInput": input,
							},
						},
					}); err != nil {
						log.Printf("[ERROR] Failed to send permission response: %v", err)
					} else {
						log.Printf("[PERMISSION] ✅ Auto-approved %s", toolName)
					}
				} else {
					log.Printf("[CONTROL/%s] request_id=%s", reqSubtype, requestID)
				}

			case "keep_alive":
				// silent

			case "control_response":
				resp, _ := msg["response"].(map[string]any)
				respSubtype, _ := resp["subtype"].(string)
				if respSubtype == "success" {
					log.Printf("[CONTROL_RESPONSE] ✅ initialize succeeded")
					// Log some fields from the response
					if respData, ok := resp["response"].(map[string]any); ok {
						if models, ok := respData["models"].([]any); ok {
							log.Printf("[CONTROL_RESPONSE] Available models: %d", len(models))
						}
					}

					// After initialize succeeds, send the first user message.
					// system/init comes AFTER the first user message.
					if !sentPrompt {
						sentPrompt = true
						log.Printf("[SEND] Sending user message (task prompt) after initialize...")
						prompt := "List the files in the current directory using the Bash tool with 'ls -la'. Then summarize what you see in 2 sentences. Do not modify any files."
						if err := sendNDJSON(conn, &mu, map[string]any{
							"type": "user",
							"message": map[string]any{
								"role":    "user",
								"content": prompt,
							},
							"parent_tool_use_id": nil,
							"session_id":         "",
						}); err != nil {
							log.Printf("[ERROR] Failed to send user message: %v", err)
						}
					}
				} else {
					log.Printf("[CONTROL_RESPONSE] %s: %v", respSubtype, resp)
				}

			default:
				log.Printf("[%s] %s", strings.ToUpper(msgType), truncate(line, 200))
			}
		}
	}
}

func truncate(s string, maxLen int) string {
	s = strings.ReplaceAll(s, "\n", "\\n")
	if len(s) > maxLen {
		return s[:maxLen] + "..."
	}
	return s
}

func main() {
	port := 9876
	if p := os.Getenv("SPIKE_PORT"); p != "" {
		fmt.Sscanf(p, "%d", &port)
	}

	http.HandleFunc("/", handleWS)

	log.Printf("╔══════════════════════════════════════════════════════╗")
	log.Printf("║  Worker WS Spike Server                             ║")
	log.Printf("║  Listening on ws://localhost:%d                     ║", port)
	log.Printf("╠══════════════════════════════════════════════════════╣")
	log.Printf("║  Now run in another terminal:                       ║")
	log.Printf("║                                                     ║")
	log.Printf("║  claude --sdk-url ws://localhost:%d \\               ║", port)
	log.Printf("║    --print --output-format stream-json \\            ║")
	log.Printf("║    --input-format stream-json \\                     ║")
	log.Printf("║    --dangerously-skip-permissions -p \"\"             ║")
	log.Printf("║                                                     ║")
	log.Printf("║  Or without --dangerously-skip-permissions to test  ║")
	log.Printf("║  permission flow (server auto-approves).            ║")
	log.Printf("╚══════════════════════════════════════════════════════╝")

	// Graceful shutdown
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	go func() {
		<-sig
		log.Printf("\nShutting down...")
		os.Exit(0)
	}()

	addr := fmt.Sprintf(":%d", port)
	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}
