package pi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"

	"github.com/JaimeStill/spike-harness-driver/harness"
)

// rpcArgs start Pi in RPC mode with an in-memory session, and without the user's extensions,
// skills, and context files, which would otherwise change the prompt or send extension UI
// requests.
var rpcArgs = []string{"--mode", "rpc", "--no-session", "-ne", "-ns", "-nc"}

// Driver starts one Pi process per session.
type Driver struct {
	// Command is the Pi executable. Empty means "pi" on the PATH.
	Command string
	// Env adds variables to Pi's environment, such as LLAMA_BASE_URL.
	Env []string
	// CloseTimeout bounds how long Close waits for Pi to exit before killing it. Zero means
	// five seconds.
	CloseTimeout time.Duration
}

var _ harness.Driver = Driver{}

// Open starts Pi, selects the model when opts names one, and reads Pi's session ID. ctx bounds
// the start-up handshake only; the session lives until Close.
func (d Driver) Open(ctx context.Context, opts harness.Options) (harness.Session, error) {
	name := d.Command
	if name == "" {
		name = "pi"
	}
	// The process outlives ctx, so it is not started with exec.CommandContext.
	cmd := exec.Command(name, rpcArgs...)
	cmd.Dir = opts.Dir
	cmd.Env = append(os.Environ(), d.Env...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("pi: stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("pi: stdout: %w", err)
	}
	s := &session{
		cmd:          cmd,
		stdin:        stdin,
		pending:      map[string]chan record{},
		done:         make(chan struct{}),
		closed:       make(chan struct{}),
		closeTimeout: d.CloseTimeout,
	}
	if s.closeTimeout == 0 {
		s.closeTimeout = 5 * time.Second
	}
	cmd.Stderr = &s.stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("pi: start %s: %w", name, err)
	}
	go s.read(stdout)

	if opts.Model != "" {
		cmd := command{Type: "set_model", Provider: opts.Provider, ModelID: opts.Model}
		if _, err := s.call(ctx, cmd); err != nil {
			return nil, closeAfter(s, err)
		}
	}
	r, err := s.call(ctx, command{Type: "get_state"})
	if err != nil {
		return nil, closeAfter(s, err)
	}
	var st state
	if err := json.Unmarshal(r.Data, &st); err != nil {
		return nil, closeAfter(s, fmt.Errorf("pi: get_state: %w", err))
	}
	s.id = st.SessionID
	return s, nil
}

// closeAfter stops a session that failed to open and returns the error that failed it.
func closeAfter(s *session, err error) error {
	_ = s.Close()
	return err
}

// read is the session's only reader of Pi's stdout, and the only caller of cmd.Wait. It routes
// responses to their callers and events to the open exchange, and when Pi exits it fails
// whatever is still waiting.
func (s *session) read(stdout io.Reader) {
	br := bufio.NewReader(stdout)
	for {
		// Split on LF only; a JSON string may hold U+2028, and a line may exceed any fixed
		// token size.
		line, err := br.ReadBytes('\n')
		if line = bytes.TrimRight(line, "\r\n"); len(line) > 0 {
			s.route(line)
		}
		if err != nil {
			if err != io.EOF {
				_ = s.cmd.Process.Kill()
			}
			break
		}
	}

	exitErr := fmt.Errorf("pi: exited")
	if err := s.cmd.Wait(); err != nil {
		exitErr = fmt.Errorf("pi: exited: %w: %s", err, bytes.TrimSpace(s.stderr.Bytes()))
	}
	s.mu.Lock()
	s.exited = true
	s.exitErr = exitErr
	for id, ch := range s.pending {
		close(ch)
		delete(s.pending, id)
	}
	x := s.active
	s.active = nil
	s.mu.Unlock()
	if x != nil {
		x.push(harness.Event{Kind: harness.KindError, Err: exitErr.Error()})
		x.push(harness.Event{Kind: harness.KindEnded})
	}
	close(s.done)
}

func (s *session) route(line []byte) {
	r, err := decode(line)
	if err != nil {
		s.toActive(harness.Event{Kind: harness.KindError, Err: err.Error(), Raw: line})
		return
	}
	if r.Type == "response" {
		s.mu.Lock()
		ch, ok := s.pending[r.ID]
		delete(s.pending, r.ID)
		s.mu.Unlock()
		if ok {
			ch <- r
		}
		return
	}
	for _, ev := range normalize(r, line) {
		s.toActive(ev)
	}
}

// toActive hands an event to the open exchange. Pi's events carry no request ID, so the open
// exchange is the only place an event can belong; an event outside any exchange, such as a
// queue_update after a cancelled run settles, is dropped.
func (s *session) toActive(ev harness.Event) {
	s.mu.Lock()
	x := s.active
	if ev.Kind == harness.KindEnded && x != nil {
		s.active = nil
	}
	s.mu.Unlock()
	if x != nil {
		x.push(ev)
	}
}
