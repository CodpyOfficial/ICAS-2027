package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
)

type rec struct {
	Seq   int    `json:"seq"`
	Email string `json:"email"`
}

func TestAddAndList(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		err := s.Add("things", func(existing []json.RawMessage) (any, error) {
			return rec{Seq: len(existing) + 1}, nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	raw, err := s.List("things")
	if err != nil {
		t.Fatal(err)
	}
	got, err := Decode[rec](raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].Seq != 1 || got[2].Seq != 3 {
		t.Fatalf("unexpected records: %+v", got)
	}
}

func TestEmptyCollection(t *testing.T) {
	s, _ := Open(t.TempDir())
	n, err := s.Count("missing")
	if err != nil || n != 0 {
		t.Fatalf("Count on missing collection = %d, %v; want 0, nil", n, err)
	}
}

func TestSkipWritesNothing(t *testing.T) {
	s, _ := Open(t.TempDir())
	err := s.Add("subs", func([]json.RawMessage) (any, error) { return nil, ErrSkip })
	if !errors.Is(err, ErrSkip) {
		t.Fatalf("err = %v, want ErrSkip", err)
	}
	if n, _ := s.Count("subs"); n != 0 {
		t.Fatalf("Count = %d after skipped add, want 0", n)
	}
}

func TestInvalidCollectionName(t *testing.T) {
	s, _ := Open(t.TempDir())
	for _, name := range []string{"", "../etc", "Upper", "a/b", "a.b"} {
		if _, err := s.List(name); err == nil {
			t.Errorf("List(%q) succeeded, want error", name)
		}
	}
}

func TestConcurrentSequenceIsDense(t *testing.T) {
	s, _ := Open(t.TempDir())
	const n = 40
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			err := s.Add("seq", func(existing []json.RawMessage) (any, error) {
				return rec{Seq: len(existing) + 1, Email: fmt.Sprint(i)}, nil
			})
			if err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	raw, _ := s.List("seq")
	got, err := Decode[rec](raw)
	if err != nil {
		t.Fatal(err)
	}
	seen := make(map[int]bool)
	for _, r := range got {
		if seen[r.Seq] {
			t.Fatalf("duplicate sequence number %d", r.Seq)
		}
		seen[r.Seq] = true
	}
	if len(seen) != n {
		t.Fatalf("got %d distinct sequence numbers, want %d", len(seen), n)
	}
}
