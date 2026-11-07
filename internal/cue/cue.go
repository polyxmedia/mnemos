// Package cue matches a memory's trigger against the action an agent is
// about to take. A cue is written as "cmd:<words>" for a shell command or
// "path:<glob>" for a file about to be edited:
//
//	cmd:git commit                 fires on "git commit -m x", not "git log --grep commit"
//	cmd:gh pr edit --add-reviewer  fires on any gh pr edit that adds a reviewer
//	path:**/migrations/*.sql       fires on an edit to db/migrations/0009_x.sql
//	path:*.php                     fires on an edit to any PHP file
//
// In a cmd: cue the first word is the program, each later plain word is
// the next positional argument (flags and a flag's value are skipped), and
// a word starting with "-" is a flag that may appear anywhere after the
// program, also in its --flag=value form.
//
// Matching is deterministic and needs no score, which is the point: the
// rule arrives at the moment it applies, every time, including after a
// compaction has dropped everything the session was told earlier.
package cue

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// Kind names what a cue matches against.
type Kind string

const (
	// KindCmd matches a shell command the agent is about to run.
	KindCmd Kind = "cmd"
	// KindPath matches a file the agent is about to edit.
	KindPath Kind = "path"
)

// maxCueLen bounds a cue so a pasted paragraph cannot become a trigger.
const maxCueLen = 200

// Cue is one parsed trigger.
type Cue struct {
	Kind Kind
	// Pattern is the text after the prefix, whitespace-normalised.
	Pattern string
	words   []string
	path    *regexp.Regexp
	base    bool
}

// String returns the canonical stored form, for example "cmd:git commit".
func (c Cue) String() string { return string(c.Kind) + ":" + c.Pattern }

// ErrEmpty is returned for a cue with nothing after its prefix.
var ErrEmpty = errors.New("cue: empty pattern")

// Parse validates and normalises a cue.
func Parse(s string) (Cue, error) {
	s = strings.TrimSpace(s)
	if len(s) > maxCueLen {
		return Cue{}, fmt.Errorf("cue: longer than %d characters", maxCueLen)
	}
	kind, pattern, ok := strings.Cut(s, ":")
	if !ok {
		return Cue{}, fmt.Errorf("cue %q: want cmd:<words> or path:<glob>", s)
	}
	pattern = strings.Join(strings.Fields(pattern), " ")
	if pattern == "" {
		return Cue{}, ErrEmpty
	}
	c := Cue{Kind: Kind(strings.ToLower(strings.TrimSpace(kind))), Pattern: pattern}
	switch c.Kind {
	case KindCmd:
		c.words = strings.Fields(pattern)
	case KindPath:
		c.Pattern = filepath.ToSlash(pattern)
		re, err := globRegexp(c.Pattern)
		if err != nil {
			return Cue{}, fmt.Errorf("cue %q: %w", s, err)
		}
		c.path = re
		// A glob without a slash matches the file name anywhere, the way a
		// .gitignore line does, so "*.php" covers every PHP file.
		c.base = !strings.Contains(c.Pattern, "/")
	default:
		return Cue{}, fmt.Errorf("cue %q: unknown kind %q, want cmd or path", s, c.Kind)
	}
	return c, nil
}

// MatchCommand reports whether a shell command fires this cue. The command
// is split into simple commands on ;, &&, || and |, and the cue fires when
// one of them contains every cue word in order, with the first cue word as
// that command's program. "cmd:git push --force" therefore fires on
// "cd x && git push origin main --force" but not on "git push
// --force-with-lease" (the safe variant) or "echo git push --force".
// A cue word may use * and ? as in a file glob.
func (c Cue) MatchCommand(command string) bool {
	if c.Kind != KindCmd || len(c.words) == 0 {
		return false
	}
	for _, simple := range splitCommands(command) {
		if matchWords(c.words, simple) {
			return true
		}
	}
	return false
}

// MatchPath reports whether a file path fires this cue. rel is the path
// relative to the project root when it lies inside the project, else the
// absolute path.
func (c Cue) MatchPath(rel string) bool {
	if c.Kind != KindPath || c.path == nil || rel == "" {
		return false
	}
	rel = filepath.ToSlash(rel)
	if c.path.MatchString(rel) {
		return true
	}
	if c.base {
		return c.path.MatchString(rel[strings.LastIndex(rel, "/")+1:])
	}
	return false
}

