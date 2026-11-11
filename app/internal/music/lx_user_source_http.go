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
	"strconv"
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

// installLXRequest exposes the callback-based HTTP bridge provided by LX.
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
		optionsMap := lxObjectMap(lxExportRequestValue(options))
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
	options := lxObjectMap(lxExportRequestValue(optionsValue))
	return performLXRequestWithClientContext(context.Background(),rawURL,options,client)
}

func performLXRequestWithClientContext(parent context.Context, rawURL string, options map[string]any, client *http.Client) (map[string]any, string, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil { return nil, "", errors.New("LX request URL 无效") }
	if err := validateLXSourceURL(parsed); err != nil { return nil, "", err }

	method := strings.ToUpper(strings.TrimSpace(fmt.Sprint(options["method"])))
	if method == "" || method == "<NIL>" { method = http.MethodGet }
	headers := lxObjectMap(options["headers"])
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
		case lxOrderedObject, map[string]interface{}, map[string]string, []interface{}:
			jsonMode := !formMode && strings.EqualFold(strings.TrimSpace(contentType), "application/json")
			if jsonMode {
				requestBody, err = json.Marshal(value)
				if err != nil { return nil, "", fmt.Errorf("LX request body JSON 编码失败：%w", err) }
			} else {
				encoded, encodeErr := needleQueryString(value, "")
				if encodeErr != nil { return nil, "", fmt.Errorf("LX request body 表单编码失败：%w", encodeErr) }
				if method == http.MethodGet {
					if parsed.RawQuery == "" { parsed.RawQuery = encoded } else { parsed.RawQuery += "&" + encoded }
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
		if name == "" { continue }
		switch name {
		case "Host":
			request.Host = fmt.Sprint(value)
		case "Content-Length":
			if length, parseErr := strconv.ParseInt(fmt.Sprint(value), 10, 64); parseErr == nil && length >= 0 {
				request.ContentLength = length
			}
		default:
			request.Header.Set(name, fmt.Sprint(value))
		}
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

type lxOrderedObject struct {
	Keys []string
	Values map[string]any
}

func (o lxOrderedObject) MarshalJSON() ([]byte, error) {
	var buffer bytes.Buffer
	buffer.WriteByte('{')
	for index, key := range o.Keys {
		if index > 0 { buffer.WriteByte(',') }
		encodedKey, err := json.Marshal(key)
		if err != nil { return nil, err }
		encodedValue, err := json.Marshal(o.Values[key])
		if err != nil { return nil, err }
		buffer.Write(encodedKey)
		buffer.WriteByte(':')
		buffer.Write(encodedValue)
	}
	buffer.WriteByte('}')
	return buffer.Bytes(), nil
}

func lxExportRequestValue(value goja.Value) any {
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) { return nil }
	exported := value.Export()
	object, isObject := value.(*goja.Object)
	if !isObject { return exported }
	switch exported.(type) {
	case []interface{}:
		length := object.Get("length").ToInteger()
		items := make([]any, int(length))
		for index := range items { items[index] = lxExportRequestValue(object.Get(strconv.Itoa(index))) }
		return items
	case map[string]interface{}:
		keys := object.Keys()
		values := make(map[string]any, len(keys))
		for _, key := range keys { values[key] = lxExportRequestValue(object.Get(key)) }
		return lxOrderedObject{Keys: keys, Values: values}
	default:
		return exported
	}
}

func lxObjectMap(value any) map[string]any {
	switch object := value.(type) {
	case lxOrderedObject:
		return object.Values
	case map[string]interface{}:
		return object
	default:
		return map[string]any{}
	}
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
	case lxOrderedObject:
		parts := make([]string, 0, len(v.Keys))
		for _, key := range v.Keys {
			name := needleEncodeURIComponent(key)
			if prefix != "" { name = prefix + "[" + name + "]" }
			part, err := needleQueryString(v.Values[key], name)
			if err != nil { return "", err }
			if part != "" { parts = append(parts, part) }
		}
		return strings.Join(parts, "&"), nil
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
