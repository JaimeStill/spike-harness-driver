// Package stdio drives a harness that speaks a line protocol over its standard input and
// output, such as Pi's RPC mode, Claude Code's stream-json, or an ACP agent. A Process runs the
// harness and moves lines; a Client adds a Codec, correlates requests with responses, and
// streams the harness's normalized events. An adapter builds its harness.Connection on a Client.
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
	// running is then killed, and pipes a child of the harness still holds are closed. Zero
	// means five seconds.
	WaitDelay time.Duration
}

// Process is a running harness. Its lifetime is a context the process owns: Close cancels it,
// and exec turns the cancellation into the harness's shutdown.
type Process struct {
	name   string
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stderr bytes.Buffer // written by cmd, read only after cmd.Wait
	ctx    context.Context
	cancel context.CancelCauseFunc

	writeMu sync.Mutex
	lines   chan []byte
	waited  chan struct{} // closed once cmd.Wait has returned
	done    chan struct{} // closed once the process has exited and Lines has closed
	exitErr error
}

// Start starts the process. It takes no context: like a network connection, the process lives
// until Close or until it exits on its own, however long its start-up took.
func Start(spec Spec) (*Process, error) {
	ctx, cancel := context.WithCancelCause(context.Background())
	p := &Process{
		name:   spec.Name,
		ctx:    ctx,
		cancel: cancel,
		lines:  make(chan []byte),
		waited: make(chan struct{}),
		done:   make(chan struct{}),
	}
	cmd := exec.CommandContext(ctx, spec.Name, spec.Args...)
	cmd.Dir = spec.Dir
	cmd.Env = append(os.Environ(), spec.Env...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel(err)
		return nil, fmt.Errorf("%s: stdin: %w", spec.Name, err)
	}
	// A line-protocol harness shuts down at the end of its input, so cancelling closes stdin
	// rather than killing. ErrProcessDone has Wait report the exit status as it is, so a clean
	// exit after Close is no error.
	cmd.Cancel = func() error {
		_ = stdin.Close()
		return os.ErrProcessDone
	}
	cmd.WaitDelay = spec.WaitDelay
	if cmd.WaitDelay == 0 {
		cmd.WaitDelay = 5 * time.Second
	}
	// Stdout goes through an io.Pipe, not StdoutPipe, so that Wait may run beside the reader:
	// otherwise a child of the harness holding stdout open would keep the reader from EOF and
	// Wait, and so WaitDelay, from ever starting.
	pr, pw := io.Pipe()
	cmd.Stdout = pw
	cmd.Stderr = &p.stderr
	ownGroup(cmd)
	p.cmd, p.stdin = cmd, stdin
	if err := cmd.Start(); err != nil {
		cancel(err)
		return nil, fmt.Errorf("start %s: %w", spec.Name, err)
	}
	go p.wait(pw)
	go p.read(pr)
	return p, nil
}

// Lines yields each stdout line without its line ending, and closes once the process has
// exited. The reader waits for each line to be taken, so the process sees backpressure.
func (p *Process) Lines() <-chan []byte { return p.lines }

// Err returns the process's exit error once Lines has closed: nil for a successful exit, and
// otherwise an error that carries the process's stderr.
func (p *Process) Err() error {
	<-p.done
	return p.exitErr
}

// Cause reports why the process was asked to stop: harness.ErrClosed after Close, or the error
// that failed its output. It is nil if the process was never asked, so an exit with a nil Cause
// was the harness's own.
func (p *Process) Cause() error { return context.Cause(p.ctx) }

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

// wait is the only caller of cmd.Wait. Once the harness has exited, it kills the processes the
// harness left behind, and closes stdout to end the reader, even when WaitDelay had to close
// the pipes one of those processes held.
func (p *Process) wait(stdout *io.PipeWriter) {
	if err := p.cmd.Wait(); err != nil {
		p.exitErr = fmt.Errorf("%s: exited: %w: %s", p.name, err, bytes.TrimSpace(p.stderr.Bytes()))
	}
	killGroup(p.cmd)
	close(p.waited)
	_ = stdout.Close()
}

func (p *Process) read(stdout io.Reader) {
	br := bufio.NewReader(stdout)
	for {
		// Split on LF only: a JSON string may hold U+2028, and a line may exceed any fixed
		// token size.
		line, err := br.ReadBytes('\n')
		if line = bytes.TrimRight(line, "\r\n"); len(line) > 0 {
			p.lines <- line
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				p.cancel(fmt.Errorf("%s: read: %w", p.name, err))
			}
			break
		}
	}
	<-p.waited
	close(p.lines)
	close(p.done)
}
