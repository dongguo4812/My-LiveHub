//go:build lxquickjs || lxproduction

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
	"net/http/httptrace"
	"net/url"
	"strings"
	"time"
)

const (
	lxDefaultRequestTimeout = 60 * time.Second
	lxMaxRequestTimeout     = 60 * time.Second
	lxMaxResponseBytes      = 8 << 20
	lxMaxRequestBodyBytes   = 2 << 20
	lxMaxRequestURLBytes    = 8 << 10
	lxMaxRequestHeaders     = 100
	lxMaxRequestHeaderBytes = 64 << 10
	lxMaxConcurrentRequests = 8
)

var lxRequestSemaphore = make(chan struct{}, lxMaxConcurrentRequests)

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
	Headers       map[string]any `json:"headers"`
	Bytes         int                 `json:"bytes"`
	Raw           []byte              `json:"raw"`
	Body          any                 `json:"body"`
}

func executeLXHTTPRequest(parent context.Context, descriptor lxRequestDescriptor, client *http.Client) (*lxHTTPResponse, error) {
	return executeLXHTTPRequestWithPolicy(parent, descriptor, client, false)
}

// executeLXHTTPRequestWithPolicy is used by tests with httptest loopback servers.
// Production dispatch always calls executeLXHTTPRequest and keeps the policy closed.
func executeLXHTTPRequestWithPolicy(parent context.Context, descriptor lxRequestDescriptor, client *http.Client, allowPrivate bool) (response *lxHTTPResponse, requestErr error) {
	if parent == nil {
		parent = context.Background()
	}
	if len(descriptor.URL) > lxMaxRequestURLBytes {
		return nil, fmt.Errorf("lx.request: URL exceeds %d bytes", lxMaxRequestURLBytes)
	}
	parsed, err := url.Parse(strings.TrimSpace(descriptor.URL))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return nil, errors.New("lx.request: only absolute HTTP(S) URLs are supported")
	}
	method := strings.ToUpper(strings.TrimSpace(descriptor.Method))
	if method == "" {
		method = http.MethodGet
	}
	ctx = context.WithValue(ctx, lxTimingRequestIDKey{}, descriptor.ID)
	httpStarted := time.Now()
	statusCode, responseBytes := 0, 0
	defer func() {
		logLXTiming("http_request", httpStarted, fmt.Sprintf("request_id=%d host=%q method=%s status=%d bytes=%d result=%s", descriptor.ID, parsed.Host, method, statusCode, responseBytes, timingResult(requestErr)))
	}()
	if !validHTTPToken(method) {
		return nil, fmt.Errorf("lx.request: invalid HTTP method %q", method)
	}
	if err := validateLXRequestHeaders(descriptor.Headers); err != nil {
		return nil, err
	}
	timeout := lxRequestTimeout(descriptor.Timeout)
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	// Acquire the global request budget before DNS resolution. Otherwise a
	// burst of scripts can bypass the concurrency limit by fanning out DNS
	// lookups before any request reaches the semaphore.
	semaphoreStarted := time.Now()
	select {
	case lxRequestSemaphore <- struct{}{}:
		logLXTiming("http_semaphore_wait", semaphoreStarted, fmt.Sprintf("host=%q", parsed.Host))
		defer func() { <-lxRequestSemaphore }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	validationStarted := time.Now()
	if err := validateLXDestinationWithPolicy(ctx, parsed, allowPrivate); err != nil {
		logLXTiming("http_destination_validation", validationStarted, fmt.Sprintf("request_id=%d host=%q result=%s", descriptor.ID, parsed.Host, timingResult(err)))
		return nil, err
	}
	logLXTiming("http_destination_validation", validationStarted, fmt.Sprintf("request_id=%d host=%q result=ok", descriptor.ID, parsed.Host))

	body, contentType, err := buildLXRequestBody(descriptor)
	if err != nil {
		return nil, err
	}
	if body != nil {
		bodyBytes, readErr := io.ReadAll(io.LimitReader(body, lxMaxRequestBodyBytes+1))
		if readErr != nil {
			return nil, fmt.Errorf("lx.request: read request body: %w", readErr)
		}
		if len(bodyBytes) > lxMaxRequestBodyBytes {
			return nil, fmt.Errorf("lx.request: request body exceeds %d bytes", lxMaxRequestBodyBytes)
		}
		body = bytes.NewReader(bodyBytes)
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
	var traceMu sync.Mutex
	var wroteRequestAt, firstByteAt time.Time
	trace := &httptrace.ClientTrace{
		TLSHandshakeStart: func() { tlsStarted := time.Now(); traceMu.Lock(); wroteRequestAt = tlsStarted; traceMu.Unlock() },
		TLSHandshakeDone: func(_ tls.ConnectionState, err error) {
			traceMu.Lock(); started := wroteRequestAt; traceMu.Unlock()
			if !started.IsZero() { logLXTiming("http_tls_handshake", started, fmt.Sprintf("request_id=%d host=%q result=%s", descriptor.ID, parsed.Host, timingResult(err))) }
		},
		GotConn: func(info httptrace.GotConnInfo) {
			logLXTiming("http_connection_acquired", httpStarted, fmt.Sprintf("request_id=%d host=%q reused=%t was_idle=%t", descriptor.ID, parsed.Host, info.Reused, info.WasIdle))
		},
		WroteRequest: func(info httptrace.WroteRequestInfo) {
			traceMu.Lock(); wroteRequestAt = time.Now(); traceMu.Unlock()
			logLXTiming("http_request_written", httpStarted, fmt.Sprintf("request_id=%d host=%q result=%s", descriptor.ID, parsed.Host, timingResult(info.Err)))
		},
		GotFirstResponseByte: func() {
			traceMu.Lock(); firstByteAt = time.Now(); wrote := wroteRequestAt; traceMu.Unlock()
			if !wrote.IsZero() { logLXTiming("http_ttfb", wrote, fmt.Sprintf("request_id=%d host=%q", descriptor.ID, parsed.Host)) }
		},
	}
	req = req.WithContext(httptrace.WithClientTrace(req.Context(), trace))
	resp, err := safeClient.Do(req)
	if err != nil {
		// Keep endpoint diagnostics useful without exposing signed query values,
		// cookies, or request bodies in script error messages.
		return nil, fmt.Errorf("lx.request %s %s: %w", method, parsed.Host+parsed.EscapedPath(), err)
	}
	statusCode = resp.StatusCode
	defer resp.Body.Close()
	bodyReadStarted := time.Now()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, lxMaxResponseBytes+1))
	logLXTiming("http_response_read", bodyReadStarted, fmt.Sprintf("request_id=%d host=%q status=%d bytes=%d result=%s", descriptor.ID, parsed.Host, resp.StatusCode, len(raw), timingResult(err)))
	if err != nil {
		return nil, fmt.Errorf("lx.request: read response: %w", err)
	}
	if len(raw) > lxMaxResponseBytes {
		return nil, fmt.Errorf("lx.request: response exceeds %d bytes", lxMaxResponseBytes)
	}
	responseBytes = len(raw)
	jsonStarted := time.Now()
	var parsedBody any
	if err := json.Unmarshal(raw, &parsedBody); err != nil {
		parsedBody = string(raw)
	}
	logLXTiming("http_response_decode", jsonStarted, fmt.Sprintf("request_id=%d host=%q bytes=%d", descriptor.ID, parsed.Host, len(raw)))
	headers := make(map[string]any, len(resp.Header))
	for key, values := range resp.Header {
		lowerKey := strings.ToLower(key)
		if lowerKey == "set-cookie" {
			headers[lowerKey] = append([]string(nil), values...)
		} else {
			headers[lowerKey] = strings.Join(values, ", ")
		}
	}
	statusMessage := strings.TrimPrefix(resp.Status, fmt.Sprintf("%d ", resp.StatusCode))
	return &lxHTTPResponse{
		StatusCode: resp.StatusCode, StatusMessage: statusMessage,
		Headers: headers, Bytes: len(raw), Raw: raw, Body: parsedBody,
	}, nil
}

