// Package native indexes Claude Code's auto memory notes for search.
//
// Claude Code keeps notes per repo, under <config dir>/projects/<repo>/memory,
// and reads them well within that repo. Nothing connects the repos: in the
// October 2026 audit 1 of 790 note references crossed a repo boundary, and a
// session in one repo answered "not much" when asked what it knew about the
// user while the profile sat in three other repos' notes.
//
// This package only ever reads those files. The index is a copy in the mnemos
// database that can be dropped and rebuilt at any time.
package native

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// ErrNotFound is returned when a note ID is not in the searched roots.
var ErrNotFound = errors.New("native note not found")

// IDPrefix marks an ID as a native note, which is how mnemos_get tells them
// from observations.
const IDPrefix = "native:"

// maxNoteBytes skips files too large to be a note. The largest real note in
// the audit was 25KB.
const maxNoteBytes = 256 << 10

// syncEvery throttles the rescan that runs before a search. The MCP server
// lives as long as the session, so this picks up notes other sessions save
// without walking the tree on every call.
const syncEvery = 5 * time.Minute

// Doc is one indexed note.
type Doc struct {
	ID          string
	Path        string
	Root        string
	Project     string
	Name        string
	Description string
	DocType     string
	Body        string
	Size        int64
	ModifiedAt  time.Time
}

// Hit is one search result. AlsoIn lists notes with the same name in other
// repos, collapsed into this hit so that one rule learned six times does not
// take every result slot.
type Hit struct {
	Doc
	Score   float64
	Snippet string
	AlsoIn  []Ref
}

// Ref points at a note collapsed into a Hit.
type Ref struct {
	ID      string
	Project string
}

// FileState is what the store remembers about a file to detect changes.
type FileState struct {
	Size       int64
	ModifiedAt time.Time
}

// Store persists the index. Declared here at the consumer; the SQLite
// implementation lives in internal/storage.
type Store interface {
	// FileStates returns the indexed files under root, keyed by path.
	FileStates(ctx context.Context, root string) (map[string]FileState, error)
	// Apply upserts docs and removes paths in one transaction.
	Apply(ctx context.Context, docs []Doc, removed []string, now time.Time) error
	// Search returns FTS matches within roots, best first.
	Search(ctx context.Context, query string, roots []string, limit int) ([]Hit, error)
	// Get returns one note by ID within roots.
	Get(ctx context.Context, id string, roots []string) (*Doc, error)
	// Counts returns how many notes and repos are indexed within roots.
	Counts(ctx context.Context, roots []string) (notes, repos int, err error)
}

// Config bundles Service dependencies.
type Config struct {
	Store Store
	Roots []string
	Clock func() time.Time
}

// Service keeps the index current and answers queries against it.
type Service struct {
	store Store
	roots []string
	clock func() time.Time

	mu       sync.Mutex
	lastSync time.Time
}

// NewService constructs a Service.
func NewService(cfg Config) *Service {
	if cfg.Clock == nil {
		cfg.Clock = time.Now
	}
	return &Service{store: cfg.Store, roots: cfg.Roots, clock: cfg.Clock}
}

// DefaultRoots resolves which config directories to index: the configured
// ones, else the directory this process runs under.
func DefaultRoots(configured []string, claudeConfigDir, home string) []string {
	if len(configured) > 0 {
		return configured
	}
	if claudeConfigDir != "" {
		return []string{claudeConfigDir}
	}
	if home != "" {
		return []string{filepath.Join(home, ".claude")}
	}
	return nil
}

// Roots returns the config directories this service covers.
func (s *Service) Roots() []string { return s.roots }

// SyncStats reports what one Sync changed.
type SyncStats struct {
	Scanned int
	Updated int
	Removed int
}

// Sync brings the index in line with the files on disk, reading only files
// whose size or modified time changed. It never writes to a notes directory.
func (s *Service) Sync(ctx context.Context) (SyncStats, error) {
	var total SyncStats
	for _, root := range s.roots {
		stats, err := s.syncRoot(ctx, root)
		if err != nil {
			return total, fmt.Errorf("sync %s: %w", root, err)
		}
		total.Scanned += stats.Scanned
		total.Updated += stats.Updated
		total.Removed += stats.Removed
	}
	s.mu.Lock()
	s.lastSync = s.clock()
	s.mu.Unlock()
	return total, nil
}

