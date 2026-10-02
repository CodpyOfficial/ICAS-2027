// Package store persists form records (proposal submissions, contact
// messages and newsletter subscriptions) as JSON Lines files, one file per
// collection. It is intentionally simple: a conference receives at most a few
// thousand records, so every write re-reads the collection under a lock,
// which makes sequence numbers and uniqueness checks trivially consistent.
package store

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sync"
)

// ErrSkip may be returned by a build function to abort an Add without
// treating it as a failure (for example, a duplicate newsletter address).
var ErrSkip = errors.New("store: record skipped")

var validName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// Store is a directory of JSON Lines collections. It is safe for concurrent use.
type Store struct {
	dir string
	mu  sync.Mutex
}

// Open creates dir if needed and returns a Store rooted there.
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("store: create data directory: %w", err)
	}
	return &Store{dir: dir}, nil
}

// Dir returns the directory the store writes to.
func (s *Store) Dir() string { return s.dir }

func (s *Store) path(collection string) (string, error) {
	if !validName.MatchString(collection) {
		return "", fmt.Errorf("store: invalid collection name %q", collection)
	}
	return filepath.Join(s.dir, collection+".jsonl"), nil
}

// Add reads the existing records of collection, passes them to build and
// appends the record build returns. The read and the append happen under a
// single lock, so build may derive sequence numbers or reject duplicates
// from existing without races. If build returns an error (including ErrSkip)
// nothing is written and that error is returned.
func (s *Store) Add(collection string, build func(existing []json.RawMessage) (any, error)) error {
	p, err := s.path(collection)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	existing, err := readLines(p)
	if err != nil {
		return err
	}
	rec, err := build(existing)
	if err != nil {
		return err
	}
	line, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("store: encode record: %w", err)
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("store: open %s: %w", collection, err)
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		f.Close()
		return fmt.Errorf("store: write %s: %w", collection, err)
	}
	return f.Close()
}

// List returns every record of collection in insertion order. A collection
// that has never been written is empty, not an error.
func (s *Store) List(collection string) ([]json.RawMessage, error) {
	p, err := s.path(collection)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return readLines(p)
}

// Count returns the number of records in collection.
func (s *Store) Count(collection string) (int, error) {
	recs, err := s.List(collection)
	return len(recs), err
}

// Decode unmarshals raw records into values of type T, skipping none: the
// first malformed record aborts with an error naming its position.
func Decode[T any](raw []json.RawMessage) ([]T, error) {
	out := make([]T, 0, len(raw))
	for i, r := range raw {
		var v T
		if err := json.Unmarshal(r, &v); err != nil {
			return nil, fmt.Errorf("store: record %d: %w", i+1, err)
		}
		out = append(out, v)
	}
	return out, nil
}

func readLines(p string) ([]json.RawMessage, error) {
	f, err := os.Open(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", filepath.Base(p), err)
	}
	defer f.Close()

	var out []json.RawMessage
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		out = append(out, json.RawMessage(append([]byte(nil), line...)))
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("store: read %s: %w", filepath.Base(p), err)
	}
	return out, nil
}
