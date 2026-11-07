package native_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/polyxmedia/mnemos/internal/native"
	"github.com/polyxmedia/mnemos/internal/storage"
)

type fixture struct {
	t     *testing.T
	root  string
	store native.Store
	svc   *native.Service
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	db, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	root := t.TempDir()
	store := db.Native()
	return &fixture{
		t: t, root: root, store: store,
		svc: native.NewService(native.Config{Store: store, Roots: []string{root}}),
	}
}

func (f *fixture) write(root, repo, file, content string) string {
	f.t.Helper()
	dir := filepath.Join(root, "projects", repo, "memory")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		f.t.Fatal(err)
	}
	path := filepath.Join(dir, file)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		f.t.Fatal(err)
	}
	return path
}

func (f *fixture) sync() native.SyncStats {
	f.t.Helper()
	stats, err := f.svc.Sync(context.Background())
	if err != nil {
		f.t.Fatal(err)
	}
	return stats
}

const profileNote = `---
name: User profile
description: Career history and current role
type: user
---

Staff engineer with ten years across logistics platforms.
`

func TestSyncIndexesOnlyChangedFilesAndDropsRemovedOnes(t *testing.T) {
	f := newFixture(t)
	path := f.write(f.root, "-repo-a", "user_profile.md", profileNote)
	f.write(f.root, "-repo-b", "topic.md", "plain body with no frontmatter")

	if got := f.sync(); got.Scanned != 2 || got.Updated != 2 || got.Removed != 0 {
		t.Fatalf("first sync: %+v", got)
	}
	if got := f.sync(); got.Scanned != 2 || got.Updated != 0 {
		t.Fatalf("unchanged files must not be reindexed: %+v", got)
	}

	later := time.Now().Add(time.Minute)
	if err := os.WriteFile(path, []byte(profileNote+"\nNow also writes Go.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatal(err)
	}
	if got := f.sync(); got.Updated != 1 {
		t.Fatalf("edited note not reindexed: %+v", got)
	}
	hits, err := f.store.Search(context.Background(), "writes", []string{f.root}, 5)
	if err != nil || len(hits) != 1 {
		t.Fatalf("edited text not searchable: %v %+v", err, hits)
	}

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if got := f.sync(); got.Removed != 1 {
		t.Fatalf("deleted note not removed: %+v", got)
	}
	if hits, _ := f.store.Search(context.Background(), "writes", []string{f.root}, 5); len(hits) != 0 {
		t.Fatalf("deleted note still searchable: %+v", hits)
	}
}

func TestSyncSkipsIndexFilesSymlinksAndOversizedNotes(t *testing.T) {
	f := newFixture(t)
	f.write(f.root, "-repo", "MEMORY.md", "- [note](note.md) lists everything")
	f.write(f.root, "-repo", "readme.txt", "not markdown")
	f.write(f.root, "-repo", "huge.md", strings.Repeat("x", 300<<10))
	outside := filepath.Join(t.TempDir(), "secret.md")
	if err := os.WriteFile(outside, []byte("private"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(f.root, "projects", "-repo", "memory", "link.md")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if got := f.sync(); got.Updated != 0 || got.Scanned != 0 {
		t.Fatalf("none of these should be indexed: %+v", got)
	}
}

func TestSyncSkipsNotesFromTempDirectories(t *testing.T) {
	f := newFixture(t)
	for _, repo := range []string{
		"-private-var-folders-60-abc-T-mnemos-on-XYZ-mnemos",
		"-var-folders-60-abc-T-run",
		"-private-tmp-claude-501-scratch",
		"-tmp-run",
	} {
		f.write(f.root, repo, "fixture.md", "made up rule")
	}
	f.write(f.root, "-Users-a-Code-tmp-tool", "real.md", "a real note")
	if got := f.sync(); got.Scanned != 1 || got.Updated != 1 {
		t.Fatalf("only the real repo should be indexed: %+v", got)
	}
}

func TestSyncNeverChangesTheNotes(t *testing.T) {
	f := newFixture(t)
	path := f.write(f.root, "-repo", "user_profile.md", profileNote)
	past := time.Now().Add(-48 * time.Hour).Truncate(time.Second)
	if err := os.Chtimes(path, past, past); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Dir(path)

	f.sync()
	if _, err := f.svc.Search(context.Background(), "career", 5); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != profileNote {
		t.Error("note content changed")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !info.ModTime().Equal(past) {
		t.Errorf("note modified time changed: %v", info.ModTime())
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("files appeared in the notes directory: %v", entries)
	}
}

func TestSearchFindsNotesFromOtherReposAndRanksNameAndDescriptionFirst(t *testing.T) {
	f := newFixture(t)
	f.write(f.root, "-repo-a", "user_profile.md", profileNote)
	f.write(f.root, "-repo-b", "billing.md", "---\nname: billing\n---\nmentions career once in passing")

	hits, err := f.svc.Search(context.Background(), "career", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 {
		t.Fatalf("want both notes, got %+v", hits)
	}
	if hits[0].Name != "User profile" || hits[0].DocType != "user" {
		t.Errorf("description match should rank first: %+v", hits[0])
	}
	if !strings.HasPrefix(hits[0].ID, native.IDPrefix) {
		t.Errorf("id missing prefix: %s", hits[0].ID)
	}
	doc, err := f.svc.Get(context.Background(), hits[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(doc.Body, "Staff engineer") || strings.Contains(doc.Body, "description:") {
		t.Errorf("body should be the note without its frontmatter: %q", doc.Body)
	}
}

func TestSearchIgnoresStopWordsSoFillerCannotOutrankTheTopic(t *testing.T) {
	f := newFixture(t)
	f.write(f.root, "-repo-a", "memory-notes.md", "memory method mechanism me me me")
	f.write(f.root, "-repo-b", "career.md", "---\nname: career\n---\nroles and history")

	hits, err := f.svc.Search(context.Background(), "tell me about my career", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Name != "career" {
		t.Fatalf("filler should not match: %+v", hits)
	}
}

func TestSearchCollapsesSameNamedNotesAcrossRepos(t *testing.T) {
	f := newFixture(t)
	for _, repo := range []string{"-repo-a", "-repo-b", "-repo-c"} {
		f.write(f.root, repo, "user_profile.md", profileNote)
	}
	f.write(f.root, "-repo-d", "career_notes.md", "career plans")

	hits, err := f.svc.Search(context.Background(), "career", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 {
		t.Fatalf("want one collapsed hit plus the other note, got %+v", hits)
	}
	var collapsed *native.Hit
	for i := range hits {
		if hits[i].Name == "User profile" {
			collapsed = &hits[i]
		}
	}
	if collapsed == nil || len(collapsed.AlsoIn) != 2 {
		t.Fatalf("collapsed hit should list the other two repos: %+v", hits)
	}
	if _, err := f.svc.Get(context.Background(), collapsed.AlsoIn[0].ID); err != nil {
		t.Errorf("a collapsed note must still open: %v", err)
	}
}

func TestSearchAndGetStayInsideTheConfiguredRoots(t *testing.T) {
	f := newFixture(t)
	otherRoot := t.TempDir()
	f.write(f.root, "-personal", "user_profile.md", profileNote)
	f.write(otherRoot, "-client", "client_secret_plans.md", "career of the client's founder")

	other := native.NewService(native.Config{Store: f.store, Roots: []string{otherRoot}})
	if _, err := other.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.sync()

	hits, err := f.svc.Search(context.Background(), "career", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Name != "User profile" {
		t.Fatalf("search crossed into another root: %+v", hits)
	}

	otherHits, err := other.Search(context.Background(), "career", 5)
	if err != nil || len(otherHits) != 1 {
		t.Fatalf("other root search: %v %+v", err, otherHits)
	}
	if _, err := f.svc.Get(context.Background(), otherHits[0].ID); err == nil {
		t.Error("get crossed into another root")
	}
	if notes, repos, err := f.svc.Counts(context.Background()); err != nil || notes != 1 || repos != 1 {
		t.Errorf("counts crossed roots: %d %d %v", notes, repos, err)
	}

	// Syncing one root must not delete the other's rows.
	if _, err := f.svc.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if hits, _ := other.Search(context.Background(), "career", 5); len(hits) != 1 {
		t.Errorf("syncing one root removed another's notes: %+v", hits)
	}
}

func TestSearchRescansOnlyAfterTheThrottleWindow(t *testing.T) {
	f := newFixture(t)
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	svc := native.NewService(native.Config{
		Store: f.store, Roots: []string{f.root},
		Clock: func() time.Time { return now },
	})
	f.write(f.root, "-repo", "first.md", "lighthouse")
	if hits, err := svc.Search(context.Background(), "lighthouse", 5); err != nil || len(hits) != 1 {
		t.Fatalf("first search should sync: %v %+v", err, hits)
	}

	f.write(f.root, "-repo", "second.md", "lighthouse keeper")
	now = now.Add(time.Minute)
	if hits, _ := svc.Search(context.Background(), "keeper", 5); len(hits) != 0 {
		t.Fatalf("searched inside the window should not rescan: %+v", hits)
	}
	now = now.Add(5 * time.Minute)
	if hits, _ := svc.Search(context.Background(), "keeper", 5); len(hits) != 1 {
		t.Fatalf("search after the window should rescan: %+v", hits)
	}
}

func TestDefaultRoots(t *testing.T) {
	cases := []struct {
		name       string
		configured []string
		env, home  string
		want       []string
	}{
		{"configured wins", []string{"/x"}, "/env", "/home", []string{"/x"}},
		{"env next", nil, "/env", "/home", []string{"/env"}},
		{"home fallback", nil, "", "/home", []string{filepath.Join("/home", ".claude")}},
		{"nothing known", nil, "", "", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := native.DefaultRoots(c.configured, c.env, c.home)
			if strings.Join(got, ",") != strings.Join(c.want, ",") {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
}
