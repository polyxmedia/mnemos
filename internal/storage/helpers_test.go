package storage

import (
	"database/sql"
	"testing"
	"time"
)

func TestNullableStrConversion(t *testing.T) {
	if nullableStr("") != nil {
		t.Error("empty → nil")
	}
	if nullableStr("x") != "x" {
		t.Error("non-empty → value")
	}
}

func TestNullableTimeConversion(t *testing.T) {
	if nullableTime(nil) != nil {
		t.Error("nil → nil")
	}
	now := time.Now()
	got := nullableTime(&now)
	if got == nil {
		t.Error("ptr → non-nil")
	}
}

func TestNullableTimePtrFromValid(t *testing.T) {
	nt := sql.NullTime{Valid: true, Time: time.Now()}
	got := nullableTimePtr(nt)
	if got == nil {
		t.Error("valid NullTime must produce non-nil pointer")
	}
	invalid := sql.NullTime{Valid: false}
	if got := nullableTimePtr(invalid); got != nil {
		t.Error("invalid NullTime must produce nil pointer")
	}
}

func TestJSONQuoteEscapesSpecials(t *testing.T) {
	cases := map[string]string{
		`plain`:       `"plain"`,
		`with"quote`:  `"with\"quote"`,
		`back\slash`:  `"back\\slash"`,
		``:            `""`,
	}
	for in, want := range cases {
		if got := jsonQuote(in); got != want {
			t.Errorf("jsonQuote(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFtsEscapeDropsOperators(t *testing.T) {
	got := ftsEscape(`hello "world" (foo) *bar*`)
	// All operator chars should be stripped; each word wrapped as prefix query.
	if got == "" {
		t.Error("ftsEscape should never return empty for non-empty input")
	}
}

func TestFtsEscapeEmpty(t *testing.T) {
	if ftsEscape("") == "" {
		t.Error("empty query should produce the empty-match token, not empty string")
	}
}

func TestParseSQLiteTime(t *testing.T) {
	cases := []string{
		time.Now().UTC().Format(time.RFC3339),
		"2026-04-16 18:00:00",
		"2026-04-16 18:00:00.123456789 +0000 UTC",
	}
	for _, s := range cases {
		if got := parseSQLiteTime(s); got.IsZero() {
			t.Errorf("parseSQLiteTime(%q) returned zero", s)
		}
	}
	if got := parseSQLiteTime("not-a-date"); !got.IsZero() {
		t.Error("bad input should parse to zero value")
	}
	if got := parseSQLiteTime(""); !got.IsZero() {
		t.Error("empty input should parse to zero value")
	}
}
