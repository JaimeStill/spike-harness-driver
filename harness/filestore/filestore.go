package filestore

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/JaimeStill/spike-harness-driver/harness"
)

// maxRecord bounds one record's line. A record holds a whole request and result.
const maxRecord = 16 << 20

// Store keeps each session's records in <dir>/<sessionID>.jsonl.
type Store struct {
	dir string
	mu  sync.Mutex
}

var _ harness.Store = (*Store)(nil)

// New returns a Store over dir, which it creates on the first Put.
func New(dir string) *Store {
	return &Store{dir: dir}
}

func (s *Store) Put(_ context.Context, rec harness.Record) error {
	path, err := s.path(rec.SessionID)
	if err != nil {
		return err
	}
	line, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("filestore: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return fmt.Errorf("filestore: %w", err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
	if err != nil {
		return fmt.Errorf("filestore: %w", err)
	}
	_, err = f.Write(append(line, '\n'))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("filestore: %w", err)
	}
	return nil
}

func (s *Store) Records(_ context.Context, sessionID string) ([]harness.Record, error) {
	path, err := s.path(sessionID)
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
	var recs []harness.Record
	sc := bufio.NewScanner(f)
	sc.Buffer(nil, maxRecord)
	for sc.Scan() {
		var r harness.Record
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			return nil, fmt.Errorf("filestore: %s line %d: %w", path, len(recs)+1, err)
		}
		recs = append(recs, r)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("filestore: %w", err)
	}
	return recs, nil
}

// path is the file that keeps a session's records. A session ID must name a file in the
// store's directory and nothing beyond it.
func (s *Store) path(sessionID string) (string, error) {
	name := sessionID + ".jsonl"
	if sessionID == "" || strings.ContainsAny(sessionID, `/\`) || !filepath.IsLocal(name) {
		return "", fmt.Errorf("filestore: invalid session ID %q", sessionID)
	}
	return filepath.Join(s.dir, name), nil
}
