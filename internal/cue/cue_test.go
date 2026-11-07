package cue

import (
	"errors"
	"testing"
)

func TestParse(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{in: "cmd:git   push  --force", want: "cmd:git push --force"},
		{in: " CMD: gh pr create ", want: "cmd:gh pr create"},
		{in: "path:**/migrations/*.sql", want: "path:**/migrations/*.sql"},
		{in: "cmd:", wantErr: true},
		{in: "git push", wantErr: true},
		{in: "tool:Bash", wantErr: true},
	}
	for _, tc := range cases {
		c, err := Parse(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("Parse(%q) = %q, want error", tc.in, c)
			}
			continue
		}
		if err != nil {
			t.Fatalf("Parse(%q): %v", tc.in, err)
		}
		if c.String() != tc.want {
			t.Errorf("Parse(%q) = %q, want %q", tc.in, c, tc.want)
		}
	}
	if _, err := Parse("cmd:   "); !errors.Is(err, ErrEmpty) {
		t.Errorf("blank pattern: got %v, want ErrEmpty", err)
	}
}

func TestMatchCommand(t *testing.T) {
	cases := []struct {
		cue     string
		command string
		want    bool
	}{
		{"cmd:git push --force", "git push --force", true},
		{"cmd:git push --force", "git push origin main --force", true},
		{"cmd:git push --force", "cd repo && git push origin main --force", true},
		{"cmd:git push --force", "git push --force-with-lease", false},
		{"cmd:git push --force", "echo git push --force", false},
		{"cmd:git push --force", "git push origin main", false},
		{"cmd:git push --force", `git commit -m "then git push --force"`, false},
		{"cmd:git commit", "git commit -m 'x'", true},
		{"cmd:git commit", "GIT_AUTHOR_NAME=x git commit -m y", true},
		{"cmd:git commit", "sudo git commit", true},
		{"cmd:git commit", "/usr/bin/git commit", true},
		{"cmd:git commit", "git status; git commit -am wip", true},
		{"cmd:git commit", "git log | grep commit", false},
		{"cmd:git commit", "git log --grep commit", false},
		{"cmd:git commit", "git checkout -b commit-fix", false},
		{"cmd:git commit", "git --no-pager commit -m x", true},
		{"cmd:git commit", "git -C repo commit -m x", true},
		{"cmd:git commit", "git show HEAD/commit", false},
		{"cmd:docker compose up", "docker compose -f dev.yml up -d", true},
		{"cmd:docker compose up", "docker compose logs up", false},
		{"cmd:git checkout -b", "git checkout -b feature/AIR-1", true},
		{"cmd:git checkout -b", "git checkout main", false},
		{"cmd:gh pr edit --add-reviewer", "gh pr edit 12 --add-reviewer=copilot", true},
		{"cmd:gh pr edit --add-reviewer", "gh pr edit 12 --add-reviewer copilot", true},
		{"cmd:heroku", "heroku login", true},
		{"cmd:gh pr edit --add-reviewer*", "gh pr edit 12 --add-reviewer=copilot", true},
		{"cmd:gh pr edit --add-reviewer*", "gh pr edit 12 --title x", false},
		{"cmd:git commit", "", false},
	}
	for _, tc := range cases {
		c, err := Parse(tc.cue)
		if err != nil {
			t.Fatalf("Parse(%q): %v", tc.cue, err)
		}
		if got := c.MatchCommand(tc.command); got != tc.want {
			t.Errorf("%s on %q = %v, want %v", tc.cue, tc.command, got, tc.want)
		}
	}
}

func TestMatchPath(t *testing.T) {
	cases := []struct {
		cue  string
		path string
		want bool
	}{
		{"path:**/migrations/*.sql", "internal/storage/migrations/0009_x.sql", true},
		{"path:**/migrations/*.sql", "migrations/0001_init.sql", true},
		{"path:**/migrations/*.sql", "internal/storage/migrations.go", false},
		{"path:internal/storage/migrations/**", "internal/storage/migrations/0001.sql", true},
		{"path:*.php", "app/Http/Controllers/HomeController.php", true},
		{"path:*.php", "app/Http/Controllers/HomeController.phpx", false},
		{"path:resources/views/emails/*", "resources/views/emails/welcome.blade.php", true},
		{"path:resources/views/emails/*", "resources/views/emails/layouts/base.blade.php", false},
		{"path:*.php", "", false},
	}
	for _, tc := range cases {
		c, err := Parse(tc.cue)
		if err != nil {
			t.Fatalf("Parse(%q): %v", tc.cue, err)
		}
		if got := c.MatchPath(tc.path); got != tc.want {
			t.Errorf("%s on %q = %v, want %v", tc.cue, tc.path, got, tc.want)
		}
	}
}

func TestKindsDoNotCross(t *testing.T) {
	cmd, _ := Parse("cmd:git commit")
	path, _ := Parse("path:*.go")
	if cmd.MatchPath("git commit") || path.MatchCommand("main.go") {
		t.Fatal("a cue matched an action of the other kind")
	}
}
