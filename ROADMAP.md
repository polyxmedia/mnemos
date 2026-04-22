# Roadmap

Open work on Mnemos. Pick anything here and ship it. If you want to claim an item, open an issue or drop a PR.

## Shipped

- [x] v0.1.0 — storage, memory service, MCP server (14 tools), installer, HTTP API
- [x] v0.2.0 — skill packs, session replay, bi-temporal timestamp precision fix
- [x] v0.3.0 — Claude Code SessionStart hook auto-wired by `mnemos init`, self-updating binary (`mnemos update`), corrections-to-skills auto-promotion in the dream pass, Claude Code skill for agent tool-call reliability, and the full rumination feature: four threshold monitors (skill effectiveness, stale skill, correction-repeat-under-skill, contradiction-detected), dedicated SQLite queue with pending/resolved/dismissed lifecycle, four MCP tools (`mnemos_ruminate_list` / `_pack` / `_resolve` / `_dismiss`) with a Popper-style `why_better` guard on resolve, prewarm badge on matched skills, and dream-pass auto-resolve via `ruminated-from:<id>` provenance tags

## Next up — high-leverage

- [ ] **`mnemos digest`** — nightly markdown summary ("yesterday you saved 12 observations, promoted 2 skills, dreamed at 2am"). Ship as `mnemos digest --since 24h`. Small feature, big "numbers on the screen" moment for devs who like to see their memory store earn its keep.
- [ ] **VS Code extension** — sidebar that reads `mnemos_touch` heat map, surfaces corrections as hover hints on files with saved corrections. Status-bar widget: "Session active · 12 obs · 3 corrections matched". Makes Mnemos *visible* in the editor, which is what gets shared.
- [ ] **Homebrew tap** — create `polyxmedia/homebrew-tap`, add `HOMEBREW_TAP_GITHUB_TOKEN` to Actions secrets, uncomment the brews block in `.goreleaser.yml`. One-line install becomes `brew install polyxmedia/tap/mnemos`.

## Invocation reliability — make sure mnemos is called at the right time

Claude Code exposes ~26 hook events; we ship 3. A memory system nobody invokes is zero value regardless of schema quality, so this layer gates everything downstream.

**Shipped**

- [x] `SessionStart` → `mnemos prewarm` (passive context injection)
- [x] `UserPromptSubmit` → `mnemos hook user-prompt` (backfills session goal from the first prompt — kills the "agent skipped mnemos_session_start on editing tasks" failure mode)
- [x] `PostToolUse` (`Edit|Write|MultiEdit|NotebookEdit`) → `mnemos hook post-tool` (passive file-touch capture; the heat map no longer depends on the agent remembering to call `mnemos_touch`)

**Next (all straight drop-ins, no design work)**

- [ ] **`SessionEnd` hook** — carries a `reason` field (`logout` | `clear` | `resume` | `prompt_input_exit` | `bypass_permissions_disabled` | `other`). Close any open mnemos session for the cwd with a derived summary. Native event for session close — not `Stop` (per-turn) and not `SessionStart`-cleanup-on-next-launch (lazy).
- [ ] **`PreCompact` / `PostCompact` hooks** — we already have `prewarm --mode=compaction_recovery`, but it only fires reactively on the next `SessionStart`. Hook `PreCompact` to dump a proactive snapshot before context is lost, and `PostCompact` to re-prewarm immediately. Closes the one-turn gap where the agent is working on freshly-compacted context without mnemos restored.

**Bigger — the blocking-hook pattern we haven't used at all**

Claude Code hooks can exit 2 with stderr that **feeds back into Claude's context**. All our hooks today are passive (exit 0, record something). The blocking pattern turns memory from "hint the agent may ignore" into an active guardrail:

- [ ] **Blocking-hook demo** — ship one concrete blocker to compose the pattern. Candidate: `PreToolUse` matched on `Bash`, blocks `git commit` commands whose message carries AI attribution phrasing when the project has a stored correction against it. Narrow, demonstrable, opens the door.
- [ ] **`PreToolUse` on `mcp__mnemos__mnemos_save` / `_correct`** — scan incoming content for injection before it lands in the store. This is Bet 2's quarantine tier shipping at the hook layer, much earlier than the full provenance work.
- [ ] **`PreToolUse` on `Edit` / `Write`** — if an active correction contradicts the pattern being introduced, surface it via stderr (fed to Claude) so the agent self-corrects before the file is written. Highest-leverage use of the memory store.
- [ ] **`TaskCreated` / `TaskCompleted`** — gate on rumination queue for the topic; surface high-severity candidates before the agent commits to an approach.
- [ ] **`SubagentStart` / `SubagentStop`** — cascade subagent learnings back to the parent session so corrections made inside a subagent don't get lost.

**Distribution — same mechanism, different hosts**

