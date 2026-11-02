// Package experiment measures the randomised comparison between the push and
// index retrieval arms from Claude Code transcripts.
//
// The store records which arm a conversation was assigned to. Everything else
// is read from the transcript afterwards, so measuring adds no hook, no table
// and nothing to the context of the session being measured.
package experiment

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"unicode"
)

// Metrics is what one conversation's transcript shows.
type Metrics struct {
	// Prompts counts turns the user typed.
	Prompts int
	// Pushback counts prompts that read as unhappy with the agent's work.
	Pushback int
	// Opens counts mnemos_get calls and Searches mnemos_search calls.
	Opens    int
	Searches int
	// NoteReads counts Read calls on auto memory notes.
	NoteReads int
	// Mentioned counts distinct saved memories whose title words appear
	// together in one assistant message.
	Mentioned int
	// InjectedChars totals what mnemos hooks added to the context.
	InjectedChars int
}

// Options configures transcript reading.
type Options struct {
	// IsSynthetic reports prompts the harness generated rather than the user.
	IsSynthetic func(string) bool
	// Titles are saved memory titles to look for in assistant messages. The
	// same list applies to both arms, which is what makes Mentioned comparable
	// between them even though it counts some coincidental matches.
	Titles []string
}

// maxLineBytes skips transcript lines too large to hold a prompt or a reply,
// such as tool results carrying a whole file. Their content is not measured.
const maxLineBytes = 4 << 20

type transcriptLine struct {
	Type             string          `json:"type"`
	IsSidechain      bool            `json:"isSidechain"`
	IsMeta           bool            `json:"isMeta"`
	IsCompactSummary bool            `json:"isCompactSummary"`
	Message          *messageBody    `json:"message"`
	Attachment       json.RawMessage `json:"attachment"`
}

type messageBody struct {
	Content json.RawMessage `json:"content"`
}

type contentBlock struct {
	Type  string `json:"type"`
	Text  string `json:"text"`
	Name  string `json:"name"`
	Input struct {
		FilePath string `json:"file_path"`
	} `json:"input"`
}

type hookAttachment struct {
	Type    string `json:"type"`
	Command string `json:"command"`
	Content any    `json:"content"`
	Stdout  string `json:"stdout"`
}

// ReadTranscript reads a Claude Code JSONL transcript. Lines it cannot parse
// are skipped, since one malformed line should not lose a whole conversation.
func ReadTranscript(r io.Reader, opts Options) (Metrics, error) {
	var m Metrics
	signatures := titleSignatures(opts.Titles)
	mentioned := map[int]bool{}
	pushback := NewPushbackDetector()

	reader := bufio.NewReaderSize(r, 1<<16)
	for {
		line, err := readLine(reader)
		if len(line) > 0 {
			var parsed transcriptLine
			if json.Unmarshal(line, &parsed) == nil && !parsed.IsSidechain {
				m.apply(parsed, opts, pushback, signatures, mentioned)
			}
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return m, fmt.Errorf("read transcript: %w", err)
		}
	}
	m.Mentioned = len(mentioned)
	return m, nil
}

// readLine returns one line, or nil for a line over maxLineBytes, whose content
// is discarded as it is read instead of being held in memory.
func readLine(r *bufio.Reader) ([]byte, error) {
	var line []byte
	oversized := false
	for {
		chunk, err := r.ReadSlice('\n')
		if !oversized {
			line = append(line, chunk...)
			if len(line) > maxLineBytes {
				oversized, line = true, nil
			}
		}
		if !errors.Is(err, bufio.ErrBufferFull) {
			return line, err
		}
	}
}

func (m *Metrics) apply(line transcriptLine, opts Options, pushback *PushbackDetector, signatures []titleSignature, mentioned map[int]bool) {
	switch line.Type {
	case "user":
		m.applyUser(line, opts, pushback)
	case "assistant":
		m.applyAssistant(line, signatures, mentioned)
	case "attachment":
		m.applyAttachment(line)
	}
}

func (m *Metrics) applyUser(line transcriptLine, opts Options, pushback *PushbackDetector) {
	if line.IsMeta || line.IsCompactSummary || line.Message == nil {
		return
	}
	text, isToolResult := promptText(line.Message.Content)
	if isToolResult || strings.TrimSpace(text) == "" || strings.HasPrefix(text, "Caveat:") {
		return
	}
	if opts.IsSynthetic != nil && opts.IsSynthetic(text) {
		return
	}
	m.Prompts++
	if pushback.Matches(text) {
		m.Pushback++
	}
}

// promptText extracts a user message's text and whether it is a tool result,
// which Claude Code also records as a user message.
func promptText(raw json.RawMessage) (string, bool) {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text, false
	}
	var blocks []contentBlock
	if json.Unmarshal(raw, &blocks) != nil {
		return "", false
	}
	found := ""
	for _, b := range blocks {
		if b.Type == "tool_result" {
			return "", true
		}
		if b.Type == "text" && found == "" {
			found = b.Text
		}
	}
	return found, false
}

