package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"sync"
)

// errInvalidData is returned for a data file that is not valid.
var errInvalidData = errors.New("invalid")

// store holds the per-name greeting counts. With a path, every change is
// written to that file before add returns.
type store struct {
	mu     sync.Mutex
	counts map[string]int64
	path   string
}

// loadStore reads the counts file at path. A missing file starts empty and
// an empty path keeps counts in memory only. The file is never modified.
func loadStore(path string) (*store, error) {
	st := &store{counts: map[string]int64{}, path: path}
	if path == "" {
		return st, nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		var pe *fs.PathError
		if errors.As(err, &pe) {
			err = pe.Err
		}
		return nil, err
	}
	counts, err := parseCounts(data)
	if err != nil {
		return nil, err
	}
	st.counts = counts
	return st, nil
}

// parseCounts accepts exactly one object with the single key "counts",
// mapping strings to non-negative integers, and nothing after it but
// whitespace. Anything else is errInvalidData; the contents are not echoed.
func parseCounts(data []byte) (map[string]int64, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	if !isDelim(dec, '{') || !isKey(dec, "counts") || !isDelim(dec, '{') {
		return nil, errInvalidData
	}
	counts := map[string]int64{}
	for dec.More() {
		t, err := dec.Token()
		key, ok := t.(string)
		if err != nil || !ok {
			return nil, errInvalidData
		}
		if _, dup := counts[key]; dup {
			return nil, errInvalidData
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil || len(raw) == 0 {
			return nil, errInvalidData
		}
		for _, c := range raw {
			if c < '0' || c > '9' {
				return nil, errInvalidData
			}
		}
		n, err := strconv.ParseInt(string(raw), 10, 64)
		if err != nil {
			return nil, errInvalidData
		}
		counts[key] = n
	}
	if !isDelim(dec, '}') || !isDelim(dec, '}') {
		return nil, errInvalidData
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errInvalidData
	}
	return counts, nil
}

func isDelim(dec *json.Decoder, want json.Delim) bool {
	t, err := dec.Token()
	d, ok := t.(json.Delim)
	return err == nil && ok && d == want
}

func isKey(dec *json.Decoder, want string) bool {
	t, err := dec.Token()
	k, ok := t.(string)
	return err == nil && ok && k == want
}

// add counts one greeting for name. With a data file it rewrites the file
// while holding the lock; if that fails the count is rolled back.
func (s *store) add(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	old, had := s.counts[name]
	s.counts[name] = old + 1
	if s.path == "" {
		return nil
	}
	if err := s.save(); err != nil {
		if had {
			s.counts[name] = old
		} else {
			delete(s.counts, name)
		}
		return err
	}
	return nil
}

// snapshot returns a copy of the counts.
func (s *store) snapshot() map[string]int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]int64, len(s.counts))
	for k, v := range s.counts {
		out[k] = v
	}
	return out
}

// save writes the counts to a temp file in the data file's directory and
// renames it over the data file, so a reader never sees a partial file.
// The caller holds s.mu.
func (s *store) save() error {
	b, err := json.Marshal(map[string]any{"counts": s.counts})
	if err != nil {
		return err
	}
	b = append(b, '\n')
	f, err := os.CreateTemp(filepath.Dir(s.path), filepath.Base(s.path)+".tmp*")
	if err != nil {
		return err
	}
	_, err = f.Write(b)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(f.Name(), 0o644)
	}
	if err == nil {
		err = os.Rename(f.Name(), s.path)
	}
	if err != nil {
		os.Remove(f.Name())
	}
	return err
}