func (s *Service) syncRoot(ctx context.Context, root string) (SyncStats, error) {
	var stats SyncStats
	known, err := s.store.FileStates(ctx, root)
	if err != nil {
		return stats, err
	}
	dirs, err := filepath.Glob(filepath.Join(globEscape(root), "projects", "*", "memory"))
	if err != nil {
		return stats, fmt.Errorf("glob notes: %w", err)
	}
	var docs []Doc
	seen := make(map[string]bool, len(known))
	for _, dir := range dirs {
		if isThrowawayRepo(filepath.Base(filepath.Dir(dir))) {
			continue
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue // a directory removed mid-scan is not worth failing over
		}
		// Decoding a repo name walks the filesystem, so do it at most once per
		// directory and only when something in it changed.
		project := ""
		for _, e := range entries {
			if !isNoteEntry(e) {
				continue
			}
			path := filepath.Join(dir, e.Name())
			info, err := e.Info()
			if err != nil || info.Size() > maxNoteBytes {
				continue
			}
			stats.Scanned++
			seen[path] = true
			state, ok := known[path]
			if ok && state.Size == info.Size() && state.ModifiedAt.Equal(info.ModTime().UTC()) {
				continue
			}
			if project == "" {
				project = repoName(filepath.Base(filepath.Dir(dir)))
			}
			doc, err := readDoc(root, project, path, info)
			if err != nil {
				delete(seen, path) // unreadable now, so drop any stale copy
				continue
			}
			docs = append(docs, *doc)
		}
	}
	var removed []string
	for path := range known {
		if !seen[path] {
			removed = append(removed, path)
		}
	}
	stats.Updated, stats.Removed = len(docs), len(removed)
	if len(docs) == 0 && len(removed) == 0 {
		return stats, nil
	}
	return stats, s.store.Apply(ctx, docs, removed, s.clock().UTC())
}

// isThrowawayRepo reports whether a notes directory belongs to a session that
// ran inside a temp directory. Those repos are gone and their notes are
// scaffolding: the store this was built against held fixture notes from verify
// harness runs, such as made-up auth rules, that would otherwise read as things
// the user had learned.
func isThrowawayRepo(encoded string) bool {
	for _, prefix := range []string{"-private-var-folders-", "-var-folders-", "-private-tmp-", "-tmp-"} {
		if strings.HasPrefix(encoded, prefix) {
			return true
		}
	}
	return false
}

// isNoteEntry accepts regular markdown files only. Symlinks are refused so a
// link inside a notes directory cannot pull an arbitrary file into search, and
// MEMORY.md is refused because it lists every other note, so it would match
// nearly any query.
func isNoteEntry(e fs.DirEntry) bool {
	return e.Type().IsRegular() &&
		strings.HasSuffix(e.Name(), ".md") &&
		e.Name() != "MEMORY.md"
}

func readDoc(root, project, path string, info fs.FileInfo) (*Doc, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read note: %w", err)
	}
	if !utf8.Valid(data) {
		return nil, fmt.Errorf("read note: %s is not UTF-8", path)
	}
	meta, body := parseNote(string(data))
	name := meta.name
	if name == "" {
		name = strings.TrimSuffix(filepath.Base(path), ".md")
	}
	return &Doc{
		ID:          noteID(path),
		Path:        path,
		Root:        root,
		Project:     project,
		Name:        name,
		Description: meta.description,
		DocType:     meta.docType,
		Body:        body,
		Size:        info.Size(),
		ModifiedAt:  info.ModTime().UTC(),
	}, nil
}

// noteID is stable across rescans, so an ID in a search result stays openable.
func noteID(path string) string {
	sum := sha256.Sum256([]byte(path))
	return IDPrefix + hex.EncodeToString(sum[:8])
}

// Search rescans if the last scan is stale, then returns up to limit hits with
// same-named notes collapsed. A failed rescan still searches what is indexed:
// slightly stale results beat none.
func (s *Service) Search(ctx context.Context, query string, limit int) ([]Hit, error) {
	if limit <= 0 {
		limit = 5
	}
	query = meaningfulTerms(query)
	if query == "" || len(s.roots) == 0 {
		return nil, nil
	}
	s.mu.Lock()
	stale := s.clock().Sub(s.lastSync) >= syncEvery
	s.mu.Unlock()
	if stale {
		_, _ = s.Sync(ctx)
	}
	hits, err := s.store.Search(ctx, query, s.roots, limit*searchOverfetch)
	if err != nil {
		return nil, err
	}
	return collapse(hits, limit), nil
}

// searchOverfetch leaves room for collapse to discard duplicates and still
// fill the requested limit.
const searchOverfetch = 5

// collapse keeps the best hit per note name, recording the others on it.
func collapse(hits []Hit, limit int) []Hit {
	index := make(map[string]int, len(hits))
	out := make([]Hit, 0, limit)
	for _, h := range hits {
		key := strings.ToLower(strings.TrimSuffix(filepath.Base(h.Path), ".md"))
		if at, dup := index[key]; dup {
			out[at].AlsoIn = append(out[at].AlsoIn, Ref{ID: h.ID, Project: h.Project})
			continue
		}
		if len(out) == limit {
			continue // a later duplicate may still belong to a kept hit
		}
		index[key] = len(out)
		out = append(out, h)
	}
	return out
}

// Get returns a note by ID, within this service's roots only.
func (s *Service) Get(ctx context.Context, id string) (*Doc, error) {
	return s.store.Get(ctx, id, s.roots)
}

// Counts reports how many notes and repos are indexed.
func (s *Service) Counts(ctx context.Context) (notes, repos int, err error) {
	return s.store.Counts(ctx, s.roots)
}

// globEscape stops a config directory containing glob characters from being
// read as a pattern.
func globEscape(path string) string {
	return strings.NewReplacer(`\`, `\\`, `*`, `\*`, `?`, `\?`, `[`, `\[`).Replace(path)
}