- [ ] **Multi-agent installers** — `mnemos init` only knows Claude Code today. Each other host has the same "harness hook" concept under a different name: Cursor has `.cursor/rules` + hooks in preview, Windsurf has Cascade rules, Codex CLI has AGENTS.md, Gemini CLI has its own. Mechanism is the same (hook, not agent judgement); config files differ. Ride on Bet 3's spec once that lands; until then, ship host-specific installers for the ones people actually use.
- [ ] **Skill pack registry** — moved down from "Next up" (lower priority than invocation reliability; Anthropic's Agent Skills ecosystem already claimed this category).

## The three bets — April 2026 frontier plays

Two deep-research passes (competitive landscape + academic frontier + user pain) converged on the same three moves. Ranked by leverage. See `docs/research/2026-04-frontier.md` for the full evidence trail.

- [ ] **Bet 1 — Symbol-anchored memory (*the launch headline*)** — memories keyed to AST/LSP symbol identity (SCIP string when an indexer exists; tree-sitter AST hash of the symbol body as content-address; git `-L`-style follow as fallback). On rename/move the memory re-anchors; on deletion it orphans with full provenance. Sibling-symbol retrieval ("this memory applies to `TransferService.Reconcile` — also surface for matching interfaces"). **Claim nobody else can make:** *mnemos memories survive refactors.* Every shipping memory product (mem0, Zep, Letta, MemPalace, agentmemory, CLAUDE.md, Cursor rules, Windsurf memories) is prose-keyed or path-keyed and rots on refactor. Serena MCP is the closest living relative and even it stops at "LSP for reading code" — its memories are free text. Solves the top-ranked user pain: *memory rot* (mem0 issue #4573 documented a 97.8% junk rate where a single hallucinated "User prefers Vim" fact re-ingested into 808 entries). Demo is one `gopls rename` away.

- [ ] **Bet 2 — Provenance graph + quarantined tool-output tier (*the depth story*)** — every memory carries `source_kind` (user | tool | agent_inference | dream), a `derived_from[]` DAG of parent memories, and a `trust_tier` (raw | curated | skill). Tool-output and browsed-content memories start in `raw` and can only be promoted via user confirmation, rumination, or the dream pass before they're surfaced as facts. Every `mnemos_search` response includes its provenance chain. Addresses three pains at once: **memory poisoning** (MemoryGraft / MINJA / "Poison Once, Exploit Forever" — persistent compromise via poisoned memory is live as of Q1 2026 and almost no shipping product sandboxes tool-derived writes), **the "why" gap** (systems save *what* but never *why* — explicitly called out as unexplored in the 2025 memory surveys), and **hallucination feedback loops** (the mem0 97.8% junk came from no grounding check between extraction and storage). Composes naturally with our existing `ContradictionDetectedMonitor` — it can now surface which chain to trust.

- [ ] **Bet 3 — `mcp-memory-spec` as an open protocol (*the category play*)** — draft a minimal memory schema as an MCP SEP: `memory/save`, `memory/search`, `memory/link`, `memory/invalidate`, `memory/provenance`, `memory/promote`. Bi-temporal (`t_valid`, `t_invalid`, `t_observed`) and provenance fields are first-class. Publish mnemos as the reference implementation and submit to modelcontextprotocol/modelcontextprotocol. The 2026 MCP roadmap calls out Tasks, Server Cards, streamable HTTP — **nothing memory-specific**. MemPalace, agentmemory, Memorix, OpenMemory MCP all hand-roll incompatible schemas. First mover defines the category. Reframes mnemos from "another memory tool" to **the standard everyone else implements**.

## Interesting but bigger

- [ ] **Memory-aware prompt compression** — use importance/recency/access scores to produce a lossless-where-it-matters compressed snapshot of memory in N tokens. Expose as `mnemos_pack_context`.
- [ ] **Session pre-warm for the MCP client side** — right now we return the prewarm block but clients might not render it prominently. Could ship an example Claude Code skill / slash command that reads the prewarm and restates it.
- [ ] **Promptware injection leaderboard** — bench our safety scanner against published injection corpuses. Ship the benchmark as `mnemos safety bench`. Now directly paired with Bet 2: tool-output quarantine is the defense, this is the measurement.
- [ ] **Team federation** — opt-in sync of observations tagged `shared:true` to a team HTTP endpoint. Downstream of Bet 3 (ship the spec, then the federation layer rides on it).

## Deprioritized after frontier research

- ~~**Cross-agent federation as a headline feature**~~ — MemPalace (19.5k stars in two weeks) and agentmemory (Cognition) already own the undifferentiated "works across Claude/Cursor/Codex" slot. Don't race there — out-schema them via Bet 3 instead.
- ~~**Skill pack registry**~~ (moved from "Next up") — Anthropic's Agent Skills + `skill-creator` ecosystem already claimed this category. Our procedural-memory edge is corrections→skills auto-promotion, not a marketplace. Keep the promotion pipeline, skip the registry.
- **Chasing LongMemEval numbers as primary KPI** — Hindsight hits 91.4% with a 20B model. Our edge isn't conversational-recall benchmark-chasing; it's code-aware, provenance-rich, local-first memory those benchmarks don't measure. Better play: define a refactor-survival benchmark and own that one.

## Quality / polish

- [ ] Push `cmd/mnemos` coverage past 70% via subprocess-based integration tests for `runServe`.
- [ ] Run LongMemEval locally and publish our numbers in the README (claim vs. measure).
- [ ] Add `mnemos bench` that prints search latency percentiles so users can see the sub-millisecond BM25 claim.
- [ ] Generate + host `pkg/client` godoc at pkg.go.dev — confirm badge resolves once a tagged release is indexed.

## Decisions I haven't made yet

These need André's call before anyone builds them:

- **Registry policy**: public (anyone can list) vs curated (PRs reviewed)?
- **Team federation**: what auth model for the team endpoint? Bearer tokens, GitHub OAuth, something else?
- **Skill versioning**: stay with integer bumps, or move to semver when packs get shared?
- **Telemetry**: do we want any opt-in anonymous telemetry (e.g. which tools are called most) to inform roadmap? Bias: no, but worth thinking about.

## How I want this roadmap used

It's a living document. When something here ships, strike it. When a new idea comes up that's worth doing, add it. When an idea gets rejected, annotate why. If you picked up Mnemos and have a thing you want — open an issue, we'll talk.
