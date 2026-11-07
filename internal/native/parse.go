package native

import (
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

type noteMeta struct {
	name, description, docType string
}

// parseNote splits a note into frontmatter fields and body. It reads the
// three fields it needs line by line, tolerating the top-level and nested
// metadata shapes both found in real notes. A note without frontmatter is all
// body. Keys are matched whole, so node_type does not read as type.
func parseNote(text string) (noteMeta, string) {
	var meta noteMeta
	normalized := strings.ReplaceAll(text, "\r\n", "\n")
	rest, ok := strings.CutPrefix(normalized, "---\n")
	if !ok {
		return meta, strings.TrimSpace(normalized)
	}
	head, body, ok := strings.Cut(rest, "\n---")
	if !ok {
		return meta, strings.TrimSpace(normalized)
	}
	for _, line := range strings.Split(head, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok {
			continue
		}
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		switch key {
		case "name":
			if meta.name == "" {
				meta.name = value
			}
		case "description":
			if meta.description == "" {
				meta.description = value
			}
		case "type":
			if meta.docType == "" {
				meta.docType = value
			}
		}
	}
	return meta, strings.TrimSpace(strings.TrimPrefix(body, "\n"))
}

// repoName recovers a readable repo name from a notes directory name. Claude
// Code flattens the repo path by replacing separators and dots with dashes,
// which cannot be reversed from the string alone because repo names contain
// dashes too, so this walks the filesystem to see which reading exists. A repo
// that has since been deleted keeps whatever the walk could not account for:
// "-Users-a-Code-old-tool" reads as "old-tool" with ~/Code still there. When
// the walk stopped inside another repo, as with a deleted "app-latest" beside a
// live "app", the name is put back together.
func repoName(encoded string) string {
	deepest, unmatched := walkEncoded(encoded)
	if unmatched == "" {
		return filepath.Base(deepest)
	}
	if _, err := os.Stat(filepath.Join(deepest, ".git")); err == nil {
		return filepath.Base(deepest) + unmatched
	}
	return strings.TrimLeft(unmatched, "-")
}

// flatten applies Claude Code's path encoding to one path segment: anything
// outside ASCII letters and digits becomes a dash.
func flatten(name string) string {
	return strings.Map(func(r rune) rune {
		if r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r)) {
			return r
		}
		return '-'
	}, name)
}

// walkEncoded descends from the filesystem root one directory at a time, into
// names whose flattened form continues the encoded path. It returns the deepest
// directory reached and the part of encoded it could not match. The longest
// matching name wins each step, so "acme-video" beats "acme".
func walkEncoded(encoded string) (deepest, unmatched string) {
	cur := string(filepath.Separator)
	remaining := encoded
	if !strings.HasPrefix(remaining, "-") {
		remaining = "-" + remaining
	}
	for remaining != "" {
		entries, err := os.ReadDir(cur)
		if err != nil {
			return cur, remaining
		}
		next, rest := "", ""
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			candidate := "-" + flatten(e.Name())
			switch {
			case remaining == candidate:
				return filepath.Join(cur, e.Name()), ""
			case strings.HasPrefix(remaining, candidate+"-") && len(e.Name()) > len(next):
				next, rest = e.Name(), remaining[len(candidate):]
			}
		}
		if next == "" {
			return cur, remaining
		}
		cur, remaining = filepath.Join(cur, next), rest
	}
	return cur, ""
}

// meaningfulTerms strips stop words and single letters from a query. A query
// made only of them keeps its words rather than searching on nothing.
//
// Stop words carry no signal in a recall query. Every term becomes an OR'd
// prefix match, so "me" also matches "memory" and "method", and a question like
// "how much do you remember about me" ranked on filler.
func meaningfulTerms(query string) string {
	stopWords := map[string]bool{
		"a": true, "about": true, "all": true, "am": true, "an": true, "and": true,
		"any": true, "are": true, "as": true, "at": true, "be": true, "been": true,
		"but": true, "by": true, "can": true, "could": true, "did": true, "do": true,
		"does": true, "for": true, "from": true, "had": true, "has": true, "have": true,
		"how": true, "i": true, "if": true, "in": true, "into": true, "is": true,
		"it": true, "its": true, "just": true, "know": true, "me": true, "more": true,
		"much": true, "my": true, "not": true, "of": true, "on": true, "or": true,
		"our": true, "remember": true, "should": true, "so": true, "some": true,
		"tell": true, "that": true, "the": true, "their": true, "them": true,
		"there": true, "these": true, "they": true, "this": true, "to": true, "us": true,
		"was": true, "we": true, "were": true, "what": true, "when": true, "where": true,
		"which": true, "who": true, "why": true, "will": true, "with": true, "would": true,
		"you": true, "your": true,
	}
	words := strings.FieldsFunc(query, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' && r != '-'
	})
	kept := make([]string, 0, len(words))
	for _, w := range words {
		lower := strings.ToLower(w)
		if !stopWords[lower] && len(lower) > 1 {
			kept = append(kept, w)
		}
	}
	if len(kept) == 0 {
		return strings.Join(words, " ")
	}
	return strings.Join(kept, " ")
}
