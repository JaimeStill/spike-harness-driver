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

// ErrSequence is returned, wrapped, by Append for an event whose Seq doesn't follow the last one
// in its run's file. Two processes driving one run, which share a --state, would each number
// events from their own fold; the second to append fails here instead of corrupting the log.
var ErrSequence = errors.New("filestore: event out of sequence")

// Append adds e to its run's file once its Seq follows the file's last, and syncs the file, and
// the directory when it created the file, so a host crash keeps what Append reported written.
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
	last, err := tail(f)
	if err == nil && e.Seq != last+1 {
		err = fmt.Errorf("%w: run %s: event %d after event %d", ErrSequence, e.RunID, e.Seq, last)
	}
	if err == nil {
		_, err = f.Write(append(line, '\n'))
	}
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && last == 0 {
		err = syncDir(s.dir)
	}
	if err != nil {
		return fmt.Errorf("filestore: %w", err)
	}
	return nil
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	err = d.Sync()
	if cerr := d.Close(); err == nil {
		err = cerr
	}
	return err
}

// Events returns the run's events with a Seq above after. A run ID that can't name a file in the
// store names no run, so it has no events, which the Runner reports as an unknown run.
func (s *Store) Events(_ context.Context, runID string, after int) ([]workflow.Event, error) {
	path, err := s.path(runID)
	if err != nil {
		return nil, nil
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

// tail returns the Seq of the last event in f, zero for an empty file, after repairing a last
// line a crash left without its newline. Events reads such a line as it is when it decodes, so
// tail keeps it and adds the newline; one that doesn't decode is torn, and tail cuts it.
func tail(f *os.File) (int, error) {
	info, err := f.Stat()
	if err != nil {
		return 0, err
	}
	end := info.Size()
	if end == 0 {
		return 0, nil
	}
	b := make([]byte, 1)
	if _, err := f.ReadAt(b, end-1); err != nil {
		return 0, err
	}
	terminated := b[0] == '\n'
	if terminated {
		end--
	}
	start, line, err := lastLine(f, end)
	if err != nil {
		return 0, err
	}
	if !terminated {
		if json.Valid(line) {
			if _, err := f.Write([]byte{'\n'}); err != nil {
				return 0, err
			}
		} else {
			if err := f.Truncate(start); err != nil {
				return 0, err
			}
			if start == 0 {
				return 0, nil
			}
			if start, line, err = lastLine(f, start-1); err != nil {
				return 0, err
			}
		}
	}
	var e struct {
		Seq int `json:"seq"`
	}
	if err := json.Unmarshal(line, &e); err != nil {
		return 0, fmt.Errorf("%s: last event at byte %d: %w", f.Name(), start, err)
	}
	return e.Seq, nil
}

// lastLine returns the line of f that ends at end, without its newline, and where it starts. It
// reads backwards in chunks, so a long log isn't read whole.
func lastLine(f *os.File, end int64) (int64, []byte, error) {
	buf := make([]byte, 4096)
	start := int64(0)
	for pos := end; pos > 0; {
		from := max(pos-int64(len(buf)), 0)
		chunk := buf[:pos-from]
		if _, err := f.ReadAt(chunk, from); err != nil {
			return 0, nil, err
		}
		if i := bytes.LastIndexByte(chunk, '\n'); i >= 0 {
			start = from + int64(i) + 1
			break
		}
		pos = from
	}
	line := make([]byte, end-start)
	if _, err := f.ReadAt(line, start); err != nil {
		return 0, nil, err
	}
	return start, line, nil
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
