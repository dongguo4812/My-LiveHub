package music

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
	"sync/atomic"

	"github.com/dop251/goja"
)

const (
	// LX userApi preload.js defaults lx.request to 60 seconds and caps it at 60 seconds.
	lxRequestTimeout = 60 * time.Second
	lxRequestRedirectLimit = 0
)

// installLXRequest exposes only the callback-based LX HTTP bridge. Requests are
// restricted to public HTTP(S) hosts and bounded in time and response size.
func installLXRequest(vm *goja.Runtime, lx *goja.Object) error {
	return installLXRequestWithClient(vm, lx, nil)
}

// installLXRequestWithClient keeps the production transport policy centralized
// while allowing deterministic HTTP bridge tests to inject a mock client.
func installLXRequestAsync(vm *goja.Runtime, lx *goja.Object, client *http.Client, loop *lxRuntimeEventLoop, onResult func(int64, string, map[string]any, error)) error {
	var requestID atomic.Int64
	return lx.Set("request", func(call goja.FunctionCall) goja.Value {
		rawURL:=strings.TrimSpace(call.Argument(0).String())
		options:=call.Argument(1)
		callback,ok:=goja.AssertFunction(call.Argument(2))
		if !ok { return vm.ToValue(func(goja.FunctionCall) goja.Value { return goja.Undefined() }) }
		optionsMap:=map[string]any{}
		if options!=nil && !goja.IsUndefined(options) && !goja.IsNull(options) {
			if exported,ok:=options.Export().(map[string]interface{}); ok { optionsMap=exported }
		}
		ctx,cancel:=context.WithCancel(loop.ctx)
		id:=requestID.Add(1)
		thisArg:=call.This
		go func() {
			defer cancel()
			response,raw,err:=performLXRequestWithClientContext(ctx,rawURL,optionsMap,client)
			loop.enqueue(func() {
				if onResult != nil { onResult(id, rawURL, response, err) }
				var errValue goja.Value=goja.Null()
				var responseValue goja.Value=goja.Null()
				var bodyValue goja.Value=goja.Null()
				if err!=nil { errValue=vm.NewGoError(err) } else {
					response["bytes"]=len(raw)
					response["raw"]=newLXBuffer(vm,[]byte(raw))
					responseValue=vm.ToValue(response)
					bodyValue=vm.ToValue(response["body"])
				}
				if _,callbackErr:=callback(thisArg,errValue,responseValue,bodyValue); callbackErr!=nil { vm.Interrupt(callbackErr) }
			})
		}()
		return vm.ToValue(func(goja.FunctionCall) goja.Value { cancel(); return goja.Undefined() })
	})
}

func installLXRequestWithClient(vm *goja.Runtime, lx *goja.Object, client *http.Client) error {
	return lx.Set("request", func(call goja.FunctionCall) goja.Value {
		rawURL := strings.TrimSpace(call.Argument(0).String())
		callback, ok := goja.AssertFunction(call.Argument(2))
		if !ok {
			return vm.ToValue("LX request callback is required")
		}
		response, _, err := performLXRequestWithClient(rawURL, call.Argument(1), client)
		var errorValue goja.Value = goja.Null()
		if err != nil {
			errorValue = vm.ToValue(err.Error())
		}
		_, callbackErr := callback(goja.Undefined(), errorValue, vm.ToValue(response), vm.ToValue(response["body"]))
		if callbackErr != nil {
			return vm.ToValue(callbackErr.Error())
		}
		// LX exposes a cancellation function. Requests currently complete
		// synchronously in the isolated worker, so cancellation is a safe no-op.
		return vm.ToValue(func(goja.FunctionCall) goja.Value { return goja.Undefined() })
	})
}

func performLXRequest(rawURL string, optionsValue goja.Value) (map[string]any, string, error) {
	return performLXRequestWithClient(rawURL, optionsValue, nil)
}

func performLXRequestWithClient(rawURL string, optionsValue goja.Value, client *http.Client) (map[string]any, string, error) {
	options:=map[string]any{}
	if optionsValue!=nil && !goja.IsUndefined(optionsValue) && !goja.IsNull(optionsValue) { if exported,ok:=optionsValue.Export().(map[string]interface{}); ok { options=exported } }
	return performLXRequestWithClientContext(context.Background(),rawURL,options,client)
}