func validateLXRequestHeaders(headers map[string]any) error {
	if len(headers) > lxMaxRequestHeaders {
		return fmt.Errorf("lx.request: too many request headers (max %d)", lxMaxRequestHeaders)
	}
	total := 0
	for key, value := range headers {
		valueText := fmt.Sprint(value)
		total += len(key) + len(valueText)
		if !validHTTPToken(key) {
			return errors.New("lx.request: invalid request header name")
		}
		switch strings.ToLower(key) {
		case "connection", "proxy-connection", "keep-alive", "proxy-authenticate",
			"proxy-authorization", "te", "trailer", "transfer-encoding", "upgrade",
			"content-length", "host":
			return errors.New("lx.request: hop-by-hop or framing header is not allowed")
		}
		for i := 0; i < len(valueText); i++ {
			b := valueText[i]
			// HTTP field values permit horizontal tab and visible/obs-text
			// bytes, but no other C0 controls or DEL.
			if (b < 0x20 && b != '\t') || b == 0x7f {
				return errors.New("lx.request: invalid request header value")
			}
		}
	}
	if total > lxMaxRequestHeaderBytes {
		return fmt.Errorf("lx.request: request headers exceed %d bytes", lxMaxRequestHeaderBytes)
	}
	return nil
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
	if len(descriptor.Body) > lxMaxRequestBodyBytes {
		return nil, "", fmt.Errorf("lx.request: request body exceeds %d bytes", lxMaxRequestBodyBytes)
	}
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
		estimatedBytes := 0
		for key, value := range descriptor.Form {
			text := fmt.Sprint(value)
			estimatedBytes += len(key)*3 + len(text)*3 + 2
			if estimatedBytes > lxMaxRequestBodyBytes {
				return nil, "", fmt.Errorf("lx.request: request body exceeds %d bytes", lxMaxRequestBodyBytes)
			}
			values.Set(key, text)
		}
		encoded := values.Encode()
		if len(encoded) > lxMaxRequestBodyBytes {
			return nil, "", fmt.Errorf("lx.request: request body exceeds %d bytes", lxMaxRequestBodyBytes)
		}
		return strings.NewReader(encoded), "application/x-www-form-urlencoded", nil
	}
	if descriptor.FormData != nil {
		buffer := &lxLimitedBuffer{limit: lxMaxRequestBodyBytes}
		writer := multipart.NewWriter(buffer)
		for key, value := range descriptor.FormData {
			if err := writer.WriteField(key, fmt.Sprint(value)); err != nil {
				return nil, "", fmt.Errorf("lx.request: encode formData: %w", err)
			}
		}
		if err := writer.Close(); err != nil {
			return nil, "", fmt.Errorf("lx.request: finalize formData: %w", err)
		}
		return bytes.NewReader(buffer.buffer.Bytes()), writer.FormDataContentType(), nil
	}
	return nil, "", nil
}

type lxLimitedBuffer struct {
	buffer bytes.Buffer
	limit int
}

func (b *lxLimitedBuffer) Write(value []byte) (int, error) {
	if b == nil || b.limit < 0 || len(value) > b.limit-b.buffer.Len() {
		return 0, fmt.Errorf("lx.request: request body exceeds %d bytes", b.limit)
	}
	return b.buffer.Write(value)
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


