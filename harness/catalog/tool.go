package catalog

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/JaimeStill/spike-harness-driver/harness"
)

const toolFile = "tool.json"

const (
	maxOutput  = 1 << 20
	stderrTail = 4 << 10
	waitDelay  = 2 * time.Second
)

var errOutputTooLarge = errors.New("output exceeds 1 MiB")

type manifest struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
	Command     []string        `json:"command"`
}

// Tools loads every command tool directly under dir, sorted by name. A subdirectory without a
// tool.json isn't a tool and is skipped. Two tools with the same name are an error, because the
// model calls a tool by name.
func Tools(dir string) ([]harness.Tool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("catalog: %w", err)
	}
	var tools []harness.Tool
	where := map[string]string{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		toolDir := filepath.Join(dir, e.Name())
		if _, err := os.Stat(filepath.Join(toolDir, toolFile)); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return nil, fmt.Errorf("catalog: %w", err)
		}
		tool, err := CommandTool(toolDir)
		if err != nil {
			return nil, err
		}
		if prev, ok := where[tool.Name]; ok {
			return nil, fmt.Errorf("catalog: %s and %s both name the tool %q", prev, toolDir, tool.Name)
		}
		where[tool.Name] = toolDir
		tools = append(tools, tool)
	}
	slices.SortFunc(tools, func(a, b harness.Tool) int { return strings.Compare(a.Name, b.Name) })
	return tools, nil
}

// CommandTool loads the command tool whose directory is dir. Its handler runs the manifest's
// command in dir, writes the call's arguments to its stdin, and returns its stdout. A first
// element of the command that holds a path separator names a file relative to dir; a bare one
// is looked up on PATH.
func CommandTool(dir string) (harness.Tool, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return harness.Tool{}, fmt.Errorf("catalog: %w", err)
	}
	file := filepath.Join(dir, toolFile)
	data, err := os.ReadFile(file)
	if err != nil {
		return harness.Tool{}, fmt.Errorf("catalog: %w", err)
	}
	var m manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return harness.Tool{}, fmt.Errorf("catalog: %s: %w", file, err)
	}
	if err := m.validate(); err != nil {
		return harness.Tool{}, fmt.Errorf("catalog: %s: %w", file, err)
	}
	var schema bytes.Buffer
	if err := json.Compact(&schema, m.InputSchema); err != nil {
		return harness.Tool{}, fmt.Errorf("catalog: %s: inputSchema: %w", file, err)
	}
	c := &command{name: m.Name, dir: dir, path: m.Command[0], args: m.Command[1:]}
	if strings.ContainsRune(c.path, '/') || strings.ContainsRune(c.path, filepath.Separator) {
		c.path = filepath.FromSlash(c.path)
		if !filepath.IsAbs(c.path) {
			c.path = filepath.Join(dir, c.path)
		}
	}
	t := harness.Tool{
		Name:        m.Name,
		Description: m.Description,
		Schema:      schema.Bytes(),
		Handler:     c.run,
	}
	if err := t.Validate(); err != nil {
		return harness.Tool{}, fmt.Errorf("catalog: %s: %w", file, err)
	}
	return t, nil
}

func (m *manifest) validate() error {
	switch {
	case strings.TrimSpace(m.Description) == "":
		return errors.New("description is empty")
	case len(m.InputSchema) == 0:
		return errors.New("inputSchema is missing")
	case len(m.Command) == 0 || m.Command[0] == "":
		return errors.New("command is empty")
	}
	if err := harness.ValidateSchema(m.InputSchema); err != nil {
		return fmt.Errorf("inputSchema: %w", err)
	}
	return nil
}

type command struct {
	name string
	dir  string
	path string
	args []string
}

func (c *command) run(ctx context.Context, args json.RawMessage) (string, error) {
	cmd := exec.CommandContext(ctx, c.path, c.args...)
	cmd.Dir = c.dir
	cmd.Stdin = bytes.NewReader(args)
	cmd.WaitDelay = waitDelay
	ownGroup(cmd)

	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		return "", fmt.Errorf("catalog: tool %q: %w", c.name, err)
	}
	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		_, _ = stdoutR.Close(), stdoutW.Close()
		return "", fmt.Errorf("catalog: tool %q: %w", c.name, err)
	}
	cmd.Stdout, cmd.Stderr = stdoutW, stderrW
	err = cmd.Start()
	_, _ = stdoutW.Close(), stderrW.Close()
	if err != nil {
		_, _ = stdoutR.Close(), stderrR.Close()
		return "", fmt.Errorf("catalog: tool %q: %w", c.name, err)
	}

	stdout := &limited{max: maxOutput}
	stderr := &tail{max: stderrTail}
	var reading sync.WaitGroup
	reading.Go(func() { drain(stdoutR, stdout) })
	reading.Go(func() { drain(stderrR, stderr) })

	err = cmd.Wait()
	killGroup(cmd)
	grace := time.AfterFunc(waitDelay, func() { _, _ = stdoutR.Close(), stderrR.Close() })
	reading.Wait()
	grace.Stop()

	if ctx.Err() != nil {
		return "", fmt.Errorf("catalog: tool %q: %w", c.name, context.Cause(ctx))
	}
	if stdout.over {
		return "", fmt.Errorf("catalog: tool %q: %w", c.name, errOutputTooLarge)
	}
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return "", fmt.Errorf("catalog: tool %q: %w: %s", c.name, err, msg)
		}
		return "", fmt.Errorf("catalog: tool %q: %w", c.name, err)
	}
	return stdout.String(), nil
}

func drain(r *os.File, w io.Writer) {
	_, _ = io.Copy(w, r)
	_ = r.Close()
}

type limited struct {
	buf  bytes.Buffer
	max  int
	over bool
}

func (l *limited) Write(p []byte) (int, error) {
	if l.buf.Len()+len(p) > l.max {
		l.over = true
		return 0, errOutputTooLarge
	}
	return l.buf.Write(p)
}

func (l *limited) String() string { return l.buf.String() }

type tail struct {
	buf []byte
	max int
}

func (t *tail) Write(p []byte) (int, error) {
	t.buf = append(t.buf, p...)
	if over := len(t.buf) - t.max; over > 0 {
		t.buf = append(t.buf[:0], t.buf[over:]...)
	}
	return len(p), nil
}

func (t *tail) String() string { return string(t.buf) }
