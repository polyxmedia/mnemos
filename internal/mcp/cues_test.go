package mcp_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/polyxmedia/mnemos/internal/mcp"
)

// fakeCues records what the server attaches.
type fakeCues struct {
	got map[string][]string
}

func (f *fakeCues) Add(_ context.Context, id string, cues []string, _ time.Time) error {
	if f.got == nil {
		f.got = map[string][]string{}
	}
	f.got[id] = append(f.got[id], cues...)
	return nil
}

func withCues(f *fakeCues) func(*mcp.Config) {
	return func(c *mcp.Config) { c.Cues = f }
}

func savedID(t *testing.T, text string) (string, []any) {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("decode %q: %v", text, err)
	}
	id, _ := out["id"].(string)
	if id == "" {
		t.Fatalf("no id in %q", text)
	}
	cues, _ := out["cues"].([]any)
	return id, cues
}

func TestWriteToolsAttachCanonicalCues(t *testing.T) {
	cases := []struct {
		tool string
		args map[string]any
	}{
		{"mnemos_save", map[string]any{"title": "commit rule", "content": "no attribution lines", "type": "convention"}},
		{"mnemos_correct", map[string]any{"title": "commit trailer", "tried": "added a trailer", "wrong_because": "commits read as human", "fix": "leave it out"}},
		{"mnemos_convention", map[string]any{"title": "commit style", "rule": "no attribution", "rationale": "house style"}},
	}
	for _, c := range cases {
		t.Run(c.tool, func(t *testing.T) {
			f := &fakeCues{}
			h := newHarnessWith(t, withCues(f))
			c.args["cues"] = []string{"CMD:git   commit", "path:**/migrations/*.sql"}
			res, text := h.call(c.tool, c.args)
			if res.IsError {
				t.Fatalf("%s failed: %s", c.tool, text)
			}
			id, returned := savedID(t, text)
			want := []string{"cmd:git commit", "path:**/migrations/*.sql"}
			if len(f.got[id]) != 2 || f.got[id][0] != want[0] || f.got[id][1] != want[1] {
				t.Errorf("stored cues = %v, want %v", f.got[id], want)
			}
			if len(returned) != 2 {
				t.Errorf("result should echo the cues, got %v", returned)
			}
		})
	}
}

func TestInvalidCueRejectsTheWholeSave(t *testing.T) {
	f := &fakeCues{}
	h := newHarnessWith(t, withCues(f))
	res, err := h.client.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name: "mnemos_save",
		Arguments: map[string]any{
			"title": "x", "content": "y", "type": "convention",
			"cues": []string{"cmd:git commit", "shell:git push"},
		},
	})
	if err == nil && !res.IsError {
		t.Fatal("an unknown cue kind must fail the call")
	}
	if len(f.got) != 0 {
		t.Errorf("nothing should be attached when a cue is invalid: %v", f.got)
	}
	_, text := h.call("mnemos_search", map[string]any{"query": "x"})
	var out map[string]any
	_ = json.Unmarshal([]byte(text), &out)
	if results, _ := out["results"].([]any); len(results) != 0 {
		t.Errorf("the memory must not be saved when its cues are invalid: %s", text)
	}
}

func TestSearchAndGetLoadWithTheSession(t *testing.T) {
	h := newHarness(t)
	res, err := h.client.ListTools(context.Background(), &mcpsdk.ListToolsParams{})
	if err != nil {
		t.Fatal(err)
	}
	always := map[string]bool{}
	for _, tool := range res.Tools {
		if v, _ := tool.Meta["anthropic/alwaysLoad"].(bool); v {
			always[tool.Name] = true
		}
	}
	if !always["mnemos_search"] || !always["mnemos_get"] {
		t.Errorf("mnemos_search and mnemos_get must carry alwaysLoad, got %v", always)
	}
	if len(always) != 2 {
		t.Errorf("only the two read tools should skip deferral, got %v", always)
	}
}
