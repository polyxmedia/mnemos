package storage

import (
	"database/sql"
	"strings"
	"time"
)

func nullableStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullableTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC()
}

func nullableTimePtr(t sql.NullTime) *time.Time {
	if !t.Valid {
		return nil
	}
	v := t.Time
	return &v
}

// jsonQuote turns a tag into the substring we search for inside the
// JSON-encoded tags column (e.g. tag "go" → `"go"`).
func jsonQuote(s string) string {
	b := strings.Builder{}
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// ftsEscape prepares a raw user query for FTS5 MATCH. Bare words are ORed;
// FTS5 operators from the caller are dropped to avoid injection. Each token
// is wrapped as a prefix query to match partial words.
func ftsEscape(q string) string {
	var out []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() == 0 {
			return
		}
		tok := cur.String()
		cur.Reset()
		if tok == "" {
			return
		}
		out = append(out, `"`+strings.ReplaceAll(tok, `"`, `""`)+`"`+`*`)
	}
	for _, r := range q {
		switch {
		case r == ' ' || r == '\t' || r == '\n':
			flush()
		case r == '"' || r == '(' || r == ')' || r == ':' || r == '*':
			// drop operator-ish characters from user input
		default:
			cur.WriteRune(r)
		}
	}
	flush()
	if len(out) == 0 {
		return `""`
	}
	return strings.Join(out, " OR ")
}
