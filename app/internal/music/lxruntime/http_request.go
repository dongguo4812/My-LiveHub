//go:build (lxquickjs || lxproduction) && cgo

package lxruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	lxDefaultRequestTimeout = 60 * time.Second
	lxMaxRequestTimeout     = 60 * time.Second
	lxMaxResponseBytes      = 8 << 20
)

type lxRequestDescriptor struct {
	ID       uint64                     `json:"id"`
	URL      string                     `json:"url"`
	Method   string                     `json:"method"`
	Timeout  int64                      `json:"timeout"`
	Headers  map[string]any             `json:"headers"`
	Body     json.RawMessage            `json:"body"`
	Form     map[string]any             `json:"form"`
	FormData map[string]any             `json:"formData"`
}

type lxHTTPResponse struct {
	StatusCode    int                 `json:"statusCode"`
	StatusMessage string              `json:"statusMessage"`
	Headers       map[string][]string `json:"headers"`
	Bytes         int                 `json:"bytes"`
	Raw           []byte              `json:"raw"`
	Body          any                 `json:"body"`
}

func executeLXHTTPRequest(parent context.Context, descriptor lxRequestDescriptor, client *http.Client) (*lxHTTPResponse, error) {
	return executeLXHTTPRequestWithPolicy(parent, descriptor, client, false)
}

// executeLXHTTPRequestWithPolicy is used by tests with httptest loopback servers.
// Production dispatch always calls executeLXHTTPRequest and keeps the policy closed.
func executeLXHTTPRequestWithPolicy(parent context.Context, descriptor lxRequestDescriptor, client *http.Client, allowPrivate bool) (*lxHTTPResponse, error) {
	if parent == nil {
		parent = context.Background()
	}
	parsed, err := url.Parse(strings.TrimSpace(descriptor.URL))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return nil, errors.New("lx.request: only absolute HTTP(S) URLs are supported")
	}
	method := strings.ToUpper(strings.TrimSpace(descriptor.Method))
	if method == "" {
		method = http.MethodGet
	}
	if !validHTTPToken(method) {
		return nil, fmt.Errorf("lx.request: invalid HTTP method %q", method)
	}
	timeout := lxRequestTimeout(descriptor.Timeout)
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	body, contentType, err := buildLXRequestBody(descriptor)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, parsed.String(), body)
	if err != nil {
		return nil, fmt.Errorf("lx.request: create request: %w", err)
	}
	for key, value := range descriptor.Headers {
		if strings.TrimSpace(key) == "" {
			continue
		}
		req.Header.Set(key, fmt.Sprint(value))
	}
	if contentType != "" && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", contentType)
	}
	safeClient, err := lxSafeHTTPClientWithPolicy(client, allowPrivate)
	if err != nil {
		return nil, fmt.Errorf("lx.request: configure network policy: %w", err)
	}
	resp, err := safeClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("lx.request: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, lxMaxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("lx.request: read response: %w", err)
	}
	if len(raw) > lxMaxResponseBytes {
		return nil, fmt.Errorf("lx.request: response exceeds %d bytes", lxMaxResponseBytes)
	}
	var parsedBody any
	if err := json.Unmarshal(raw, &parsedBody); err != nil {
		parsedBody = string(raw)
	}
	return &lxHTTPResponse{
		StatusCode: resp.StatusCode, StatusMessage: resp.Status,
		Headers: resp.Header.Clone(), Bytes: len(raw), Raw: raw, Body: parsedBody,
	}, nil
}

func lxRequestTimeout(milliseconds int64) time.Duration {
	if milliseconds <= 0 {
		return lxDefaultRequestTimeout
	}
	maxMilliseconds := int64(lxMaxRequestTimeout / time.Millisecond)
	if milliseconds > maxMilliseconds {
		return lxMaxRequestTimeout
	}
	return time.Duration(milliseconds) * time.Millisecond
}

func lxJSONTruthy(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return false
	}
	switch typed := value.(type) {
	case nil:
		return false
	case bool:
		return typed
	case float64:
		return typed != 0
	case string:
		return typed != ""
	default:
		// JavaScript arrays and objects are truthy, including empty ones.
		return true
	}
}

func buildLXRequestBody(descriptor lxRequestDescriptor) (io.Reader, string, error) {
	if lxJSONTruthy(descriptor.Body) {
		var value any
		if err := json.Unmarshal(descriptor.Body, &value); err != nil {
			return nil, "", fmt.Errorf("lx.request: invalid body: %w", err)
		}
		if text, ok := value.(string); ok {
			return strings.NewReader(text), "", nil
		}
		data, err := json.Marshal(value)
		if err != nil {
			return nil, "", fmt.Errorf("lx.request: encode body: %w", err)
		}
		return bytes.NewReader(data), "application/json", nil
	}
	if descriptor.Form != nil {
		values := url.Values{}
		for key, value := range descriptor.Form {
			values.Set(key, fmt.Sprint(value))
		}
		return strings.NewReader(values.Encode()), "application/x-www-form-urlencoded", nil
	}
	if descriptor.FormData != nil {
		var buffer bytes.Buffer
		writer := multipart.NewWriter(&buffer)
		for key, value := range descriptor.FormData {
			if err := writer.WriteField(key, fmt.Sprint(value)); err != nil {
				return nil, "", fmt.Errorf("lx.request: encode formData: %w", err)
			}
		}
		if err := writer.Close(); err != nil {
			return nil, "", fmt.Errorf("lx.request: finalize formData: %w", err)
		}
		return &buffer, writer.FormDataContentType(), nil
	}
	return nil, "", nil
}

func validHTTPToken(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r <= 32 || r >= 127 || strings.ContainsRune("()<>@,;:\\\"/[]?={}", r) {
			return false
		}
	}
	return true
}
