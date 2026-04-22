package dream

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/polyxmedia/mnemos/internal/rumination"
)

// ruminatedFromPrefix is the canonical tag prefix agents attach to a
// revision (skill version or superseding observation) naming the
// rumination candidate it resolves. The MCP ruminate_resolve handler
// records this provenance explicitly; the auto-resolve pass picks up the
// same signal when the agent took a shortcut and wrote the revision
// without going through the resolve tool.
//
// Kept here (not in rumination/) because this is dream-specific glue; the
// primitive-layer packages do not need to know about dream auto-resolve.
const ruminatedFromPrefix = "ruminated-from:"

// autoResolveRuminations scans skills (and, when the reader is wired,
// observations) for the `ruminated-from:<candidate-id>` provenance tag.
// Every pending candidate whose ID appears in such a tag is closed with
// resolved_by set to the entity that carried the tag.
//
// Non-pending candidates and missing IDs are silent skips: a repeat dream
// pass over an already-resolved candidate should be a no-op, not a warning
// flood. Errors on the skills list are propagated — if that read fails,
// the rest of the dream pass deserves to see it.
//
// Note on the scientific-method guard: the MCP ruminate_resolve handler
// enforces a Popper-style why_better field. Auto-resolve accepts looser
// rigor because the revision object already exists in the store — the
// hostile review happened when the agent wrote the revision, and re-
// running it now would be second-guessing work that landed. This is the
// AGM distinction between explicit belief revision (guarded) and inferred
// closure from derived state (relaxed).
func (s *Service) autoResolveRuminations(ctx context.Context) (int, error) {
	if s.rumination == nil || s.skills == nil {
		return 0, nil
	}
	sks, err := s.skills.List(ctx, "")
	if err != nil {
		return 0, fmt.Errorf("list skills: %w", err)
	}

	closed := 0
	for _, sk := range sks {
		for _, tag := range sk.Tags {
			candID := candidateIDFromTag(tag)
			if candID == "" {
				continue
			}
			// Check state before resolving so repeat dream passes over the
			// same (already-resolved) revision do not re-count. The Store's
			// Resolve is idempotent for same-revision reconfirmations, but
			// the dream journal should track *transitions*, not reaffirms.
			cand, err := s.rumination.Get(ctx, candID)
			if err != nil {
				if errors.Is(err, rumination.ErrNotFound) {
					continue
				}
				s.log.Debug("auto-resolve lookup", "candidate", candID, "err", err)
				continue
			}
			if cand.Status != rumination.StatusPending {
				continue
			}
			if err := s.rumination.Resolve(ctx, candID, sk.ID); err != nil {
				s.log.Debug("auto-resolve skip", "candidate", candID, "skill", sk.ID, "err", err)
				continue
			}
			closed++
		}
	}
	return closed, nil
}

// candidateIDFromTag returns the candidate ID portion of a
// `ruminated-from:<id>` tag, or "" when the tag has a different prefix.
// Exists as its own function so the regex/semantics are visible when a
// second monitor type (observations, conventions) adds a parallel scan.
func candidateIDFromTag(tag string) string {
	if !strings.HasPrefix(tag, ruminatedFromPrefix) {
		return ""
	}
	return strings.TrimPrefix(tag, ruminatedFromPrefix)
}
