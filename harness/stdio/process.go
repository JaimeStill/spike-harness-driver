// Package stdio drives a harness that speaks a line protocol over its standard input and
// output, such as Pi's RPC mode, Claude Code's stream-json, or an ACP agent. A Process runs the
// harness and moves lines; a Client adds a Codec, correlates commands with responses, answers
// the harness's own requests through a Handler, and streams the harness's normalized events.
// An adapter builds its harness.Connection on a Client.
//
// See docs/design.md#the-line-protocol
package stdio

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/JaimeStill/spike-harness-driver/harness"
)

// Spec says how to start a harness process. On Unix the harness runs in a process group of its
// own, and whatever is left of the group is killed once the harness exits.
type Spec struct {
	Name string
	Args []string
	// Dir is the working directory. Empty means the current directory.
	Dir string
	// Env adds variables to the inherited environment.
	Env []string
	// WaitDelay is the grace period after Close, or after the harness exits: a harness still
	// running is then killed, and pipes that a process outside its group still holds are
	// closed. Zero means five seconds.
	WaitDelay time.Duration
}

const stderrTail = 64 << 10

// Process is a running harness. Its lifetime is a context the process owns: Close cancels it,
// and exec turns the cancellation into the harness's shutdown.
type Process struct {
	name      string
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	stderr    *tail
	waitDelay time.Duration
	ctx       context.Context
	cancel    context.CancelCauseFunc

	writeMu    sync.Mutex
	lines      chan []byte
	stdoutDone chan struct{}
	stderrDone chan struct{}
	waited     chan struct{}
	done       chan struct{}
	exitErr    error
	cause      error
}

// Start starts the process. It takes no context: like a network connection, the process lives
// until Close or until it exits on its own, however long its start-up took.
func Start(spec Spec) (*Process, error) {
	ctx, cancel := context.WithCancelCause(context.Background())
	p := &Process{
		name:       spec.Name,
		stderr:     &tail{max: stderrTail},
		waitDelay:  spec.WaitDelay,
		ctx:        ctx,
		cancel:     cancel,
		lines:      make(chan []byte),
		stdoutDone: make(chan struct{}),
		stderrDone: make(chan struct{}),
		waited:     make(chan struct{}),
		done:       make(chan struct{}),
	}
	if p.waitDelay == 0 {
		p.waitDelay = 5 * time.Second
	}
	cmd := exec.CommandContext(ctx, spec.Name, spec.Args...)
	cmd.Dir = spec.Dir
	cmd.Env = append(os.Environ(), spec.Env...)
	cmd.WaitDelay = p.waitDelay
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel(err)
		return nil, fmt.Errorf("%s: stdin: %w", spec.Name, err)
	}
	cmd.Cancel = func() error {
		_ = stdin.Close()
		return os.ErrProcessDone
	}
	stdout, stdoutW, err := os.Pipe()
	if err != nil {
		cancel(err)
		return nil, fmt.Errorf("%s: stdout: %w", spec.Name, err)
	}
	stderr, stderrW, err := os.Pipe()
	if err != nil {
		_, _ = stdout.Close(), stdoutW.Close()
		cancel(err)
		return nil, fmt.Errorf("%s: stderr: %w", spec.Name, err)
	}
	cmd.Stdout, cmd.Stderr = stdoutW, stderrW
	ownGroup(cmd)
	p.cmd, p.stdin = cmd, stdin
	err = cmd.Start()
	_, _ = stdoutW.Close(), stderrW.Close()
	if err != nil {
		_, _ = stdout.Close(), stderr.Close()
		cancel(err)
		return nil, fmt.Errorf("start %s: %w", spec.Name, err)
	}
	go p.readStderr(stderr)
	go p.read(stdout)
	go p.wait(stdout, stderr)
	return p, nil
}

// Lines yields each stdout line without its line ending, and closes once the process has
// exited. The reader waits for each line to be taken, so the process sees backpressure.
func (p *Process) Lines() <-chan []byte { return p.lines }

// Err returns the process's exit error once Lines has closed: nil for a successful exit, and
// otherwise an error that carries the end of the process's stderr.
func (p *Process) Err() error {
	<-p.done
	return p.exitErr
}

// Cause reports why the process was asked to stop, as it stood when the process exited:
// harness.ErrClosed after Close, or the error that failed its output. It is nil if the process
// was never asked before it exited, so an exit with a nil Cause was the harness's own. It
// waits for the process to exit.
func (p *Process) Cause() error {
	<-p.done
	return p.cause
}

// WriteLine writes line and a line feed to the process's stdin.
func (p *Process) WriteLine(line []byte) error {
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	if _, err := p.stdin.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("%s: write: %w", p.name, err)
	}
	return nil
}

// Close asks the harness to exit, waits for it, and returns its exit error. A harness that
// hasn't exited within WaitDelay is killed.
func (p *Process) Close() error {
	p.cancel(harness.ErrClosed)
	<-p.done
	return p.exitErr
}

func (p *Process) wait(stdout, stderr *os.File) {
	err := p.cmd.Wait()
	p.cause = context.Cause(p.ctx)
	killGroup(p.cmd)
	grace := time.AfterFunc(p.waitDelay, func() {
		_, _ = stdout.Close(), stderr.Close()
	})
	defer grace.Stop()
	<-p.stderrDone
	if err != nil {
		p.exitErr = fmt.Errorf("%s: exited: %w: %s", p.name, err, p.stderr)
	}
	close(p.waited)
	<-p.stdoutDone
}

func (p *Process) readStderr(stderr *os.File) {
	defer close(p.stderrDone)
	defer func() { _ = stderr.Close() }()
	_, _ = io.Copy(p.stderr, stderr)
}

func (p *Process) read(stdout *os.File) {
	br := bufio.NewReader(stdout)
	for {
		line, err := br.ReadBytes('\n')
		if line = bytes.TrimRight(line, "\r\n"); len(line) > 0 {
			p.lines <- line
		}
		if err != nil {
			if !errors.Is(err, io.EOF) && !errors.Is(err, os.ErrClosed) {
				p.cancel(fmt.Errorf("%s: read: %w", p.name, err))
			}
			break
		}
	}
	_ = stdout.Close()
	close(p.stdoutDone)
	<-p.waited
	close(p.lines)
	close(p.done)
}

type tail struct {
	mu  sync.Mutex
	buf []byte
	max int
}

func (t *tail) Write(b []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, b...)
	if over := len(t.buf) - t.max; over > 0 {
		t.buf = append(t.buf[:0], t.buf[over:]...)
	}
	return len(b), nil
}

func (t *tail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(bytes.TrimSpace(t.buf))
}
