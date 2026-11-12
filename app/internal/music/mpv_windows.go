//go:build windows

package music

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

var mpvInstanceSequence atomic.Uint64


type MpvEngine struct {
	lifecycleMu sync.Mutex
	mu sync.RWMutex
	commandMu sync.Mutex
	executable string
	pipePath string
	command *exec.Cmd
	requestID atomic.Uint64
	done chan error
	track *Track
	loadStartedAt time.Time
	volume int
	loudnessNormalization bool
	audioOutput string // Optional mpv audio output override, used by deterministic integration tests.
	stderrTail string
}

func NewMpvEngine(executable string) *MpvEngine {
	return &MpvEngine{executable: strings.TrimSpace(executable), volume: 80}
}


func (e *MpvEngine) Validate() error {
	if e == nil { return ErrPlaybackEngineUnavailable }
	e.mu.RLock()
	configured := e.executable
	e.mu.RUnlock()
	resolved, err := resolveMpvExecutable(configured)
	if err != nil { return err }

	// Starting mpv with --version verifies that the executable can actually
	// load its runtime dependencies (including any required DLLs).
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, resolved, "--no-config", "--version").CombinedOutput()
	if err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("mpv runtime check timed out: %w", ctx.Err())
		}
		detail := strings.TrimSpace(string(output))
		if detail == "" { detail = err.Error() }
		return fmt.Errorf("mpv runtime is incomplete or cannot start: %s", detail)
	}
	if !strings.Contains(strings.ToLower(string(output)), "mpv") {
		return fmt.Errorf("mpv runtime check returned unexpected output: %s", strings.TrimSpace(string(output)))
	}

	e.mu.Lock()
	e.executable = resolved
	e.mu.Unlock()
	return nil
}

func (e *MpvEngine) Start(ctx context.Context) error {
	if e == nil { return ErrPlaybackEngineUnavailable }
	if ctx == nil { return errors.New("mpv startup context is required") }
	if err := e.Validate(); err != nil { return err }
	e.lifecycleMu.Lock()
	defer e.lifecycleMu.Unlock()
	e.mu.Lock()
	if e.command != nil && e.command.Process != nil {
		e.mu.Unlock()
		return nil
	}
	executable := e.executable
	e.mu.Unlock()


	instanceID := mpvInstanceSequence.Add(1)
	pipePath := fmt.Sprintf("\\\\.\\pipe\\my-livehub-mpv-%d-%d", os.Getpid(), instanceID)
	args := buildMpvArgs(pipePath)
	e.mu.RLock()
	audioOutput := strings.TrimSpace(e.audioOutput)
	e.mu.RUnlock()
	if audioOutput != "" {
		args = append(args, "--ao="+audioOutput)
	}
	cmd := exec.Command(executable, args...)
	cmd.Stdout = io.Discard
	stderr, err := cmd.StderrPipe()
	if err != nil { return fmt.Errorf("capture mpv diagnostics: %w", err) }
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow:true, CreationFlags:0x08000000}
	if err := cmd.Start(); err != nil { return fmt.Errorf("start mpv: %w", err) }
	go e.captureStderr(stderr)
	done := make(chan error, 1)
	go func() {
		err := cmd.Wait()
		done <- err
		close(done)
		e.mu.Lock()
		if e.command == cmd {
			e.command = nil
			e.pipePath = ""
				e.track = nil
			e.loadStartedAt = time.Time{}
		}
		e.mu.Unlock()
	}()

	e.mu.Lock()
	e.executable, e.pipePath, e.command, e.done = executable, pipePath, cmd, done
	e.stderrTail = ""
	e.mu.Unlock()

	deadline := time.NewTimer(5*time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(50*time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			_ = e.closeLocked()
			return ctx.Err()
		case <-deadline.C:
			_ = e.closeLocked()
			return errors.New("mpv IPC did not become ready within 5 seconds")
		case waitErr := <-done:
				e.mu.Lock(); e.command = nil; e.pipePath = ""; e.mu.Unlock()
				return fmt.Errorf("mpv exited before IPC became ready: %v", waitErr)
		case <-ticker.C:
			if pipe, err := os.OpenFile(pipePath, os.O_RDWR, 0); err == nil {
				_ = pipe.Close()
				e.mu.RLock()
				volume := e.volume
				normalize := e.loudnessNormalization
				e.mu.RUnlock()
				if err := e.SetVolume(ctx, volume); err != nil { return err }
				if normalize { return e.SetLoudnessNormalization(ctx, true) }
				return nil
			}
		}
	}
}


