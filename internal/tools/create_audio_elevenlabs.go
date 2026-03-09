package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// callElevenLabsSoundEffect generates a sound effect via the ElevenLabs sound generation API.
// Endpoint: POST {baseURL}/v1/sound-generation
// Auth: xi-api-key header (NOT Bearer token).
// Response: binary audio data directly (not JSON).
func (t *CreateAudioTool) callElevenLabsSoundEffect(ctx context.Context, prompt string, durationSeconds int) ([]byte, error) {
	baseURL := t.elevenlabsBaseURL
	if baseURL == "" {
		baseURL = "https://api.elevenlabs.io"
	}

	// Cap duration at ElevenLabs max of 30 seconds.
	if durationSeconds > 30 {
		durationSeconds = 30
	}

	body := map[string]any{
		"text":             prompt,
		"output_format":    "mp3_44100_128",
		"prompt_influence": 0.3,
	}
	if durationSeconds > 0 {
		body["duration_seconds"] = durationSeconds
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	url := strings.TrimRight(baseURL, "/") + "/v1/sound-generation"
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("xi-api-key", t.elevenlabsAPIKey)

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ElevenLabs API error %d: %s", resp.StatusCode, truncateBytes(errBody, 500))
	}

	audioBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read audio response: %w", err)
	}
	if len(audioBytes) == 0 {
		return nil, fmt.Errorf("empty audio response from ElevenLabs")
	}

	return audioBytes, nil
}
