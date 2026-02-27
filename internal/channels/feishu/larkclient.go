package feishu

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"net/http"
	"strconv"
	"sync"
	"time"
)

const (
	tokenExpiryBuffer = 3 * time.Minute
	tokenEndpoint     = "/open-apis/auth/v3/tenant_access_token/internal"
)

// LarkClient is a lightweight Feishu/Lark API client using net/http.
// Handles tenant_access_token auto-refresh and all REST API calls.
type LarkClient struct {
	baseURL    string
	appID      string
	appSecret  string
	httpClient *http.Client

	mu       sync.Mutex
	token    string
	tokenExp time.Time
}

// NewLarkClient creates a native Lark HTTP client.
func NewLarkClient(appID, appSecret, baseURL string) *LarkClient {
	return &LarkClient{
		baseURL:    baseURL,
		appID:      appID,
		appSecret:  appSecret,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// --- Token management ---

func (c *LarkClient) getToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.token != "" && time.Now().Before(c.tokenExp) {
		return c.token, nil
	}

	body, _ := json.Marshal(map[string]string{
		"app_id":     c.appID,
		"app_secret": c.appSecret,
	})

	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+tokenEndpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("lark token request: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Code              int    `json:"code"`
		Msg               string `json:"msg"`
		TenantAccessToken string `json:"tenant_access_token"`
		Expire            int    `json:"expire"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("lark token decode: %w", err)
	}
	if result.Code != 0 {
		return "", fmt.Errorf("lark token error: code=%d msg=%s", result.Code, result.Msg)
	}

	c.token = result.TenantAccessToken
	c.tokenExp = time.Now().Add(time.Duration(result.Expire)*time.Second - tokenExpiryBuffer)
	return c.token, nil
}

func (c *LarkClient) clearToken() {
	c.mu.Lock()
	c.token = ""
	c.tokenExp = time.Time{}
	c.mu.Unlock()
}

// isTokenError returns true if the error code indicates an expired/invalid token.
func isTokenError(code int) bool {
	return code == 99991663 || code == 99991664 || code == 99991671
}

// --- Generic API helpers ---

type apiResponse struct {
	Code int             `json:"code"`
	Msg  string          `json:"msg"`
	Data json.RawMessage `json:"data"`
}

// doJSON performs an authenticated JSON API call with auto token refresh.
func (c *LarkClient) doJSON(ctx context.Context, method, path string, body interface{}) (*apiResponse, error) {
	resp, err := c.doJSONOnce(ctx, method, path, body)
	if err != nil {
		return nil, err
	}
	// Retry once on token error
	if isTokenError(resp.Code) {
		c.clearToken()
		return c.doJSONOnce(ctx, method, path, body)
	}
	return resp, nil
}

func (c *LarkClient) doJSONOnce(ctx context.Context, method, path string, body interface{}) (*apiResponse, error) {
	token, err := c.getToken(ctx)
	if err != nil {
		return nil, err
	}

	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal body: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bodyReader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json; charset=utf-8")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("lark api %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	var result apiResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("lark api decode: %w", err)
	}
	return &result, nil
}

// doDownload performs an authenticated GET that returns raw bytes.
func (c *LarkClient) doDownload(ctx context.Context, path string) ([]byte, string, error) {
	token, err := c.getToken(ctx)
	if err != nil {
		return nil, "", err
	}

	req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+path, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("lark download %s: %w", path, err)
	}
	defer resp.Body.Close()

	// Check for JSON error response
	ct := resp.Header.Get("Content-Type")
	if ct != "" {
		mt, _, _ := mime.ParseMediaType(ct)
		if mt == "application/json" {
			var errResp apiResponse
			if err := json.NewDecoder(resp.Body).Decode(&errResp); err == nil && errResp.Code != 0 {
				return nil, "", fmt.Errorf("lark download error: code=%d msg=%s", errResp.Code, errResp.Msg)
			}
		}
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("lark read download: %w", err)
	}

	// Extract filename from Content-Disposition
	fileName := ""
	if cd := resp.Header.Get("Content-Disposition"); cd != "" {
		_, params, _ := mime.ParseMediaType(cd)
		fileName = params["filename"]
	}

	return data, fileName, nil
}

// doMultipart performs an authenticated multipart upload.
func (c *LarkClient) doMultipart(ctx context.Context, path string, fields map[string]string, fileField string, fileData io.Reader, fileName string) (*apiResponse, error) {
	token, err := c.getToken(ctx)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	for k, v := range fields {
		writer.WriteField(k, v)
	}

	if fileField != "" && fileData != nil {
		if fileName == "" {
			fileName = "upload"
		}
		part, err := writer.CreateFormFile(fileField, fileName)
		if err != nil {
			return nil, fmt.Errorf("create form file: %w", err)
		}
		if _, err := io.Copy(part, fileData); err != nil {
			return nil, fmt.Errorf("copy file data: %w", err)
		}
	}
	writer.Close()

	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+path, &buf)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("lark upload %s: %w", path, err)
	}
	defer resp.Body.Close()

	var result apiResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("lark upload decode: %w", err)
	}
	return &result, nil
}

// --- IM API: Messages ---

type SendMessageResp struct {
	MessageID string `json:"message_id"`
}

func (c *LarkClient) SendMessage(ctx context.Context, receiveIDType, receiveID, msgType, content string) (*SendMessageResp, error) {
	path := "/open-apis/im/v1/messages?receive_id_type=" + receiveIDType
	body := map[string]string{
		"receive_id": receiveID,
		"msg_type":   msgType,
		"content":    content,
	}
	resp, err := c.doJSON(ctx, "POST", path, body)
	if err != nil {
		return nil, err
	}
	if resp.Code != 0 {
		return nil, fmt.Errorf("send message: code=%d msg=%s", resp.Code, resp.Msg)
	}
	var data SendMessageResp
	json.Unmarshal(resp.Data, &data)
	return &data, nil
}

// --- IM API: Images ---

func (c *LarkClient) DownloadImage(ctx context.Context, imageKey string) ([]byte, error) {
	path := "/open-apis/im/v1/images/" + imageKey
	data, _, err := c.doDownload(ctx, path)
	return data, err
}

func (c *LarkClient) UploadImage(ctx context.Context, data io.Reader) (string, error) {
	resp, err := c.doMultipart(ctx, "/open-apis/im/v1/images",
		map[string]string{"image_type": "message"},
		"image", data, "image.png")
	if err != nil {
		return "", err
	}
	if resp.Code != 0 {
		return "", fmt.Errorf("upload image: code=%d msg=%s", resp.Code, resp.Msg)
	}
	var result struct {
		ImageKey string `json:"image_key"`
	}
	json.Unmarshal(resp.Data, &result)
	return result.ImageKey, nil
}

// --- IM API: Files ---

func (c *LarkClient) UploadFile(ctx context.Context, data io.Reader, fileName, fileType string, durationMs int) (string, error) {
	fields := map[string]string{
		"file_type": fileType,
		"file_name": fileName,
	}
	if durationMs > 0 {
		fields["duration"] = strconv.Itoa(durationMs)
	}
	resp, err := c.doMultipart(ctx, "/open-apis/im/v1/files", fields, "file", data, fileName)
	if err != nil {
		return "", err
	}
	if resp.Code != 0 {
		return "", fmt.Errorf("upload file: code=%d msg=%s", resp.Code, resp.Msg)
	}
	var result struct {
		FileKey string `json:"file_key"`
	}
	json.Unmarshal(resp.Data, &result)
	return result.FileKey, nil
}

// --- IM API: Message Resources ---

func (c *LarkClient) DownloadMessageResource(ctx context.Context, messageID, fileKey, resourceType string) ([]byte, string, error) {
	path := fmt.Sprintf("/open-apis/im/v1/messages/%s/resources/%s?type=%s", messageID, fileKey, resourceType)
	return c.doDownload(ctx, path)
}

// --- CardKit API ---

func (c *LarkClient) CreateCard(ctx context.Context, cardType, data string) (string, error) {
	resp, err := c.doJSON(ctx, "POST", "/open-apis/cardkit/v1/cards", map[string]string{
		"type": cardType,
		"data": data,
	})
	if err != nil {
		return "", err
	}
	if resp.Code != 0 {
		return "", fmt.Errorf("create card: code=%d msg=%s", resp.Code, resp.Msg)
	}
	var result struct {
		CardID string `json:"card_id"`
	}
	json.Unmarshal(resp.Data, &result)
	return result.CardID, nil
}

func (c *LarkClient) UpdateCardSettings(ctx context.Context, cardID, settings string, seq int, uuid string) error {
	path := "/open-apis/cardkit/v1/cards/" + cardID
	resp, err := c.doJSON(ctx, "PATCH", path, map[string]interface{}{
		"settings": settings,
		"sequence": seq,
		"uuid":     uuid,
	})
	if err != nil {
		return err
	}
	if resp.Code != 0 {
		return fmt.Errorf("update card settings: code=%d msg=%s", resp.Code, resp.Msg)
	}
	return nil
}

func (c *LarkClient) UpdateCardElement(ctx context.Context, cardID, elementID, content string, seq int, uuid string) error {
	path := fmt.Sprintf("/open-apis/cardkit/v1/cards/%s/elements/%s", cardID, elementID)
	resp, err := c.doJSON(ctx, "PATCH", path, map[string]interface{}{
		"content":  content,
		"sequence": seq,
		"uuid":     uuid,
	})
	if err != nil {
		return err
	}
	if resp.Code != 0 {
		slog.Debug("lark update card element failed", "code", resp.Code, "msg", resp.Msg)
		return fmt.Errorf("update card element: code=%d msg=%s", resp.Code, resp.Msg)
	}
	return nil
}

// --- Bot API ---

// GetBotInfo fetches the bot's identity from /open-apis/bot/v3/info.
// Returns the bot's open_id which is needed for mention detection in groups.
func (c *LarkClient) GetBotInfo(ctx context.Context) (string, error) {
	resp, err := c.doJSON(ctx, "GET", "/open-apis/bot/v3/info", nil)
	if err != nil {
		return "", err
	}
	if resp.Code != 0 {
		return "", fmt.Errorf("get bot info: code=%d msg=%s", resp.Code, resp.Msg)
	}
	var result struct {
		Bot struct {
			OpenID string `json:"open_id"`
		} `json:"bot"`
	}
	json.Unmarshal(resp.Data, &result)
	return result.Bot.OpenID, nil
}

// --- Contact API ---

func (c *LarkClient) GetUser(ctx context.Context, userID, userIDType string) (string, error) {
	path := fmt.Sprintf("/open-apis/contact/v3/users/%s?user_id_type=%s", userID, userIDType)
	resp, err := c.doJSON(ctx, "GET", path, nil)
	if err != nil {
		return "", err
	}
	if resp.Code != 0 {
		return "", fmt.Errorf("get user: code=%d msg=%s", resp.Code, resp.Msg)
	}
	var result struct {
		User struct {
			Name string `json:"name"`
		} `json:"user"`
	}
	json.Unmarshal(resp.Data, &result)
	return result.User.Name, nil
}