func performLXRequestWithClientContext(parent context.Context, rawURL string, options map[string]any, client *http.Client) (map[string]any, string, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil { return nil, "", errors.New("LX request URL 无效") }
	if err := validateLXSourceURL(parsed); err != nil { return nil, "", err }

	method := strings.ToUpper(strings.TrimSpace(fmt.Sprint(options["method"])))
	if method == "" || method == "<NIL>" { method = http.MethodGet }
	if method != http.MethodGet && method != http.MethodHead && method != http.MethodPost && method != http.MethodPut && method != http.MethodPatch && method != http.MethodDelete && method != http.MethodOptions {
		return nil, "", errors.New("LX request HTTP method 不受支持")
	}

	headers, _ := options["headers"].(map[string]interface{})
	contentType := ""
	for key, value := range headers { if strings.EqualFold(strings.TrimSpace(key), "content-type") { contentType = strings.TrimSpace(fmt.Sprint(value)); break } }
	data, hasData := options["body"]
	formMode := false
	if !jsTruthy(data) {
		data, hasData = options["form"]
		if jsTruthy(data) { formMode = true } else {
			data, hasData = options["formData"]
			if jsTruthy(data) { formMode = true }
		}
	}
	requestBody := []byte(nil)
	if hasData && jsTruthy(data) {
		switch value := data.(type) {
		case string:
			requestBody = []byte(value)
		case []byte:
			requestBody = value
		case map[string]interface{}, map[string]string, []interface{}:
			jsonMode := !formMode && strings.EqualFold(strings.TrimSpace(contentType), "application/json")
			if jsonMode {
				requestBody, err = json.Marshal(value)
				if err != nil { return nil, "", fmt.Errorf("LX request body JSON 编码失败：%w", err) }
			} else {
				encoded, encodeErr := needleQueryString(value, "")
				if encodeErr != nil { return nil, "", fmt.Errorf("LX request body 表单编码失败：%w", encodeErr) }
				if method == http.MethodGet {
					parsed.RawQuery = encoded
				} else {
					requestBody = []byte(encoded)
				}
			}
		default:
			requestBody = []byte(fmt.Sprint(value))
		}
	}

	timeout := lxRequestTimeout
	if value, ok := options["timeout"]; ok {
		if number, ok := value.(int64); ok && number > 0 { timeout = time.Duration(number) * time.Millisecond }
		if number, ok := value.(float64); ok && number > 0 { timeout = time.Duration(number) * time.Millisecond }
	}
	if timeout > 60*time.Second { timeout = 60*time.Second }
	if timeout <= 0 { timeout = lxRequestTimeout }
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, method, parsed.String(), bytes.NewReader(requestBody))
	if err != nil { return nil, "", err }
	request.Header.Set("Accept", "*/*")
	request.Header.Set("User-Agent", "Needle/3.2.0")
	for key, value := range headers {
		name := http.CanonicalHeaderKey(strings.TrimSpace(key))
		if name == "" || name == "Host" || name == "Content-Length" || name == "Connection" || strings.HasPrefix(strings.ToLower(name), "proxy-") { continue }
		request.Header.Set(name, fmt.Sprint(value))
	}
	if len(requestBody) > 0 {
		if _, ok := request.Header["Content-Type"]; !ok {
			if strings.EqualFold(strings.TrimSpace(contentType), "application/json") && !formMode {
				request.Header.Set("Content-Type", "application/json; charset=utf-8")
			} else {
				request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			}
		}
	}

	if client == nil {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.Proxy = http.ProxyFromEnvironment
		transport.DisableCompression = true // Needle 3.2.0 defaults compressed=false.
		client = &http.Client{
			Transport: transport,
			Timeout: timeout,
			CheckRedirect: func(next *http.Request, via []*http.Request) error {
				if lxRequestRedirectLimit == 0 { return http.ErrUseLastResponse } // Needle defaults follow_max=0 and returns the 3xx response.
				if len(via) > lxRequestRedirectLimit { return errors.New("LX request 重定向次数过多") }
				return validateLXSourceURL(next.URL)
			},
		}
		defer transport.CloseIdleConnections()
	}
	resp, err := client.Do(request)
	if err != nil {
		var urlErr *url.Error
		if errors.As(err, &urlErr) && urlErr.Err != nil {
			if endpoint := safeLXRequestEndpoint(rawURL); endpoint != "" { return nil, "", fmt.Errorf("LX request 请求失败（%s）：%w", endpoint, urlErr.Err) }
			return nil, "", fmt.Errorf("LX request 请求失败：%w", urlErr.Err)
		}
		return nil, "", errors.New("LX request 请求失败")
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil { return nil, "", fmt.Errorf("读取 LX request 响应失败：%w", err) }
	responseHeaders := make(map[string]string, len(resp.Header))
	for key, values := range resp.Header { responseHeaders[strings.ToLower(key)] = strings.Join(values, ", ") }
	var responseBody any = string(raw)
	var decoded any
	if json.Unmarshal(raw, &decoded) == nil { responseBody = decoded }
	statusMessage := strings.TrimSpace(strings.TrimPrefix(resp.Status, fmt.Sprintf("%d", resp.StatusCode)))
	response := map[string]any{
		"statusCode": resp.StatusCode,
		"statusMessage": statusMessage,
		"headers": responseHeaders,
		"bytes": len(raw),
		"raw": raw,
		"body": responseBody,
	}
	return response, string(raw), nil
}

