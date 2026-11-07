package native

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestParseNote(t *testing.T) {
	cases := []struct {
		name string
		text string
		want noteMeta
		body string
	}{
		{
			name: "top level fields",
			text: "---\nname: Profile\ndescription: \"Who they are\"\ntype: user\n---\n\nBody here\n",
			want: noteMeta{name: "Profile", description: "Who they are", docType: "user"},
			body: "Body here",
		},
		{
			name: "nested metadata type",
			text: "---\nname: rule\nmetadata:\n  type: feedback\n---\nBody",
			want: noteMeta{name: "rule", docType: "feedback"},
			body: "Body",
		},
		{
			name: "node_type is not type",
			text: "---\nname: x\nnode_type: memory\ntype: project\n---\nBody",
			want: noteMeta{name: "x", docType: "project"},
			body: "Body",
		},
		{
			name: "no frontmatter",
			text: "# Heading\n\nBody\n",
			want: noteMeta{},
			body: "# Heading\n\nBody",
		},
		{
			name: "unterminated frontmatter is all body",
			text: "---\nname: x\nno closing fence",
			want: noteMeta{},
			body: "---\nname: x\nno closing fence",
		},
		{
			name: "windows line endings",
			text: "---\r\nname: x\r\n---\r\nBody\r\n",
			want: noteMeta{name: "x"},
			body: "Body",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, body := parseNote(c.text)
			if got != c.want {
				t.Errorf("meta = %+v, want %+v", got, c.want)
			}
			if body != c.body {
				t.Errorf("body = %q, want %q", body, c.body)
			}
		})
	}
}

func TestMeaningfulTerms(t *testing.T) {
	cases := []struct{ in, want string }{
		{"tell me about my career", "career"},
		{"how much do you remember about me", "how much do you remember about me"},
		{"acme.dev stripe accounts", "acme dev stripe accounts"},
		{"José's career", "José career"},
		{"", ""},
	}
	for _, c := range cases {
		if got := meaningfulTerms(c.in); got != c.want {
			t.Errorf("meaningfulTerms(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestRepoName(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("encoded names are decoded against POSIX paths")
	}
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{"Code/acme", "Code/acme-video", "Code/site.online", "Code/app/.git"} {
		if err := os.MkdirAll(filepath.Join(base, filepath.FromSlash(dir)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	prefix := flattenPath(base)
	cases := []struct{ name, encoded, want string }{
		{"dashed repo name resolves whole", prefix + "-Code-acme-video", "acme-video"},
		{"shorter sibling resolves", prefix + "-Code-acme", "acme"},
		{"dot in repo name", prefix + "-Code-site-online", "site.online"},
		{"missing leading dash still walks", strings.TrimLeft(prefix, "-") + "-Code-acme", "acme"},
		{"deleted repo keeps the unmatched part", prefix + "-Code-old-tool", "old-tool"},
		{"deleted repo beside a live one keeps both", prefix + "-Code-app-latest", "app-latest"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := repoName(c.encoded); got != c.want {
				t.Errorf("repoName(%q) = %q, want %q", c.encoded, got, c.want)
			}
		})
	}
}

func flattenPath(path string) string {
	var b strings.Builder
	for _, seg := range strings.Split(filepath.ToSlash(path), "/") {
		if seg != "" {
			b.WriteString("-" + flatten(seg))
		}
	}
	return b.String()
}
