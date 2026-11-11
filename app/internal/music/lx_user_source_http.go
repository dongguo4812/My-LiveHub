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
	"mime/multipart"
	"strings"
	"time"
	"sync/atomic"

	"github.com/dop251/goja"
)

const (
	// LX userApi preload.js defaults lx.request to 60 seconds and caps it at 60 seconds.
	lxRequestTimeout = 60 * time.Second
	lxRequestBodyLimit = 2 << 20
	lxRequestRedirectLimit = 4
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
		ctx,cancel:=context.WithCancel(context.Background())
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
		return vm.ToValue(func(goja.FunctionCall) goja.Value { cancel(); return vm.ToValue(id) })
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

	requestBody := []byte(nil)
	if value, ok := options["body"]; ok && value != nil {
		switch body := value.(type) {
		case string: requestBody = []byte(body)
		case []byte: requestBody = body
		case map[string]interface{}, []interface{}:
			encoded, marshalErr := json.Marshal(body)
			if marshalErr != nil { return nil, "", fmt.Errorf("LX request body JSON 编码失败：%w", marshalErr) }
			requestBody = encoded
		default: requestBody = []byte(fmt.Sprint(body))
		}
	}
	if form, ok := options["form"].(map[string]interface{}); ok {
		values := url.Values{}
		for key, value := range form { values.Set(key, fmt.Sprint(value)) }
		requestBody = []byte(values.Encode())
	}
	if formData, ok := options["formData"].(map[string]interface{}); ok {
		var multipartBody bytes.Buffer
		writer := multipart.NewWriter(&multipartBody)
		for key, value := range formData {
			if err := writer.WriteField(key, fmt.Sprint(value)); err != nil {
				return nil, "", fmt.Errorf("LX request formData 字段 %q 无效：%w", key, err)
			}
		}
		if err := writer.Close(); err != nil { return nil, "", fmt.Errorf("LX request formData 编码失败：%w", err) }
		requestBody = multipartBody.Bytes()
		options["__lx_content_type"] = writer.FormDataContentType()
	} else if _, exists := options["formData"]; exists {
		return nil, "", errors.New("LX request formData 必须是普通对象")
	}
	if len(requestBody) > lxRequestBodyLimit { return nil, "", errors.New("LX request 请求体超过 2 MB 限制") }

	timeout := lxRequestTimeout
	if value, ok := options["timeout"]; ok {
		if number, ok := value.(int64); ok && number > 0 { timeout = time.Duration(number) * time.Millisecond }
		if number, ok := value.(float64); ok && number > 0 { timeout = time.Duration(number) * time.Millisecond }
	}
	if timeout > 60*time.Second { timeout = 60*time.Second }
	redirectLimit := lxRequestRedirectLimit
	if value, ok := options["follow_max"]; ok {
		if number, ok := value.(int64); ok && number >= 0 && number < 10 { redirectLimit = int(number) }
		if number, ok := value.(float64); ok && number >= 0 && number < 10 { redirectLimit = int(number) }
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
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
		if contentType, ok := options["__lx_content_type"].(string); ok {
			request.Header.Set("Content-Type", contentType)
		} else if _, form := options["form"].(map[string]interface{}); form {
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
	}

	if client == nil {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.Proxy = http.ProxyFromEnvironment
		transport.DialContext = (&LXUserSourceStore{}).dialPublic
		client = &http.Client{
			Transport: transport,
			Timeout: timeout,
			CheckRedirect: func(next *http.Request, via []*http.Request) error {
				if len(via) > redirectLimit { return errors.New("LX request 重定向次数过多") }
				return validateLXSourceURL(next.URL)
			},
		}
		defer transport.CloseIdleConnections()
	}
	resp, err := client.Do(request)
	if err != nil {
		// net/http wraps transport errors in url.Error, whose Error() string
		// includes the full request URL (including signed query parameters).
		// Keep the useful transport cause while exposing only a sanitized endpoint.
		var urlErr *url.Error
		if errors.As(err, &urlErr) && urlErr.Err != nil {
			if endpoint := safeLXRequestEndpoint(rawURL); endpoint != "" {
				return nil, "", fmt.Errorf("LX request 请求失败（%s）：%w", endpoint, urlErr.Err)
			}
			return nil, "", fmt.Errorf("LX request 请求失败：%w", urlErr.Err)
		}
		return nil, "", errors.New("LX request 请求失败")
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, lxRequestBodyLimit+1))
	if err != nil { return nil, "", fmt.Errorf("读取 LX request 响应失败：%w", err) }
	if len(data) > lxRequestBodyLimit { return nil, "", errors.New("LX request 响应体超过 2 MB 限制") }
	responseHeaders := make(map[string]string, len(resp.Header))
	for key, values := range resp.Header { responseHeaders[key] = strings.Join(values, ", ") }
	// LX Desktop's needle wrapper attempts JSON.parse on every response body,
	// independent of Content-Type and leading whitespace. Match that behavior
	// so source scripts receive the same response.body value as they do in LX.
	var responseBody any = string(data)
	var decoded any
	if json.Unmarshal(data, &decoded) == nil { responseBody = decoded }
	responseURL := parsed.String()
	if resp.Request != nil && resp.Request.URL != nil { responseURL = resp.Request.URL.String() }
	response := map[string]any{
		"statusCode": resp.StatusCode,
		"status": resp.StatusCode,
		"headers": responseHeaders,
		"url": responseURL,
		"statusMessage": resp.Status,
		// LX Music scripts read response.body; JSON responses are decoded like LX Desktop.
		"body": responseBody,
	}
	return response, string(data), nil
}
