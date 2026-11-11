package music

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"
)

func TestLXSourceSessionWorkerUsesRequestKeysAndKeepsRuntimeAlive(t *testing.T) {
	inputReader, inputWriter := io.Pipe()
	outputReader, outputWriter := io.Pipe()
	workerDone := make(chan int, 1)
	go func() { workerDone <- RunLXUserSourceSessionWorker(inputReader, outputWriter) }()

	script := `let count = 0;
lx.on(lx.EVENT_NAMES.request, function (request) {
  return new Promise(function (resolve) {
    setTimeout(function () {
      count++;
      resolve("https://cdn.example.test/" + request.info.musicInfo.id + "-" + count + ".mp3");
    }, request.info.delay);
  });
});
lx.send(lx.EVENT_NAMES.inited, {
  sources: { wy: { type: "music", actions: ["musicUrl"], qualitys: ["320k"] } }
});`
	init, _ := json.Marshal(lxSessionWorkerInput{Operation: "init", Script: script})
	if _, err := inputWriter.Write(append(init, '\n')); err != nil { t.Fatal(err) }
	reader := bufio.NewReader(outputReader)
	readyLine, err := reader.ReadBytes('\n')
	if err != nil { t.Fatal(err) }
	var ready lxSessionWorkerOutput
	if err := json.Unmarshal(readyLine, &ready); err != nil { t.Fatal(err) }
	if ready.Type != "ready" || ready.Error != "" || !ready.Result.Initialized {
		t.Fatalf("worker did not initialize: %#v", ready)
	}

	sendRequest := func(key, id string, delay int) {
		t.Helper()
		payload, _ := json.Marshal(map[string]any{
			"source": "wy", "action": "musicUrl",
			"info": map[string]any{"type": "320k", "delay": delay, "musicInfo": map[string]any{"id": id}},
		})
		message, _ := json.Marshal(lxSessionWorkerInput{Operation: "request", RequestKey: key, Payload: payload})
		if _, err := inputWriter.Write(append(message, '\n')); err != nil { t.Fatal(err) }
	}
	sendRequest("request_slow", "slow", 35)
	sendRequest("request_fast", "fast", 5)

	results := make(map[string]string)
	for range 2 {
		line, err := reader.ReadBytes('\n')
		if err != nil { t.Fatal(err) }
		var response lxSessionWorkerOutput
		if err := json.Unmarshal(line, &response); err != nil { t.Fatal(err) }
		if response.Type != "response" || response.Error != "" { t.Fatalf("unexpected worker response: %#v", response) }
		var value struct { Data struct { URL string `json:"url"` } `json:"data"` }
		if err := json.Unmarshal(response.Value, &value); err != nil { t.Fatal(err) }
		results[response.RequestKey] = value.Data.URL
	}
	if results["request_slow"] != "https://cdn.example.test/slow-2.mp3" ||
		results["request_fast"] != "https://cdn.example.test/fast-1.mp3" {
		t.Fatalf("requestKey responses were not correlated or JS state was not retained: %#v", results)
	}

	closeMessage, _ := json.Marshal(lxSessionWorkerInput{Operation: "close"})
	_, _ = inputWriter.Write(append(closeMessage, '\n'))
	_ = inputWriter.Close()
	select {
	case code := <-workerDone:
		if code != 0 { t.Fatalf("worker exit code = %d", code) }
	case <-time.After(3 * time.Second):
		t.Fatal("LX session worker did not shut down")
	}
	_ = outputReader.Close()
	_ = outputWriter.Close()
	if strings.Contains(results["request_slow"], "fast") { t.Fatal("request responses crossed") }
}


type lxTestWriteCloser struct {
	writes int
}

func (w *lxTestWriteCloser) Write(value []byte) (int, error) {
	w.writes++
	return len(value), nil
}

func (w *lxTestWriteCloser) Close() error { return nil }

func TestLXUserSourceProcessDoesNotDispatchAlreadyCancelledRequest(t *testing.T) {
	writer := &lxTestWriteCloser{}
	process := &LXUserSourceProcess{
		stdin: writer,
		pending: make(map[string]chan lxSessionResponse),
		done: make(chan struct{}),
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := process.Request(ctx, json.RawMessage(`{"source":"wy","action":"musicUrl","info":{"type":"320k"}}`))
	if err == nil || err.Error() != "Cancel request" {
		t.Fatalf("already-cancelled request error = %v", err)
	}
	if writer.writes != 0 {
		t.Fatalf("already-cancelled request wrote %d protocol messages, want none", writer.writes)
	}
	if len(process.pending) != 0 {
		t.Fatalf("already-cancelled request left pending entries: %#v", process.pending)
	}
}


type lxTestProtocolWriter struct {
	writes chan string
}

func (w *lxTestProtocolWriter) Write(value []byte) (int, error) {
	w.writes <- string(value)
	return len(value), nil
}

func (w *lxTestProtocolWriter) Close() error { return nil }

func TestLXUserSourceProcessMapsDeadlineToRequestTimeout(t *testing.T) {
	writer := &lxTestWriteCloser{}
	process := &LXUserSourceProcess{
		stdin: writer,
		pending: make(map[string]chan lxSessionResponse),
		done: make(chan struct{}),
	}
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()

	_, err := process.Request(ctx, json.RawMessage(`{"source":"wy","action":"musicUrl","info":{"type":"320k"}}`))
	if err == nil || err.Error() != "Request timeout" {
		t.Fatalf("expired request error = %v, want Request timeout", err)
	}
	if writer.writes != 0 {
		t.Fatalf("expired request wrote %d protocol messages, want none", writer.writes)
	}
}

func TestLXUserSourceProcessSendsCancelAndReportsRequestTimeout(t *testing.T) {
	writer := &lxTestProtocolWriter{writes: make(chan string, 2)}
	process := &LXUserSourceProcess{
		stdin: writer,
		pending: make(map[string]chan lxSessionResponse),
		done: make(chan struct{}),
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := process.Request(ctx, json.RawMessage(`{"source":"wy","action":"musicUrl","info":{"type":"320k"}}`))
		result <- err
	}()

	first := <-writer.writes
	var request struct {
		Op string `json:"operation"`
		RequestKey string `json:"requestKey"`
	}
	if err := json.Unmarshal([]byte(first), &request); err != nil { t.Fatal(err) }
	if request.Op != "request" { t.Fatalf("first protocol op = %q, want request", request.Op) }

	second := <-writer.writes
	var cancellation struct {
		Op string `json:"operation"`
		RequestKey string `json:"requestKey"`
	}
	if err := json.Unmarshal([]byte(second), &cancellation); err != nil { t.Fatal(err) }
	if cancellation.Op != "cancel" || cancellation.RequestKey != request.RequestKey {
		t.Fatalf("cancellation message = %#v, want cancel for %q", cancellation, request.RequestKey)
	}
	if err := <-result; err == nil || err.Error() != "Request timeout" {
		t.Fatalf("deadline result error = %v, want Request timeout", err)
	}
}
