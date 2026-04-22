---
name: mnemos
description: Use this skill whenever mnemos_* MCP tools are available and you are doing work worth remembering. Triggers on session start (record a goal), session end (record a summary), user says "save", "remember", "record this", "we were wrong about", and on any genuine correction or architectural decision. Keeps mnemos's memory store alive across sessions. Without this skill, agents silently edit and the store goes empty.
---

# mnemos

You have a persistent memory layer available via the `mnemos_*` MCP tools. By default you will skip these tools on plain editing tasks. Don't. Mnemos is the learning-loop primitive for the project you are working on: corrections, decisions, and conventions compound across sessions if you record them. Silently editing leaves the store empty and the next session blind.

There is a known correction already stored about this exact failure mode: _"agent skipped mnemos_session_start on editing tasks — LLMs skip optional tool calls when the task looks like plain reading/editing; the agent needs an external nudge."_ This skill is that nudge.

## Session lifecycle

**Start.** The Claude Code SessionStart hook fires a prewarm at launch, but it does not open a session with a Goal. If you are about to do real work (not a one-shot question), open one:

```
mnemos_session_start(
  project="<repo name from git or cwd>",
  goal="<one short line — what you are trying to do this session>"
)
```

Without a goal, the session is a bare timestamp. With a goal, it becomes a durable record that `mnemos replay` and future prewarms can use.

**End.** When the user signals done ("ship it", "that's it", "commit and close", "we're done"), close the session:

```
mnemos_session_end(
  session_id="<id>",
  summary="<one or two sentence recap of what shipped>",
  status="completed"
)
```

Status values: `completed` | `abandoned` | `handoff`. An open session with no summary is dead weight in the next session's prewarm.

## Which tool for which signal

| Signal from the user or the work | Tool to call |
| --- | --- |
| A non-obvious decision, pattern, or architectural call worth preserving | `mnemos_save` with `type=decision` / `architecture` / `pattern` |
| User corrects an approach: "actually no, do X because Y" | `mnemos_correct(tried, wrong_because, fix)` |
| A rule that should apply to the project forever: "always wrap errors with %w" | `mnemos_convention(title, rule, rationale, project)` |
| A file you are editing heavily and will revisit | `mnemos_touch(path, project)` |
| Reloading state mid-session after a context compaction | `mnemos_context(session_id=..., mode="recovery")` |
| Checking history before doing something you might already have figured out | `mnemos_search(query, project)` |
| Quick state check ("is mnemos actually recording?") | `mnemos_stats` |
| Linking two observations so they surface together | `mnemos_link(from_id, to_id, relation)` |
| At the top of a long session, or when the user questions a stored rule | `mnemos_ruminate_list` → pick one → `mnemos_ruminate_pack` |
| Replacing a weak skill or stale rule after hostile review | `mnemos_ruminate_resolve(id, resolved_by, why_better)` |
| Leaving a rule intact because the evidence was noise | `mnemos_ruminate_dismiss(id, reason)` |

## Correction shape

Corrections are the atomic unit of the mnemos learning loop: three corrections with the same topic get auto-promoted into a skill in the dream pass. Fill the fields honestly:

```json
{
  "title": "oauth retry without backoff",
  "tried": "retry on 401",
  "wrong_because": "401 is auth failure, not transient",
  "fix": "refresh token, then retry once",
  "trigger_context": "implementing token refresh for the apollo integration",
  "project": "<repo>"
}
```

`trigger_context` is optional but valuable: it populates the `## When this applies` section of the auto-promoted skill if three corrections cluster on the same label.

## Save shape

```json
{
  "title": "use modernc.org/sqlite, not mattn/go-sqlite3",
  "content": "pure-Go driver keeps the binary CGO-free and cross-compilable to linux/darwin/windows without a toolchain on the install path",
  "type": "decision",
  "rationale": "zero-CGO is a hard invariant",
  "project": "mnemos",
  "tags": ["sqlite", "build", "dependencies"],
  "importance": 8
}
```

Valid `type` values: `decision` | `bugfix` | `pattern` | `preference` | `context` | `architecture` | `episodic` | `semantic` | `procedural` | `correction` | `convention`.

## What not to do

- Don't call `mnemos_save` for ephemeral within-session context ("I'm reading X now"). Save things worth remembering next session, not running commentary on this one.
- Don't call `mnemos_correct` for trivial typos or one-off slips. Corrections are for conceptual mistakes that would repeat.
- Don't fabricate tool arguments. Derive `project` from the git remote or the working directory; if unclear, ask.
- Don't spam `mnemos_search` before every edit. It is fast but the goal is a richer memory, not a pre-check ritual.
- Don't skip `mnemos_session_end`. Check for open sessions via `mnemos_stats` at the top of a session; if one is open from a past run, close it.

## Rumination: when a stored rule looks wrong

Mnemos flags stored knowledge whose effectiveness has fallen below the threshold. These are **rumination candidates** and they want a hostile review, not a rubber stamp.

Trigger situations where you should check the queue:

- At the top of any long or consequential session (run `mnemos_ruminate_list`).
- When the user questions a rule the memory layer is surfacing ("is this still true?").
- When you just watched a saved skill or convention fail in practice.

Workflow:

1. `mnemos_ruminate_list` → returns `{candidates: [...], counts: {...}}`. Pick one by ID.
2. `mnemos_ruminate_pack(id)` → returns a review block with **Hypothesis**, **Disconfirming evidence**, **Falsifiable restatement**, **Hostile review** prompts, and an **Action** section. Read the block. Answer the hostile prompts honestly. The prompts are ordered: steel-man the opposite → find the fatal flaw → decide falsification vs noise → check whether context shifted → state the new prediction.
3. Decide one of two outcomes:
    - **Resolve** with a concrete revision: `mnemos_ruminate_resolve(id, resolved_by, why_better)`. `resolved_by` is the ID of the new skill version or superseding observation. `why_better` **must be a sentence naming a new prediction the revision makes that the old version did not** — Popper's falsifiability guard. Cosmetic rewording is rejected by the server (min 16 chars, but length is not the real test — the test is whether you can state a new prediction).
    - **Dismiss** with an honest reason: `mnemos_ruminate_dismiss(id, reason)`. Use when the hostile review convinced you the rule stands and the evidence was a one-off. The reason is preserved so a future rumination pass doesn't re-raise the same flag without context.

Never close a candidate silently or with filler. A rumination that resolves to "no change" is either a dismissal with a real reason or a bug in the monitor — both belong in the provenance trail, not in the void.

## Quick self-check

Before wrapping any multi-step session, run through this:

1. Did I open a session with a real goal? If not, call `mnemos_session_start` now and cite what the goal was retroactively.
2. Is there a correction from this session? If the user said "no, do X instead", record it before they have to ask.
3. Is there a decision worth preserving? If the answer took thinking to arrive at, save it.
4. Did I check `mnemos_ruminate_list` at least once this session? If the queue had pending candidates on a topic I touched, I should have resolved or dismissed them.
5. Did I close the session with a summary that future-me can read in a prewarm?
