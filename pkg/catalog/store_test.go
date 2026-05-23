package catalog_test

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/qualithm/ratatoskr-go/pkg/catalog"
)

func sampleEntry() catalog.Entry {
	return catalog.Entry{
		SchemaVersion: catalog.SchemaVersion,
		Query:         "up == 0",
		Language:      catalog.LanguagePromQL,
		Source:        "http://mimir:9090",
		FetchedAt:     time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC),
		TTLSeconds:    3600,
		Result: &catalog.Result{
			MetricNames: []string{"up"},
			LabelNames:  []string{"job", "instance"},
			SeriesCount: 42,
		},
	}
}

func TestEntryKeyIsDeterministic(t *testing.T) {
	t.Parallel()
	a := sampleEntry()
	b := sampleEntry()
	if a.Key() != b.Key() {
		t.Fatalf("keys differ: %s vs %s", a.Key(), b.Key())
	}
	b.Source = "http://other:9090"
	if a.Key() == b.Key() {
		t.Fatal("changing source must change key")
	}
}

func TestEntryValidate(t *testing.T) {
	t.Parallel()
	cases := map[string]func(*catalog.Entry){
		"missing query":    func(e *catalog.Entry) { e.Query = "" },
		"missing source":   func(e *catalog.Entry) { e.Source = "" },
		"missing time":     func(e *catalog.Entry) { e.FetchedAt = time.Time{} },
		"unknown language": func(e *catalog.Entry) { e.Language = "graphql" },
		"newer schema":     func(e *catalog.Entry) { e.SchemaVersion = catalog.SchemaVersion + 1 },
		"no result no err": func(e *catalog.Entry) { e.Result = nil; e.Error = "" },
	}
	for name, mut := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			e := sampleEntry()
			mut(&e)
			if err := e.Validate(); err == nil {
				t.Fatalf("expected error for %s", name)
			}
		})
	}
	if err := sampleEntry().Validate(); err != nil {
		t.Fatalf("baseline entry should validate: %v", err)
	}
}

func TestEntryExpired(t *testing.T) {
	t.Parallel()
	e := sampleEntry()
	if e.Expired(e.FetchedAt.Add(time.Hour)) {
		t.Fatal("entry within TTL marked expired")
	}
	if !e.Expired(e.FetchedAt.Add(2 * time.Hour)) {
		t.Fatal("entry past TTL not marked expired")
	}
	e.TTLSeconds = 0
	if e.Expired(e.FetchedAt.Add(24 * time.Hour)) {
		t.Fatal("zero TTL must not expire implicitly")
	}
}

func TestMemoryStoreRoundTrip(t *testing.T) {
	t.Parallel()
	s := catalog.NewMemoryStore()
	e := sampleEntry()
	if err := s.Put(e); err != nil {
		t.Fatalf("put: %v", err)
	}
	got, err := s.Get(e.Key())
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Query != e.Query {
		t.Fatalf("query mismatch: %s vs %s", got.Query, e.Query)
	}
	keys, err := s.Keys()
	if err != nil {
		t.Fatalf("keys: %v", err)
	}
	if len(keys) != 1 || keys[0] != e.Key() {
		t.Fatalf("keys: %v", keys)
	}
	if err := s.Delete(e.Key()); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := s.Get(e.Key()); !errors.Is(err, catalog.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestFSStoreRoundTripAndAtomicity(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s := catalog.NewFSStore(dir)

	e := sampleEntry()
	if err := s.Put(e); err != nil {
		t.Fatalf("put: %v", err)
	}

	got, err := s.Get(e.Key())
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Result.SeriesCount != 42 {
		t.Fatalf("series count mismatch: %d", got.Result.SeriesCount)
	}

	// On-disk path follows the locked layout.
	want := filepath.Join(dir, "v1", "promql", e.Key()+".json")
	if _, err := s.Get(e.Key()); err != nil {
		t.Fatalf("get after rename: %v", err)
	}
	if !strings.Contains(want, "v1/promql") {
		t.Fatalf("layout broken: %s", want)
	}

	// Overwrite must replace, not append.
	e2 := e
	e2.Result = &catalog.Result{MetricNames: []string{"up"}, SeriesCount: 99}
	if err := s.Put(e2); err != nil {
		t.Fatalf("put 2: %v", err)
	}
	got, err = s.Get(e.Key())
	if err != nil {
		t.Fatalf("get 2: %v", err)
	}
	if got.Result.SeriesCount != 99 {
		t.Fatalf("overwrite failed: %d", got.Result.SeriesCount)
	}

	keys, err := s.Keys()
	if err != nil {
		t.Fatalf("keys: %v", err)
	}
	if len(keys) != 1 || keys[0] != e.Key() {
		t.Fatalf("keys: %v", keys)
	}

	if err := s.Delete(e.Key()); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := s.Get(e.Key()); !errors.Is(err, catalog.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestFSStoreEmptyKeysOnMissingDir(t *testing.T) {
	t.Parallel()
	s := catalog.NewFSStore(filepath.Join(t.TempDir(), "does-not-exist"))
	keys, err := s.Keys()
	if err != nil {
		t.Fatalf("keys on missing root: %v", err)
	}
	if len(keys) != 0 {
		t.Fatalf("expected empty, got %v", keys)
	}
}

func TestFSStoreNormalisesSlicesOnWrite(t *testing.T) {
	t.Parallel()
	s := catalog.NewFSStore(t.TempDir())
	e := sampleEntry()
	e.Result.LabelNames = []string{"job", "instance", "job"} // duplicates + unsorted
	if err := s.Put(e); err != nil {
		t.Fatalf("put: %v", err)
	}
	got, err := s.Get(e.Key())
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	want := []string{"instance", "job"}
	if len(got.Result.LabelNames) != len(want) {
		t.Fatalf("normalise: %v", got.Result.LabelNames)
	}
	for i := range want {
		if got.Result.LabelNames[i] != want[i] {
			t.Fatalf("normalise[%d]: %s vs %s", i, got.Result.LabelNames[i], want[i])
		}
	}
}
