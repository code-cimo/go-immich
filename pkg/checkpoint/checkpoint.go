// Package checkpoint persists the last successfully processed record ID of a
// long-running import so a restarted migration resumes instead of re-scanning.
// The state file is written atomically (tmp + rename) because power loss on an
// SBC is a realistic failure mode.
package checkpoint

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// State is the serialisable resume position for one migration source.
type State struct {
	Source    string    `json:"source"`     // e.g. "users", "assets", "albums"
	LastID    string    `json:"last_id"`    // last imported row id ("" = start)
	Count     int64     `json:"count"`      // rows imported so far
	UpdatedAt time.Time `json:"updated_at"`
}

// Store manages a single JSON state file holding one State per source.
type Store struct {
	mu   sync.Mutex
	path string
	data map[string]State
}

// Open loads the state file if present; a missing/corrupt file starts fresh.
func Open(path string) (*Store, error) {
	s := &Store{path: path, data: make(map[string]State)}
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("checkpoint: read %s: %w", path, err)
	}
	if len(raw) > 0 {
		var states []State
		if err := json.Unmarshal(raw, &states); err != nil {
			// Corrupt file: keep a backup and restart clean rather than fail.
			_ = os.Rename(path, path+".corrupt-"+time.Now().UTC().Format("20060102T150405"))
			s.data = make(map[string]State)
			return s, nil
		}
		for _, st := range states {
			s.data[st.Source] = st
		}
	}
	return s, nil
}

// LastID returns the saved resume position for source ("" when none).
func (s *Store) LastID(source string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data[source].LastID
}

// Count returns how many rows were recorded as imported for source.
func (s *Store) Count(source string) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data[source].Count
}

// Save updates the position for source and flushes it to disk atomically.
func (s *Store) Save(source, lastID string, count int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[source] = State{
		Source:    source,
		LastID:    lastID,
		Count:     count,
		UpdatedAt: time.Now().UTC(),
	}
	return s.flushLocked()
}

// Reset clears all saved positions and persists the empty state.
func (s *Store) Reset() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data = make(map[string]State)
	return s.flushLocked()
}

func (s *Store) flushLocked() error {
	states := make([]State, 0, len(s.data))
	for _, st := range s.data {
		states = append(states, st)
	}
	raw, err := json.MarshalIndent(states, "", "  ")
	if err != nil {
		return fmt.Errorf("checkpoint: marshal: %w", err)
	}
	dir := filepath.Dir(s.path)
	tmp, err := os.CreateTemp(dir, ".import_state-*")
	if err != nil {
		return fmt.Errorf("checkpoint: create temp: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }() // no-op after successful rename
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("checkpoint: write temp: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("checkpoint: fsync: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("checkpoint: close temp: %w", err)
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		return fmt.Errorf("checkpoint: rename: %w", err)
	}
	return nil
}
