package music

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestRunLXSourceWorkerInitializesDeclaredPlatforms(t *testing.T) {
	script := `/*
 * @name Runtime Test
 */
lx.send(lx.EVENT_NAMES.inited, {
  sources: {
    wy: { name: "网易云音乐", actions: ["musicUrl"], qualitys: ["standard"] },
    tx: { name: "QQ音乐", actions: ["musicUrl"], qualitys: ["standard"] }
  }
});
lx.on(lx.EVENT_NAMES.request, function (request) { return request; });
`
	input, err := json.Marshal(lxWorkerRequest{Script: script})
	if err != nil { t.Fatal(err) }
	var output bytes.Buffer
	if code := RunLXSourceWorker(bytes.NewReader(input), &output); code != 0 {
		t.Fatalf("worker returned %d: %s", code, output.String())
	}
	var response lxWorkerResponse
	if err := json.Unmarshal(output.Bytes(), &response); err != nil { t.Fatal(err) }
	if response.Error != "" { t.Fatal(response.Error) }
	if !response.Result.Initialized { t.Fatal("source was not initialized") }
	if strings.Join(response.Result.Platforms, ",") != "tx,wy" {
		t.Fatalf("unexpected platforms: %#v", response.Result.Platforms)
	}
	if len(response.Result.Handlers) != 1 || response.Result.Handlers[0] != "request" {
		t.Fatalf("unexpected handlers: %#v", response.Result.Handlers)
	}
}

func TestRunLXSourceWorkerRejectsMissingHandshake(t *testing.T) {
	input := strings.NewReader(`{"script":"var source = 1;" }`)
	var output bytes.Buffer
	if code := RunLXSourceWorker(input, &output); code == 0 {
		t.Fatal("expected worker to reject script without inited event")
	}
	var response lxWorkerResponse
	if err := json.Unmarshal(output.Bytes(), &response); err != nil { t.Fatal(err) }
	if !strings.Contains(response.Error, "inited") {
		t.Fatalf("unexpected error: %q", response.Error)
	}
}

func TestRunLXSourceWorkerRejectsUndeclaredPlatforms(t *testing.T) {
	script := `lx.send(lx.EVENT_NAMES.inited, { sources: {} });`
	input, _ := json.Marshal(lxWorkerRequest{Script: script})
	var output bytes.Buffer
	_ = RunLXSourceWorker(bytes.NewReader(input), &output)
	var response lxWorkerResponse
	if err := json.Unmarshal(output.Bytes(), &response); err != nil { t.Fatal(err) }
	if !strings.Contains(response.Error, "没有声明 sources") {
		t.Fatalf("unexpected error: %q", response.Error)
	}
}