func (m *Metrics) applyAssistant(line transcriptLine, signatures []titleSignature, mentioned map[int]bool) {
	if line.Message == nil {
		return
	}
	var blocks []contentBlock
	if json.Unmarshal(line.Message.Content, &blocks) != nil {
		return
	}
	for _, b := range blocks {
		switch b.Type {
		case "tool_use":
			m.applyToolUse(b)
		case "text":
			words := wordSet(b.Text)
			for i, sig := range signatures {
				if !mentioned[i] && sig.foundIn(words) {
					mentioned[i] = true
				}
			}
		}
	}
}

func (m *Metrics) applyToolUse(b contentBlock) {
	switch {
	case strings.HasSuffix(b.Name, "mnemos_get"):
		m.Opens++
	case strings.HasSuffix(b.Name, "mnemos_search"):
		m.Searches++
	case b.Name == "Read" && isNotePath(b.Input.FilePath):
		m.NoteReads++
	}
}

func isNotePath(path string) bool {
	return strings.Contains(path, "/projects/") &&
		strings.Contains(path, "/memory/") &&
		strings.HasSuffix(path, ".md")
}

func (m *Metrics) applyAttachment(line transcriptLine) {
	var a hookAttachment
	if json.Unmarshal(line.Attachment, &a) != nil || a.Type != "hook_success" {
		return
	}
	if !strings.Contains(a.Command, "mnemos") {
		return
	}
	if text, ok := a.Content.(string); ok && text != "" {
		m.InjectedChars += len(text)
		return
	}
	m.InjectedChars += len(a.Stdout)
}

// PushbackDetector spots prompts that read as unhappy with the agent's work.
//
// Its rules were tuned by reading 600 real prompts from the October 2026 audit.
// People rarely open with "no, that's wrong": most pushback is a short report
// that something still fails or got worse. On that sample the rules matched 16
// prompts, 2.7%. Read by hand, 9 of the 16 were clearly pushback, 3 were
// borderline and 4 were not, and how many real cases they miss was not measured.
// That is a noisy, rare signal, so treat it as a guardrail. Random assignment
// spreads the noise over both arms alike, which blurs a difference and does not
// create one.
type PushbackDetector struct {
	phrases *regexp.Regexp
	openers *regexp.Regexp
	pasted  *regexp.Regexp
}

// NewPushbackDetector compiles the detector's rules.
func NewPushbackDetector() *PushbackDetector {
	return &PushbackDetector{
		phrases: regexp.MustCompile(`\b(still|worse|(that's|that is|this is|it's|looks) wrong|not what i|` +
			`you (forgot|missed|didn't|broke|ignored|skipped)|` +
			`i (told you|already (said|told|asked)|asked you|meant)|` +
			`why (did|would|are) you|revert|undo|same (issue|problem|error|bug))\b`),
		openers: regexp.MustCompile(`^(no[,.]|nope\b|nah\b|wrong\b|actually,)`),
		pasted:  regexp.MustCompile(`(?s)<pasted_content[^>]*>.*?</pasted_content>`),
	}
}

// pushbackWindow limits matching to the start of a prompt. Long prompts are
// usually new briefs, and a word like "still" deep inside one means little.
const pushbackWindow = 160

// Matches reports whether a prompt reads as unhappy with the agent's work.
// Pasted text is ignored because it is someone else's words.
func (d *PushbackDetector) Matches(prompt string) bool {
	p := d.pasted.ReplaceAllString(prompt, " ")
	p = strings.ToLower(strings.TrimSpace(p))
	p = strings.ReplaceAll(p, "\u2019", "'")
	if d.openers.MatchString(p) {
		return true
	}
	if len(p) > pushbackWindow {
		p = p[:pushbackWindow]
	}
	return d.phrases.MatchString(p)
}

// titleSignature is the distinctive words of a memory title and how many must
// appear together to count as a mention.
type titleSignature struct {
	words []string
	need  int
}

const (
	// minSignatureWords excludes titles too generic to detect reliably.
	minSignatureWords = 3
	// minSignatureWordLen drops short words that match anything.
	minSignatureWordLen = 4
)

func titleSignatures(titles []string) []titleSignature {
	out := make([]titleSignature, 0, len(titles))
	for _, title := range titles {
		seen := map[string]bool{}
		var words []string
		for _, w := range splitWords(title) {
			if len(w) >= minSignatureWordLen && !seen[w] {
				seen[w] = true
				words = append(words, w)
			}
		}
		if len(words) < minSignatureWords {
			continue
		}
		// At least three words and at least 60% of them.
		need := (len(words)*3 + 4) / 5
		if need < minSignatureWords {
			need = minSignatureWords
		}
		out = append(out, titleSignature{words: words, need: need})
	}
	return out
}

func (s titleSignature) foundIn(words map[string]bool) bool {
	found := 0
	for _, w := range s.words {
		if words[w] {
			found++
		}
	}
	return found >= s.need
}

func wordSet(text string) map[string]bool {
	out := map[string]bool{}
	for _, w := range splitWords(text) {
		out[w] = true
	}
	return out
}

func splitWords(text string) []string {
	return strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}
