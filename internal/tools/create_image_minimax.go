package tools

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/nextlevelbuilder/goclaw/internal/providers"
)

// callMinimaxImageGen calls the MiniMax image generation API.
// Endpoint: POST {apiBase}/image_generation
// Response: base64 image data in data.image_list[0].base64_image
// aspectRatioToMinimaxSize converts aspect_ratio (e.g. "16:9") to MiniMax size format.
// Falls back to explicit "size" param if set, otherwise uses aspect_ratio mapping.
func aspectRatioToMinimaxSize(params map[string]any) string {
	// Explicit size param takes priority (from UI provider settings)
	if s := GetParamString(params, "size", ""); s != "" {
		return s
	}
	ar := GetParamString(params, "aspect_ratio", "1:1")
	switch ar {
	case "16:9":
		return "1280*720"
	case "9:16":
		return "720*1280"
	case "4:3":
		return "1024*768"
	case "3:4":
		return "768*1024"
	default:
		return "1024*1024"
	}
}

func callMinimaxImageGen(ctx context.Context, apiKey, apiBase, model, prompt string, params map[string]any) ([]byte, *providers.Usage, error) {
	size := aspectRatioToMinimaxSize(params)
	promptOptimizer := GetParamBool(params, "prompt_optimizer", true)

	body := map[string]any{
		"model":                model,
		"prompt":               prompt,
		"size":                 size,
		"num_images":           1,
		"enable_base64_output": true,
		"prompt_optimizer":     promptOptimizer,
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal request: %w", err)
	}

	url := strings.TrimRight(apiBase, "/") + "/image_generation"
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	client := &http.Client{} // timeout governed by chain context
	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("API error %d: %s", resp.StatusCode, truncateBytes(respBody, 500))
	}

	var minimaxResp struct {
		Data *struct {
			ImageList []struct {
				Base64Image string `json:"base64_image"`
			} `json:"image_list"`
		} `json:"data"`
		BaseResp *struct {
			StatusCode int    `json:"status_code"`
			StatusMsg  string `json:"status_msg"`
		} `json:"base_resp"`
	}
	if err := json.Unmarshal(respBody, &minimaxResp); err != nil {
		return nil, nil, fmt.Errorf("parse response: %w", err)
	}

	if minimaxResp.BaseResp != nil && minimaxResp.BaseResp.StatusCode != 0 {
		return nil, nil, fmt.Errorf("MiniMax API error %d: %s",
			minimaxResp.BaseResp.StatusCode, minimaxResp.BaseResp.StatusMsg)
	}

	if minimaxResp.Data == nil || len(minimaxResp.Data.ImageList) == 0 {
		return nil, nil, fmt.Errorf("no image data in MiniMax response")
	}

	b64 := minimaxResp.Data.ImageList[0].Base64Image
	if b64 == "" {
		return nil, nil, fmt.Errorf("empty base64_image in MiniMax response")
	}

	imageBytes, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, nil, fmt.Errorf("decode base64: %w", err)
	}

	return imageBytes, nil, nil
}
