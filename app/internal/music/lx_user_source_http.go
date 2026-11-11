package music

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/dop251/goja"
)

const (
	lxRequestTimeout = 5 * time.Second
	lxRequestBodyLimit = 2 << 20
	lxRequestRedirectLimit = 4
)

// installLXRequest exposes only the callback-based LX HTTP bridge. Requests are
// restricted to public HTTP(S) hosts and bounded in time and response size.
func installLXRequest(vm *goja.Runtime, lx *goja.Object) error {
	return lx.Set("request", func(call goja.FunctionCall) goja.Value {
		rawURL := strings.TrimSpace(call.Argument(0).String())
		callback, ok := goja.AssertFunction(call.Argument(2))
		if !ok {
			return vm.ToValue("LX request callback is required")
		}
		response, body, err := performLXRequest(rawURL, call.Argument(1))
		var errorValue goja.Value = goja.Null()
		if err != nil {
			errorValue = vm.ToValue(err.Error())
		}
		_, callbackErr := callback(goja.Undefined(), errorValue, vm.ToValue(response), vm.ToValue(body))
		if callbackErr != nil {
			return vm.ToValue(callbackErr.Error())
		}
		return goja.Undefined()
	})
}

func performLXRequest(rawURL string, optionsValue goja.Value) (map[string]any, string, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil { return nil, "", errors.New("LX request URL 无效") }
	if err := validateLXSourceURL(parsed); err != nil { return nil, "", err }

	options := map[string]any{}
	if optionsValue != nil && !goja.IsUndefined(optionsValue) && !goja.IsNull(optionsValue) {
		if exported, ok := optionsValue.Export().(map[string]interface{}); ok { options = exported }
	}
	method := strings.ToUpper(strings.TrimSpace(fmt.Sprint(options["method"])))
	if method == "" || method == "<NIL>" { method = http.MethodGet }
	if method != http.MethodGet && method != http.MethodPost && method != http.MethodPut && method != http.MethodDelete {
		return nil, "", errors.New("LX request 仅支持 GET、POST、PUT、DELETE")
	}

	requestBody := []byte(nil)
	if value, ok := options["body"]; ok && value != nil {
		switch body := value.(type) {
		case string: requestBody = []byte(body)
		case []byte: requestBody = body
		default: requestBody = []byte(fmt.Sprint(body))
		}
	}
	if form, ok := options["form"].(map[string]interface{}); ok {
		values := url.Values{}
		for key, value := range form { values.Set(key, fmt.Sprint(value)) }
		requestBody = []byte(values.Encode())
	}
	if _, ok := options["formData"]; ok {
		return nil, "", errors.New("LX request formData 暂不支持；请使用 form 或 body")
	}
	if len(requestBody) > lxRequestBodyLimit { return nil, "", errors.New("LX request 请求体超过 2 MB 限制") }

	timeout := lxRequestTimeout
	if value, ok := options["timeout"]; ok {
		if number, ok := value.(int64); ok && number > 0 { timeout = time.Duration(number) * time.Millisecond }
		if number, ok := value.(float64); ok && number > 0 { timeout = time.Duration(number) * time.Millisecond }
	}
	if timeout > lxRequestTimeout { timeout = lxRequestTimeout }
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, method, parsed.String(), bytes.NewReader(requestBody))
	if err != nil { return nil, "", err }
	if headers, ok := options["headers"].(map[string]interface{}); ok {
		for key, value := range headers {
			name := http.CanonicalHeaderKey(strings.TrimSpace(key))
			if name == "" || name == "Host" || name == "Content-Length" || name == "Connection" || strings.HasPrefix(strings.ToLower(name), "proxy-") { continue }
			request.Header.Set(name, fmt.Sprint(value))
		}
	}
	if _, ok := request.Header["Content-Type"]; !ok && len(requestBody) > 0 {
		if _, form := options["form"].(map[string]interface{}); form { request.Header.Set("Content-Type", "application/x-www-form-urlencoded") }
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = (&LXUserSourceStore{}).dialPublic
	client := &http.Client{
		Transport: transport,
		Timeout: timeout,
		CheckRedirect: func(next *http.Request, via []*http.Request) error {
			if len(via) > lxRequestRedirectLimit { return errors.New("LX request 重定向次数过多") }
			return validateLXSourceURL(next.URL)
		},
	}
	defer transport.CloseIdleConnections()
	resp, err := client.Do(request)
	if err != nil { return nil, "", fmt.Errorf("LX request 请求失败：%w", err) }
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, lxRequestBodyLimit+1))
	if err != nil { return nil, "", fmt.Errorf("读取 LX request 响应失败：%w", err) }
	if len(data) > lxRequestBodyLimit { return nil, "", errors.New("LX request 响应体超过 2 MB 限制") }
	responseHeaders := make(map[string]string, len(resp.Header))
	for key, values := range resp.Header { responseHeaders[key] = strings.Join(values, ", ") }
	response := map[string]any{
		"statusCode": resp.StatusCode,
		"headers": responseHeaders,
		"url": resp.Request.URL.String(),
		"statusMessage": resp.Status,
	}
	return response, string(data), nil
}
