package prewarm

import (
	"context"
	"fmt"
	"strings"

	"github.com/polyxmedia/mnemos/internal/memory"
	"github.com/polyxmedia/mnemos/internal/safety"
)

const (
	// indexMaxTokens bounds the titles index. The store's titles alone were
	// about 1,300 tokens at 85 memories, so the budget, not the store size,
	// sets the cost per session: own project and global memories come first,
	// and other projects get what is left.
	indexMaxTokens = 1000
	// indexFetch is how many live memories the index considers.
	indexFetch = 500
	// indexTitleChars caps one title. A title that long is already a summary.
	indexTitleChars = 90
	// conventionsInBlock mirrors stepConventions' limit: those conventions are
	// printed in full by the prewarm block, so the index skips them.
	conventionsInBlock = 10

	indexHeader = "## memory index\n" +
		"Saved memories, titles only, this project first. When one bears on the task, " +
		"open it with mcp__mnemos__mnemos_get using its 8-character handle before relying " +
		"on it. Ignore the rest."
)

// MemoryIndex renders a titles-only list of live memories for a session that
// pulls instead of being pushed to. In the October 2026 replay, a model choosing
// from titles alone was relevant in 11 of 16 picks, against 5.5% for the prompt
// hook, though it also caught fewer of the memories that mattered most. Whether
// that trade pays off live is what `mnemos experiment` measures. It is the same
// pattern as Claude Code's own MEMORY.md, which agents read and use.
//
// Lines are not recorded in the injection log. A title is not the memory, and
// logging it would suppress the memory itself from pre-edit injection for the
// next two hours. Returns "" when there is nothing to list.
func (s *Service) MemoryIndex(ctx context.Context, agentID, project string) (string, error) {
	all, err := s.obs.ListByProject(ctx, agentID, "", "", indexFetch)
	if err != nil {
		return "", fmt.Errorf("memory index: %w", err)
	}
	var own, global, other []string
	conventions := 0
	for _, o := range all {
		if o.TrustTier == memory.TrustRaw {
			continue
		}
		line := fmt.Sprintf("- %s %s", memory.Handle(o.ID), indexTitle(o))
		switch {
		case project != "" && o.Project == project:
			if o.Type == memory.TypeConvention && conventions < conventionsInBlock {
				conventions++
				continue
			}
			own = append(own, line)
		case o.Project == "":
			global = append(global, line)
		default:
			other = append(other, fmt.Sprintf("%s (%s)", line, o.Project))
		}
	}
	lines := append(append(own, global...), other...)
	if len(lines) == 0 {
		return "", nil
	}

	var body strings.Builder
	used := estimateTokens(indexHeader)
	for _, line := range lines {
		cost := estimateTokens(line + "\n")
		if used+cost > indexMaxTokens {
			break
		}
		body.WriteString(line)
		body.WriteString("\n")
		used += cost
	}
	report := s.scanner.Scan(strings.TrimRight(body.String(), "\n"))
	text := report.Sanitised
	if report.MaxRisk >= safety.RiskHigh {
		text = safety.WrapFlagged(report.Sanitised, report)
	}
	return indexHeader + "\n" + text, nil
}

func indexTitle(o memory.Observation) string {
	title := oneLine(o.Title)
	if runes := []rune(title); len(runes) > indexTitleChars {
		return string(runes[:indexTitleChars-1]) + "…"
	}
	return title
}