func jsTruthy(value any) bool {
	if value == nil { return false }
	switch v := value.(type) {
	case bool: return v
	case string: return v != ""
	case int: return v != 0
	case int64: return v != 0
	case float64: return v != 0
	default: return true
	}
}

func needleQueryString(value any, prefix string) (string, error) {
	switch v := value.(type) {
	case map[string]interface{}:
		parts := make([]string, 0, len(v))
		keys := make([]string, 0, len(v))
		for key := range v { keys = append(keys, key) }
		sort.Strings(keys)
		for _, key := range keys {
			name := needleEncodeURIComponent(key)
			if prefix != "" { name = prefix + "[" + name + "]" }
			part, err := needleQueryString(v[key], name)
			if err != nil { return "", err }
			if part != "" { parts = append(parts, part) }
		}
		return strings.Join(parts, "&"), nil
	case map[string]string:
		parts := make([]string, 0, len(v))
		keys := make([]string, 0, len(v))
		for key := range v { keys = append(keys, key) }
		sort.Strings(keys)
		for _, key := range keys {
			name := needleEncodeURIComponent(key)
			if prefix != "" { name = prefix + "[" + name + "]" }
			part, err := needleQueryString(v[key], name)
			if err != nil { return "", err }
			parts = append(parts, part)
		}
		return strings.Join(parts, "&"), nil
	case []interface{}:
		parts := make([]string, 0, len(v))
		for _, item := range v {
			part, err := needleQueryString(item, prefix+"[]")
			if err != nil { return "", err }
			if part != "" { parts = append(parts, part) }
		}
		return strings.Join(parts, "&"), nil
	case nil:
		if prefix == "" { return "", errors.New("Cannot build a querystring out of: null") }
		return prefix + "=", nil
	case string:
		if prefix == "" {
			if strings.Contains(v, "=") { return v, nil }
			return "", fmt.Errorf("Cannot build a querystring out of: %s", v)
		}
		return prefix + "=" + needleEncodeURIComponent(v), nil
	default:
		if prefix == "" { return "", fmt.Errorf("Cannot build a querystring out of: %v", value) }
		return prefix + "=" + needleEncodeURIComponent(fmt.Sprint(value)), nil
	}
}

func needleEncodeURIComponent(value string) string {
	encoded := url.QueryEscape(value)
	encoded = strings.ReplaceAll(encoded, "+", "%20")
	for _, pair := range [][2]string{{"%21","!"},{"%27","'"},{"%28","("},{"%29",")"},{"%2A","*"}} {
		encoded = strings.ReplaceAll(encoded, pair[0], pair[1])
	}
	return encoded
}
