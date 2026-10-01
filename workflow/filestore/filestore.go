package filestore

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/JaimeStill/spike-harness-driver/workflow"
)

// maxEvent bounds one event's line. A run_started event holds the whole workflow, and a
// step_ended event a whole result.
const maxEvent = 16 << 20

// ext is the suffix of a run's file.
const ext = ".jsonl"

// Store keeps each run's events in <dir>/<runID>.jsonl.
type Store struct {
	dir string
	mu  sync.Mutex
}

var _ workflow.Store = (*Store)(nil)

// New returns a Store over dir, which it creates on the first Append.
func New(dir string) *Store {
	return &Store{dir: dir}
}

func (s *Store) Append(_ context.Context, e workflow.Event) error {
	path, err := s.path(e.RunID)
	if err != nil {
		return err
	}
	line, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("filestore: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return fmt.Errorf("filestore: %w", err)
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return fmt.Errorf("filestore: %w", err)
	}
	err = trimTorn(f)
	if err == nil {
		_, err = f.Write(append(line, '\n'))
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("filestore: %w", err)
	}
	return nil
}

func (s *Store) Events(_ context.Context, runID string, after int) ([]workflow.Event, error) {
	path, err := s.path(runID)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("filestore: %w", err)
	}
	defer func() { _ = f.Close() }()
	var (
		evs      []workflow.Event
		line     int
		unended  bool
		sc       = bufio.NewScanner(f)
		scanLine = func(data []byte, atEOF bool) (int, []byte, error) {
			advance, token, err := bufio.ScanLines(data, atEOF)
			if token != nil && advance == len(data) && atEOF && !bytes.HasSuffix(data, []byte{'\n'}) {
				unended = true
			}
			return advance, token, err
		}
	)
	sc.Buffer(nil, maxEvent)
	sc.Split(scanLine)
	for sc.Scan() {
		line++
		var e workflow.Event
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			if unended {
				break
			}
			return nil, fmt.Errorf("filestore: %s line %d: %w", path, line, err)
		}
		if e.Seq > after {
			evs = append(evs, e)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("filestore: %w", err)
	}
	return evs, nil
}

func (s *Store) Runs(_ context.Context) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := os.ReadDir(s.dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("filestore: %w", err)
	}
	var ids []string
	for _, ent := range entries {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ext) {
			continue
		}
		ids = append(ids, strings.TrimSuffix(ent.Name(), ext))
	}
	return ids, nil // ReadDir sorts by name, and every name shares the suffix
}

// trimTorn cuts a torn last line from f, a file a crash left ending mid-write, back to the
// last newline, so the next line starts on a line of its own. It leaves a file that is empty
// or ends in a newline alone.
func trimTorn(f *os.File) error {
	info, err := f.Stat()
	if err != nil {
		return err
	}
	size := info.Size()
	if size == 0 {
		return nil
	}
	buf := make([]byte, 4096)
	end := size
	for end > 0 {
		start := max(end-int64(len(buf)), 0)
		chunk := buf[:end-start]
		if _, err := f.ReadAt(chunk, start); err != nil {
			return err
		}
		if end == size && chunk[len(chunk)-1] == '\n' {
			return nil
		}
		if i := bytes.LastIndexByte(chunk, '\n'); i >= 0 {
			return f.Truncate(start + int64(i) + 1)
		}
		end = start
	}
	return f.Truncate(0)
}

// path is the file that keeps a run's events. A run ID must name a file in the store's
// directory and nothing beyond it.
func (s *Store) path(runID string) (string, error) {
	name := runID + ext
	if runID == "" || strings.ContainsAny(runID, `/\`) || !filepath.IsLocal(name) {
		return "", fmt.Errorf("filestore: invalid run ID %q", runID)
	}
	return filepath.Join(s.dir, name), nil
}
