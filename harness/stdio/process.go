// Package stdio drives a harness that speaks a line protocol over its standard input and
// output, such as Pi's RPC mode, Claude Code's stream-json, or an ACP agent. A Process runs the
// harness and moves lines; a Client adds a Codec, correlates requests with responses, and
// streams the harness's normalized events. An adapter builds its harness.Connection on a Client.
package stdio

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"
)

// Spec says how to start a harness process.
type Spec struct {
	Name string
	Args []string
	// Dir is the working directory. Empty means the current directory.
	Dir string
	// Env adds variables to the inherited environment.
	Env []string
	// CloseTimeout bounds how long Close waits for the process to exit before killing it.
	// Zero means five seconds.
	CloseTimeout time.Duration
}

// Process is a running harness. One goroutine reads its stdout, and it is the only caller of
// cmd.Wait.
type Process struct {
	name         string
	cmd          *exec.Cmd
	stdin        io.WriteCloser
	stderr       bytes.Buffer // written by cmd, read only after cmd.Wait
	closeTimeout time.Duration

	writeMu sync.Mutex
	lines   chan []byte
	done    chan struct{} // closed once the process has exited
	exitErr error

	closeOnce sync.Once
	closeErr  error
}

// Start starts the process. It takes no context: the process lives until Close or until it
// exits on its own.
func Start(spec Spec) (*Process, error) {
	cmd := exec.Command(spec.Name, spec.Args...)
	cmd.Dir = spec.Dir
	cmd.Env = append(os.Environ(), spec.Env...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("%s: stdin: %w", spec.Name, err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("%s: stdout: %w", spec.Name, err)
	}
	p := &Process{
		name:         spec.Name,
		cmd:          cmd,
		stdin:        stdin,
		closeTimeout: spec.CloseTimeout,
		lines:        make(chan []byte),
		done:         make(chan struct{}),
	}
	if p.closeTimeout == 0 {
		p.closeTimeout = 5 * time.Second
	}
	cmd.Stderr = &p.stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", spec.Name, err)
	}
	go p.read(stdout)
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

// WriteLine writes line and a line feed to the process's stdin.
func (p *Process) WriteLine(line []byte) error {
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	if _, err := p.stdin.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("%s: write: %w", p.name, err)
	}
	return nil
}

// Close closes the process's stdin, which a line-protocol harness takes as the signal to shut
// down, and kills the process if it hasn't exited within the close timeout.
func (p *Process) Close() error {
	p.closeOnce.Do(func() {
		_ = p.stdin.Close()
		select {
		case <-p.done:
			p.closeErr = p.exitErr
		case <-time.After(p.closeTimeout):
			_ = p.cmd.Process.Kill()
			<-p.done
			p.closeErr = fmt.Errorf("%s: killed after %s: %w", p.name, p.closeTimeout, p.exitErr)
		}
	})
	return p.closeErr
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
				_ = p.cmd.Process.Kill()
			}
			break
		}
	}
	if err := p.cmd.Wait(); err != nil {
		p.exitErr = fmt.Errorf("%s: exited: %w: %s", p.name, err, bytes.TrimSpace(p.stderr.Bytes()))
	}
	close(p.lines)
	close(p.done)
}
