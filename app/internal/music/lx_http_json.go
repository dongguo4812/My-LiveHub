package music

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

// doLXRaw is the shared bounded HTTP transport for the retained LX Go
// adapters. It preserves request cancellation and applies the common headers,
// response-size limit, and HTTP status handling.
func doLXRaw(ctx context.Context, client *http.Client, method, rawURL string, headers map[string]string, body io.Reader) ([]byte, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if client == nil {
		client = &http.Client{Timeout: 18 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "lx-music request")
	req.Header.Set("bHhtl", "bHhtl")
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求音乐平台失败：%w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, fmt.Errorf("读取音乐平台响应失败：%w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("音乐平台返回 HTTP %d", resp.StatusCode)
	}
	return raw, nil
}

func doLXJSON(ctx context.Context, client *http.Client, method, rawURL string, headers map[string]string, body io.Reader) (map[string]any, error) {
	raw, err := doLXRaw(ctx, client, method, rawURL, headers, body)
	if err != nil {
		return nil, err
	}
	var result map[string]any
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("解析音乐平台响应失败：%w", err)
	}
	return result, nil
}

func jsonBody(value any) ([]byte, error) { return json.Marshal(value) }
func mapValue(value any) map[string]any { v, _ := value.(map[string]any); return v }
func sliceValue(value any) []any { v, _ := value.([]any); return v }
func stringValue(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case json.Number:
		return v.String()
	case float64:
		return strconv.FormatInt(int64(v), 10)
	case int:
		return strconv.Itoa(v)
	case int64:
		return strconv.FormatInt(v, 10)
	default:
		return ""
	}
}
func intValue(value any) int64 {
	switch v := value.(type) {
	case float64:
		return int64(v)
	case json.Number:
		n, _ := v.Int64()
		return n
	case string:
		n, _ := strconv.ParseInt(v, 10, 64)
		return n
	case int:
		return int64(v)
	case int64:
		return v
	default:
		return 0
	}
}