func buildMpvArgs(pipePath string) []string {
	return []string{
		"--no-config", "--terminal=yes", "--idle=yes", "--keep-open=no",
		"--msg-level=all=warn", "--input-ipc-server=" + pipePath,
		"--no-video", "--force-window=no",
	}
}

func (e *MpvEngine) captureStderr(reader io.ReadCloser) {
	defer reader.Close()
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), 64<<10)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" { continue }
		e.mu.Lock()
		if e.stderrTail != "" { e.stderrTail += "\n" }
		e.stderrTail += line
		if len(e.stderrTail) > 8192 { e.stderrTail = e.stderrTail[len(e.stderrTail)-8192:] }
		e.mu.Unlock()
	}
}

// openMpvIPC retries the transient ERROR_PIPE_BUSY condition returned by
// Windows named pipes while mpv is handing off a just-closed client instance.
func openMpvIPC(ctx context.Context, pipePath string) (*os.File, error) {
	const errorPipeBusy syscall.Errno = 231
	ticker := time.NewTicker(15 * time.Millisecond)
	defer ticker.Stop()
	for {
		conn, err := os.OpenFile(pipePath, os.O_RDWR, 0)
		if err == nil {
			return conn, nil
		}
		if !errors.Is(err, errorPipeBusy) {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

func (e *MpvEngine) commandIPC(ctx context.Context, args ...any) (json.RawMessage, error) {
	if e == nil { return nil, ErrPlaybackEngineUnavailable }
	if ctx == nil { return nil, errors.New("mpv command context is required") }
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	e.commandMu.Lock()
	defer e.commandMu.Unlock()
	e.mu.RLock()
	pipePath := e.pipePath
	e.mu.RUnlock()
	if pipePath == "" { return nil, ErrPlaybackEngineUnavailable }

	conn, err := openMpvIPC(ctx, pipePath)
	if err != nil { return nil, fmt.Errorf("connect to mpv IPC: %w", err) }
	defer conn.Close()
	requestID := e.requestID.Add(1)
	request := map[string]any{"command":args, "request_id":requestID}
	if err := json.NewEncoder(conn).Encode(request); err != nil { return nil, fmt.Errorf("send mpv command: %w", err) }

	// mpv sends asynchronous event objects and command responses over the
	// same JSON stream. Responses are top-level objects (not nested under a
	// "response" field), so ignore events until this request ID is received.
	type commandResponse struct {
		RequestID uint64          `json:"request_id"`
		Error     string          `json:"error"`
		Data      json.RawMessage `json:"data"`
		ReadErr   error
	}
	result := make(chan commandResponse, 1)
	go func() {
		decoder := json.NewDecoder(bufio.NewReader(conn))
		for {
			var item commandResponse
			if err := decoder.Decode(&item); err != nil {
				result <- commandResponse{ReadErr:err}
				return
			}
			if item.RequestID == requestID {
				result <- item
				return
			}
		}
	}()
	select {
	case <-ctx.Done():
		_ = conn.Close()
		return nil, ctx.Err()
	case item := <-result:
		if item.ReadErr != nil { return nil, fmt.Errorf("read mpv response: %w", item.ReadErr) }
		if item.Error != "success" { return nil, fmt.Errorf("mpv command failed: %s", item.Error) }
		return item.Data, nil
	}
}

func (e *MpvEngine) Load(ctx context.Context, resource PlaybackResource, track Track) error {
	if err := validateTrack(track); err != nil { return err }
	parsed, err := url.Parse(resource.URL)
	if err != nil || parsed.Scheme == "" || (parsed.Scheme != "http" && parsed.Scheme != "https" && parsed.Scheme != "file") {
		return errors.New("playback resource must use http, https, or file URL")
	}
	if resource.URL == "" { return errors.New("playback resource URL is required") }
	if len(resource.Headers) > 0 {
		headers := make([]string, 0, len(resource.Headers))
		for key, value := range resource.Headers {
			if strings.ContainsAny(key+value, "\r\n") { return errors.New("playback headers cannot contain line breaks") }
			headers = append(headers, key+": "+value)
		}
		if _, err := e.commandIPC(ctx, "set_property", "http-header-fields", headers); err != nil { return err }
	} else {
		_, _ = e.commandIPC(ctx, "set_property", "http-header-fields", []string{})
	}
	e.mu.Lock()
	e.stderrTail = ""
	e.mu.Unlock()
	if _, err := e.commandIPC(ctx, "loadfile", resource.URL, "replace"); err != nil { return err }
	copy := track
	e.mu.Lock()
	e.track = &copy
	e.loadStartedAt = time.Now()
	e.mu.Unlock()
	return nil
}

// AddSubtitle loads a local external subtitle track into the dedicated mpv process.
func (e *MpvEngine) AddSubtitle(ctx context.Context, path string) error {
	if strings.TrimSpace(path) == "" { return errors.New("subtitle path is required") }
	_, err := e.commandIPC(ctx, "sub-add", path, "select")
	return err
}

func (e *MpvEngine) Pause(ctx context.Context) error {
	_, err := e.commandIPC(ctx, "set_property", "pause", true)
	return err
}
func (e *MpvEngine) Resume(ctx context.Context) error {
	_, err := e.commandIPC(ctx, "set_property", "pause", false)
	return err
}
func (e *MpvEngine) Seek(ctx context.Context, positionMS int64) error {
	if positionMS < 0 { return errors.New("seek position cannot be negative") }
	_, err := e.commandIPC(ctx, "seek", float64(positionMS)/1000, "absolute+exact")
	return err
}
func (e *MpvEngine) SetVolume(ctx context.Context, volume int) error {
	if e == nil { return ErrPlaybackEngineUnavailable }
	if volume < 0 || volume > 100 { return errors.New("volume must be between 0 and 100") }
	e.mu.RLock()
	started := e.pipePath != ""
	e.mu.RUnlock()
	if started {
		if _, err := e.commandIPC(ctx, "set_property", "volume", volume); err != nil { return err }
	}
	e.mu.Lock()
	e.volume = volume
	e.mu.Unlock()
	return nil
}
const mpvLoudnessNormalizationFilter = "lavfi=[loudnorm=I=-16:TP=-1.5:LRA=11:linear=false,aresample=48000]"

// SetLoudnessNormalization applies EBU R128 single-pass dynamic loudness
// normalization. The setting is retained while mpv is stopped and applied
// when the engine starts.
func (e *MpvEngine) SetLoudnessNormalization(ctx context.Context, enabled bool) error {
	if e == nil { return ErrPlaybackEngineUnavailable }
	if ctx == nil { return errors.New("loudness normalization context is required") }
	e.mu.RLock()
	started := e.pipePath != ""
	e.mu.RUnlock()
	if started {
		var err error
		if enabled {
			_, err = e.commandIPC(ctx, "af", "set", mpvLoudnessNormalizationFilter)
		} else {
			_, err = e.commandIPC(ctx, "af", "clr", "")
		}
		if err != nil {
			return fmt.Errorf("apply mpv loudness normalization: %w", err)
		}
	}
	e.mu.Lock()
	e.loudnessNormalization = enabled
	e.mu.Unlock()
	return nil
}

func (e *MpvEngine) Stop(ctx context.Context) error {
	_, err := e.commandIPC(ctx, "stop")
	e.mu.Lock()
	e.track = nil
	e.loadStartedAt = time.Time{}
	e.mu.Unlock()
	return err
}
func (e *MpvEngine) Snapshot(ctx context.Context) (PlaybackSnapshot, error) {
	if e == nil { return PlaybackSnapshot{}, ErrPlaybackEngineUnavailable }
	e.mu.RLock()
	pipePath, fallbackVolume := e.pipePath, e.volume
	e.mu.RUnlock()
	if pipePath == "" { return PlaybackSnapshot{State:PlaybackIdle, Volume:fallbackVolume}, nil }
	snapshot := PlaybackSnapshot{State:PlaybackIdle}
	readNumber := func(property string) int64 {
		data, err := e.commandIPC(ctx, "get_property", property)
		if err != nil { return 0 }
		var value float64
		if json.Unmarshal(data, &value) != nil { return 0 }
		return int64(value*1000)
	}
	position := readNumber("time-pos")
	duration := readNumber("duration")
	pauseData, pauseErr := e.commandIPC(ctx, "get_property", "pause")
	idleData, idleErr := e.commandIPC(ctx, "get_property", "idle-active")
	var paused, idle bool
	if pauseErr == nil { _ = json.Unmarshal(pauseData, &paused) }
	if idleErr == nil { _ = json.Unmarshal(idleData, &idle) }
	volumeData, volumeErr := e.commandIPC(ctx, "get_property", "volume")
	volume := float64(fallbackVolume)
	if volumeErr == nil { _ = json.Unmarshal(volumeData, &volume) }
	e.mu.RLock()
	if e.track != nil { copy := *e.track; snapshot.Track = &copy }
	e.mu.RUnlock()
	snapshot.PositionMS, snapshot.DurationMS, snapshot.Volume = position, duration, int(volume)
	e.mu.RLock()
	loadStartedAt := e.loadStartedAt
	e.mu.RUnlock()
	loadExpired := !loadStartedAt.IsZero() && time.Since(loadStartedAt) >= 30*time.Second
	// mpv can briefly report idle-active=false while an asynchronous load is
	// still failing. Do not mistake that transient state for successful
	// playback: an expired load with neither duration nor playback position
	// is a terminal load failure. A live stream with unknown duration is still
	// accepted once its playback clock starts advancing.
	loadFailed := snapshot.Track != nil && loadExpired && duration == 0 && position == 0
	if loadFailed {
		snapshot.State = PlaybackError
		e.mu.RLock()
		detail := strings.TrimSpace(e.stderrTail)
		e.mu.RUnlock()
		snapshot.Error = "mpv 未能加载该音频资源"
		if detail != "" { snapshot.Error += "：" + detail }
	} else if snapshot.Track != nil && !idle {
		snapshot.State = PlaybackPlaying
		if paused { snapshot.State = PlaybackPaused }
	} else if snapshot.Track != nil && !loadExpired {
		snapshot.State = PlaybackLoading
	} else if snapshot.Track != nil {
		snapshot.State = PlaybackError
		e.mu.RLock()
		detail := strings.TrimSpace(e.stderrTail)
		e.mu.RUnlock()
		snapshot.Error = "mpv 未能加载该音频资源"
		if detail != "" { snapshot.Error += "：" + detail }
	}
	return snapshot, nil
}

func (e *MpvEngine) Close() error {
	if e == nil { return nil }
	e.lifecycleMu.Lock()
	defer e.lifecycleMu.Unlock()
	return e.closeLocked()
}

func (e *MpvEngine) closeLocked() error {
	e.mu.Lock()
	cmd := e.command
	done := e.done
	e.command = nil
	e.done = nil
	e.pipePath = ""
	e.track = nil
	e.loadStartedAt = time.Time{}
	e.mu.Unlock()
	if cmd == nil || cmd.Process == nil { return nil }
	_ = cmd.Process.Kill()
	if done != nil {
		if err := <-done; err != nil {
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) { return err }
		}
	}
	return nil
}

var _ PlaybackEngine = (*MpvEngine)(nil)
