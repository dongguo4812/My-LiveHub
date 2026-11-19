//go:build lxquickjs && cgo

package lxruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// DispatchLXRequest invokes one LX source action and services every lx.request
// operation queued by the source. All JavaScript execution remains on the
// QuickJS runtime thread; Go performs network I/O between Eval calls.
func (r *Runtime) DispatchLXRequest(ctx context.Context, source, action string, info any) (any, error) {
	if r == nil {
		return nil, errors.New("LX dispatch: runtime is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(source) == "" || strings.TrimSpace(action) == "" {
		return nil, errors.New("LX dispatch: source and action are required")
	}
	payload, err := json.Marshal(map[string]any{"source": source, "action": action, "info": info})
	if err != nil {
		return nil, fmt.Errorf("LX dispatch: encode action: %w", err)
	}
	start := `
(() => {
  const handler = globalThis.__lxHostHandlers && globalThis.__lxHostHandlers.request;
  if (typeof handler !== 'function') throw new Error('LX request handler is not registered');
  globalThis.__lxDispatchResult = { done: false, value: null, error: '' };
  Promise.resolve().then(() => handler(JSON.parse(` + string(mustJSONQuote(string(payload))) + `)))
    .then(value => { globalThis.__lxDispatchResult = { done: true, value, error: '' }; })
    .catch(error => { globalThis.__lxDispatchResult = { done: true, value: null, error: String(error && error.message || error) }; });
  'started';
})()
`
	if _, err := r.Eval(ctx, start, "lx-dispatch-start.js"); err != nil {
		return nil, err
	}

	const maxRequests = 256
	processed := 0
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		stateText, err := r.Eval(ctx, `
JSON.stringify({
  done: !!globalThis.__lxDispatchResult.done,
  value: globalThis.__lxDispatchResult.value,
  error: globalThis.__lxDispatchResult.error || '',
  requests: globalThis.__lxTakeRequests()
})
`, "lx-dispatch-poll.js")
		if err != nil {
			return nil, err
		}
		var state struct {
			Done     bool                 `json:"done"`
			Value    any                  `json:"value"`
			Error    string               `json:"error"`
			Requests []lxRequestDescriptor `json:"requests"`
		}
		if err := json.Unmarshal([]byte(stateText), &state); err != nil {
			return nil, fmt.Errorf("LX dispatch: decode runtime state: %w", err)
		}
		if len(state.Requests) == 0 {
			if state.Done {
				if state.Error != "" {
					return nil, errors.New(state.Error)
				}
				return state.Value, nil
			}
			return nil, errors.New("LX dispatch: source action is waiting for an unsupported asynchronous host capability")
		}
		for _, request := range state.Requests {
			processed++
			if processed > maxRequests {
				return nil, fmt.Errorf("LX dispatch: request limit (%d) exceeded", maxRequests)
			}
			response, requestErr := executeLXHTTPRequest(ctx, request, http.DefaultClient)
			if err := r.completeLXRequest(ctx, request.ID, response, requestErr); err != nil {
				return nil, err
			}
		}
	}
}

func (r *Runtime) completeLXRequest(ctx context.Context, id uint64, response *lxHTTPResponse, requestErr error) error {
	var errorText any
	if requestErr != nil {
		errorText = requestErr.Error()
	}
	var responseValue any
	if response != nil {
		raw := make([]int, len(response.Raw))
		for i, value := range response.Raw {
			raw[i] = int(value)
		}
		responseValue = map[string]any{
			"statusCode": response.StatusCode,
			"statusMessage": response.StatusMessage,
			"headers": response.Headers,
			"bytes": response.Bytes,
			"raw": raw,
			"body": response.Body,
		}
	}
	data, err := json.Marshal(responseValue)
	if err != nil {
		return fmt.Errorf("LX dispatch: encode HTTP response: %w", err)
	}
	script := `
(() => {
  const response = JSON.parse(` + string(mustJSONQuote(string(data))) + `);
  if (response) response.raw = new Uint8Array(response.raw);
  globalThis.__lxCompleteRequest(` + fmt.Sprint(id) + `, ` + string(mustJSONQuote(errorText)) + `, response);
  'completed';
})()
`
	if _, err := r.Eval(ctx, script, "lx-dispatch-complete.js"); err != nil {
		return fmt.Errorf("LX dispatch: deliver HTTP response: %w", err)
	}
	return nil
}

func mustJSONQuote(value any) []byte {
	data, err := json.Marshal(value)
	if err != nil {
		return []byte("null")
	}
	return data
}
