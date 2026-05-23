package catalog

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// ErrNotFound is returned by [Store.Get] when no entry exists for a key.
var ErrNotFound = errors.New("catalog: entry not found")

// Store persists [Entry] values keyed by [Entry.Key].
//
// Implementations must be safe for concurrent use. [Get] returns
// [ErrNotFound] when the key is unknown; other errors indicate I/O or
// decode failures and should bubble up unmodified.
type Store interface {
	Get(key string) (Entry, error)
	Put(entry Entry) error
	Delete(key string) error
	// Keys returns the set of keys currently stored, in unspecified order.
	Keys() ([]string, error)
}

// MemoryStore is an in-memory [Store] suitable for tests and `--offline`
// runs that should never touch the disk.
type MemoryStore struct {
	mu      sync.RWMutex
	entries map[string]Entry
}

// NewMemoryStore returns an empty in-memory store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{entries: make(map[string]Entry)}
}

// Get implements [Store].
func (s *MemoryStore) Get(key string) (Entry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.entries[key]
	if !ok {
		return Entry{}, ErrNotFound
	}
	return e, nil
}

// Put implements [Store].
func (s *MemoryStore) Put(e Entry) error {
	if err := e.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[e.Key()] = e
	return nil
}

// Delete implements [Store].
func (s *MemoryStore) Delete(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.entries, key)
	return nil
}

// Keys implements [Store].
func (s *MemoryStore) Keys() ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, 0, len(s.entries))
	for k := range s.entries {
		out = append(out, k)
	}
	sort.Strings(out)
	return out, nil
}

// FSStore persists entries as one JSON file per key, sharded by language
// under <root>/v<SchemaVersion>/<language>/<key>.json.
//
// Writes are atomic — entries are written to a sibling tempfile and
// renamed into place — so partial files never appear under cancellation
// or crash. The store is safe for concurrent use; per-key locking is not
// required because [os.Rename] is atomic on POSIX and each call writes a
// distinct tempfile.
type FSStore struct {
	root string
}

// NewFSStore returns a filesystem-backed store rooted at root. The
// directory is created on the first write.
func NewFSStore(root string) *FSStore {
	return &FSStore{root: root}
}

func (s *FSStore) versionDir() string {
	return filepath.Join(s.root, fmt.Sprintf("v%d", SchemaVersion))
}

func (s *FSStore) pathFor(lang Language, key string) string {
	return filepath.Join(s.versionDir(), string(lang), key+".json")
}

// Get implements [Store]. Because the on-disk layout shards by language
// and the cache key does not encode the language as a directory hint,
// [Get] probes both language directories. This keeps the [Store]
// interface language-agnostic at trivial filesystem cost.
func (s *FSStore) Get(key string) (Entry, error) {
	for _, lang := range []Language{LanguagePromQL, LanguageLogQL} {
		e, err := s.readFile(s.pathFor(lang, key))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return Entry{}, err
		}
		return e, nil
	}
	return Entry{}, ErrNotFound
}

func (s *FSStore) readFile(path string) (Entry, error) {
	data, err := os.ReadFile(path) //#nosec G304 -- path is computed from validated cache root + sanitised key
	if err != nil {
		return Entry{}, err
	}
	var e Entry
	if err := json.Unmarshal(data, &e); err != nil {
		return Entry{}, fmt.Errorf("catalog: decode %s: %w", path, err)
	}
	if err := e.Validate(); err != nil {
		return Entry{}, fmt.Errorf("catalog: invalid entry at %s: %w", path, err)
	}
	return e, nil
}

// Put implements [Store].
func (s *FSStore) Put(e Entry) error {
	if e.SchemaVersion == 0 {
		e.SchemaVersion = SchemaVersion
	}
	if err := e.Validate(); err != nil {
		return err
	}
	normaliseResult(e.Result)

	dst := s.pathFor(e.Language, e.Key())
	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
		return fmt.Errorf("catalog: mkdir %s: %w", filepath.Dir(dst), err)
	}

	tmp, err := os.CreateTemp(filepath.Dir(dst), ".entry-*.json.tmp")
	if err != nil {
		return fmt.Errorf("catalog: tempfile: %w", err)
	}
	tmpPath := tmp.Name()
	// Best-effort cleanup if anything below fails.
	defer func() {
		if _, statErr := os.Stat(tmpPath); statErr == nil {
			_ = os.Remove(tmpPath)
		}
	}()

	enc := json.NewEncoder(tmp)
	enc.SetIndent("", "  ")
	if err := enc.Encode(e); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("catalog: encode: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("catalog: close tempfile: %w", err)
	}
	if err := os.Rename(tmpPath, dst); err != nil {
		return fmt.Errorf("catalog: rename %s: %w", dst, err)
	}
	return nil
}

// Delete implements [Store]. Missing keys are not an error.
func (s *FSStore) Delete(key string) error {
	for _, lang := range []Language{LanguagePromQL, LanguageLogQL} {
		if err := os.Remove(s.pathFor(lang, key)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return nil
}

// Keys implements [Store]. The traversal skips the temp files written by
// [Put] in case a previous process crashed mid-write.
func (s *FSStore) Keys() ([]string, error) {
	out := []string{}
	root := s.versionDir()
	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return fs.SkipDir
			}
			return err
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		if filepath.Ext(name) != ".json" {
			return nil
		}
		out = append(out, name[:len(name)-len(".json")])
		return nil
	})
	if walkErr != nil && !errors.Is(walkErr, fs.ErrNotExist) {
		return nil, walkErr
	}
	sort.Strings(out)
	return out, nil
}

// normaliseResult sorts and dedupes the slice fields so on-disk
// representations diff cleanly between snapshots.
func normaliseResult(r *Result) {
	if r == nil {
		return
	}
	r.MetricNames = sortedUnique(r.MetricNames)
	r.LabelNames = sortedUnique(r.LabelNames)
	for k, v := range r.LabelValues {
		r.LabelValues[k] = sortedUnique(v)
	}
}

func sortedUnique(in []string) []string {
	if len(in) == 0 {
		return in
	}
	cp := append([]string(nil), in...)
	sort.Strings(cp)
	out := cp[:0]
	for i, v := range cp {
		if i > 0 && v == cp[i-1] {
			continue
		}
		out = append(out, v)
	}
	return out
}
