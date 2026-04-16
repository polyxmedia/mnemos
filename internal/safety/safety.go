// Package safety scans observation content for prompt-injection patterns
// before injection into agent context. This is defence-in-depth: memory
// stores are a new attack surface (promptware), and naive injection is
// dangerous. The scanner is conservative — it flags rather than deletes.
package safety

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

// Risk is the severity of a detected pattern.
type Risk int

const (
	RiskNone Risk = iota
	RiskLow
	RiskMedium
	RiskHigh
)

// String returns a short label for the risk level.
func (r Risk) String() string {
	switch r {
	case RiskHigh:
		return "high"
	case RiskMedium:
		return "medium"
	case RiskLow:
		return "low"
	default:
		return "none"
	}
}

// Finding is one detected pattern.
type Finding struct {
	Rule    string
	Risk    Risk
	Snippet string
}

// Report bundles the findings and the highest observed risk.
type Report struct {
	Findings  []Finding
	MaxRisk   Risk
	Sanitised string
}

// Scanner detects common prompt-injection patterns in text intended for
// injection into an LLM context. It does not parse natural language; it
// matches structural patterns that legitimate memory content almost never
// needs (explicit instruction spoofing, zero-width unicode, hidden bidi
// overrides, fake tool-call syntax). Tuned for high precision.
type Scanner struct {
	rules []rule
}

// NewScanner returns a Scanner with the default rule set.
func NewScanner() *Scanner {
	return &Scanner{rules: defaultRules()}
}

// Scan returns findings for text. If the highest risk is above threshold,
// callers should refuse injection or mark the content with a visible
// [MNEMOS: FLAGGED] banner.
func (s *Scanner) Scan(text string) Report {
	var report Report
	for _, r := range s.rules {
		for _, match := range r.find(text) {
			f := Finding{Rule: r.name, Risk: r.risk, Snippet: truncate(match, 80)}
			report.Findings = append(report.Findings, f)
			if r.risk > report.MaxRisk {
				report.MaxRisk = r.risk
			}
		}
	}
	report.Sanitised = sanitise(text)
	return report
}

// WrapFlagged prepends a visible marker to text that exceeded the risk
// threshold, so the agent sees it was flagged rather than silently
// absorbing potentially-malicious instructions.
func WrapFlagged(text string, report Report) string {
	if report.MaxRisk == RiskNone {
		return text
	}
	rules := make([]string, 0, len(report.Findings))
	seen := map[string]bool{}
	for _, f := range report.Findings {
		if seen[f.Rule] {
			continue
		}
		seen[f.Rule] = true
		rules = append(rules, f.Rule)
	}
	return fmt.Sprintf(
		"[MNEMOS: FLAGGED risk=%s rules=%s] %s",
		report.MaxRisk, strings.Join(rules, ","), text,
	)
}

// --- rules --------------------------------------------------------------

type rule struct {
	name string
	risk Risk
	re   *regexp.Regexp
}

func (r rule) find(text string) []string {
	if r.re == nil {
		return nil
	}
	return r.re.FindAllString(text, -1)
}

func defaultRules() []rule {
	return []rule{
		{
			name: "instruction-override",
			risk: RiskHigh,
			re: regexp.MustCompile(`(?i)(ignore\s+(all\s+)?previous|disregard\s+the\s+above|forget\s+(everything|all))`),
		},
		{
			name: "system-role-spoof",
			risk: RiskHigh,
			re: regexp.MustCompile(`(?i)(system\s*:|you\s+are\s+now|from\s+now\s+on\s+you\s+are)`),
		},
		{
			name: "fake-tool-call",
			risk: RiskMedium,
			re: regexp.MustCompile(`<\s*(tool_use|tool_call|function_calls?)\b`),
		},
		{
			name: "mcp-spoof",
			risk: RiskHigh,
			re: regexp.MustCompile(`(?i)\bmnemos_\w+\s*\(|call\s+(the\s+)?mnemos_`),
		},
		{
			name: "credential-bait",
			risk: RiskMedium,
			re: regexp.MustCompile(`(?i)(api[\s_-]?key|bearer\s+token|aws[\s_-]?secret|password\s*[:=])`),
		},
		{
			name: "exfil-link",
			risk: RiskMedium,
			re: regexp.MustCompile(`\[[^\]]{0,80}\]\(https?://[^)]{0,400}\?.*=`),
		},
		{
			name: "zero-width-unicode",
			risk: RiskHigh,
			re: regexp.MustCompile("[\u200B\u200C\u200D\u200E\u200F\u202A-\u202E\u2066-\u2069\u00AD\uFEFF]"),
		},
		{
			name: "tag-chars",
			risk: RiskHigh,
			re: regexp.MustCompile("[\U000E0000-\U000E007F]"),
		},
		{
			name: "base64-blob",
			risk: RiskLow,
			re: regexp.MustCompile(`\b(?:[A-Za-z0-9+/]{120,}={0,2})\b`),
		},
	}
}

// sanitise strips control characters, zero-width space, and unicode tag
// characters that have no business in human-written memory content.
func sanitise(text string) string {
	var b strings.Builder
	b.Grow(len(text))
	for _, r := range text {
		switch {
		case r == '\n' || r == '\t' || r == '\r':
			b.WriteRune(r)
		case r >= 0xE0000 && r <= 0xE007F:
			// unicode tag characters — drop
		case r == 0x200B || r == 0x200C || r == 0x200D ||
			r == 0x200E || r == 0x200F || r == 0x202A ||
			r == 0x202B || r == 0x202C || r == 0x202D ||
			r == 0x202E || r == 0x2066 || r == 0x2067 ||
			r == 0x2068 || r == 0x2069 || r == 0xFEFF ||
			r == 0x00AD:
			// zero-width / bidi overrides — drop
		case unicode.IsControl(r):
			// other control chars — drop
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
