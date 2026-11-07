package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRepoRoot(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	mkdir := func(parts ...string) string {
		t.Helper()
		dir := filepath.Join(append([]string{base}, parts...)...)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	write := func(path, content string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	repo := mkdir("code", "repo")
	mkdir("code", "repo", ".git", "worktrees", "feature")
	sub := mkdir("code", "repo", "cmd", "tool")

	// A linked worktree outside the repo, as git worktree add makes.
	linked := mkdir("elsewhere", "repo-feature")
	write(filepath.Join(linked, ".git"), "gitdir: "+filepath.Join(repo, ".git", "worktrees", "feature")+"\n")
	linkedSub := mkdir("elsewhere", "repo-feature", "pkg")

	// A worktree inside the repo with a relative gitdir.
	nested := mkdir("code", "repo", ".claude", "worktrees", "fix")
	write(filepath.Join(nested, ".git"), "gitdir: ../../../.git/worktrees/feature\n")

	// A submodule is its own project.
	submodule := mkdir("code", "repo", "vendor", "lib")
	write(filepath.Join(submodule, ".git"), "gitdir: ../../.git/modules/lib\n")

	// A dotfiles repo at home must not swallow every folder under home.
	home := mkdir("home")
	mkdir("home", ".git")
	homeFolder := mkdir("home", "notes", "ideas")

	plain := mkdir("plain", "folder")

	cases := []struct {
		name, dir, home, want string
	}{
		{"repo root", repo, "", repo},
		{"subfolder resolves to the repo", sub, "", repo},
		{"linked worktree resolves to the main checkout", linked, "", repo},
		{"subfolder of a linked worktree", linkedSub, "", repo},
		{"relative gitdir", nested, "", repo},
		{"submodule stays itself", submodule, "", submodule},
		{"folder under a dotfiles home stays itself", homeFolder, home, homeFolder},
		{"home itself stays itself", home, home, home},
		{"no repo stays itself", plain, "", plain},
		{"filesystem root", string(filepath.Separator), "", string(filepath.Separator)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := repoRoot(c.dir, c.home); got != c.want {
				t.Errorf("repoRoot(%q, %q) = %q, want %q", c.dir, c.home, got, c.want)
			}
		})
	}
}

func TestProjectFromCWDEmpty(t *testing.T) {
	if got := projectFromCWD(""); got != "" {
		t.Errorf("got %q", got)
	}
}

func TestIsAgentNoteFile(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"/Users/a/.claude/projects/-Users-a-Code-x/memory/feedback_x.md", true},
		{"/Users/a/.claude-personal/projects/-Users-a-Code-x/memory/MEMORY.md", true},
		{"/Users/a/.claude/plans/some-plan.md", true},
		{"/private/tmp/claude-501/-Users-a-Code-x/abc/scratchpad/notes.py", true},
		{"/Users/a/Code/x/internal/memory/service.go", false},
		{"/Users/a/Code/x/docs/memory/README.md", false},
		{"/Users/a/Code/x/.claude/skills/thing/SKILL.md", false},
		{"/Users/a/Code/x/scratchpad/app.go", false},
		{"/Users/a/Code/claude-tools/scratchpad/app.go", false},
		{"/Users/a/Code/x/projects/y/memory/app.go", false},
	}
	for _, c := range cases {
		t.Run(c.path, func(t *testing.T) {
			if got := isAgentNoteFile(c.path); got != c.want {
				t.Errorf("isAgentNoteFile(%q) = %v, want %v", c.path, got, c.want)
			}
		})
	}
}