// splitCommands breaks a command line into simple commands, each as words.
// Quoting is honoured just enough that a separator inside quotes stays put.
func splitCommands(command string) [][]string {
	var (
		out     [][]string
		words   []string
		cur     strings.Builder
		quote   rune
		escaped bool
	)
	flushWord := func() {
		if cur.Len() > 0 {
			words = append(words, cur.String())
			cur.Reset()
		}
	}
	flushCmd := func() {
		flushWord()
		if len(words) > 0 {
			out = append(out, words)
			words = nil
		}
	}
	runes := []rune(command)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		switch {
		case escaped:
			cur.WriteRune(r)
			escaped = false
		case r == '\\' && quote != '\'':
			escaped = true
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote = r
		case r == ';' || r == '|' || r == '\n' || r == '(' || r == ')':
			flushCmd()
		case r == '&':
			// "&&" separates commands; a lone "&" backgrounds one.
			flushCmd()
		case r == ' ' || r == '\t':
			flushWord()
		default:
			cur.WriteRune(r)
		}
	}
	flushCmd()
	return out
}

// matchWords reports whether a simple command matches a cue's words.
// want[0] matches the program, after any VAR=value assignments and a
// leading sudo, env, command, exec or time, and may be called by path.
// Each later plain word must be the next positional argument: flags are
// skipped, and so is one value right after a flag, so "docker compose -f
// x.yml up" matches "docker compose up" while "git log --grep commit" does
// not match "git commit". A word starting with "-" is a flag and may appear
// anywhere after the program.
func matchWords(want, words []string) bool {
	start := 0
	for start < len(words) {
		w := words[start]
		if strings.Contains(w, "=") && !strings.HasPrefix(w, "-") && !strings.HasPrefix(w, "=") {
			start++
			continue
		}
		if w == "sudo" || w == "env" || w == "command" || w == "exec" || w == "time" {
			start++
			continue
		}
		break
	}
	if start >= len(words) || !programMatch(want[0], words[start]) {
		return false
	}
	args := words[start+1:]
	pos := 0
	for _, w := range want[1:] {
		if isFlag(w) {
			if !anyFlagMatch(w, args) {
				return false
			}
			continue
		}
		next, ok := nextPositional(w, args, pos)
		if !ok {
			return false
		}
		pos = next
	}
	return true
}

// nextPositional matches pattern against the next positional argument in
// args[pos:] and returns the index after it.
func nextPositional(pattern string, args []string, pos int) (int, bool) {
	for i := pos; i < len(args); i++ {
		a := args[i]
		if isFlag(a) {
			continue
		}
		if wordMatch(pattern, a) {
			return i + 1, true
		}
		if i > 0 && isFlag(args[i-1]) {
			continue // most likely the value of the flag before it
		}
		return 0, false
	}
	return 0, false
}

func isFlag(s string) bool { return len(s) > 1 && s[0] == '-' }

// anyFlagMatch reports whether a flag in args matches pattern, either whole
// or by its name in the --flag=value form.
func anyFlagMatch(pattern string, args []string) bool {
	for _, a := range args {
		if !isFlag(a) {
			continue
		}
		if wordMatch(pattern, a) {
			return true
		}
		if name, _, ok := strings.Cut(a, "="); ok && wordMatch(pattern, name) {
			return true
		}
	}
	return false
}

// programMatch is wordMatch that also takes the program by path:
// /usr/bin/git is git.
func programMatch(pattern, word string) bool {
	if wordMatch(pattern, word) {
		return true
	}
	return !strings.ContainsAny(pattern, "*?[/") && strings.HasSuffix(word, "/"+pattern)
}

func wordMatch(pattern, word string) bool {
	if pattern == word {
		return true
	}
	if !strings.ContainsAny(pattern, "*?[") {
		return false
	}
	ok, err := filepath.Match(pattern, word)
	return err == nil && ok
}

// globRegexp converts a glob to an anchored regexp: ** spans directories,
// * and ? stay within one path segment.
func globRegexp(glob string) (*regexp.Regexp, error) {
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(glob); i++ {
		ch := glob[i]
		switch ch {
		case '*':
			if i+1 < len(glob) && glob[i+1] == '*' {
				i++
				if i+1 < len(glob) && glob[i+1] == '/' {
					// "**/" matches zero or more whole directories.
					i++
					b.WriteString("(?:.*/)?")
				} else {
					b.WriteString(".*")
				}
			} else {
				b.WriteString("[^/]*")
			}
		case '?':
			b.WriteString("[^/]")
		default:
			b.WriteString(regexp.QuoteMeta(string(ch)))
		}
	}
	b.WriteString("$")
	return regexp.Compile(b.String())
}
