# Agent Memory Frontier — April 2026

Research notes from two deep-research passes (competitive landscape + academic frontier + user pain) that informed the three-bet roadmap in `ROADMAP.md`. Kept here so future decisions can trace back to the evidence.

Dated April 2026. Re-run this research before committing to any of the bets if more than six months have passed — the field is moving fast.

## TL;DR

Two independent research passes converged on the same three moves:

1. **Symbol-anchored memory** — first mover, genuine moat, solves a real but unnamed pain.
2. **Provenance graph + quarantined tool-output tier** — defends against memory poisoning (live threat as of Q1 2026) and answers the "why" gap everyone complains about.
3. **`mcp-memory-spec` as an open protocol** — the 2026 MCP roadmap has no memory spec; first mover defines the category.

## 1. Where existing products are

| Product | Positioning | What it does well | What it can't do |
|---|---|---|---|
| **Letta (ex-MemGPT)** | Programmatic agent-memory service; V1 loop rewrite Mar 2026, Letta Code app Apr 6 | Tool-call reliability, three-tier memory, REST-first | V1 loses user control of reasoning traces; lost some heartbeat automation |
| **mem0** | Vector + hybrid search, 21+ integrations | Easy DX, fast (~50% latency cut in v2.0.0 Apr 16) after pivoting *away* from graph | No provenance, no grounding check — [issue #4573](https://github.com/mem0ai/mem0/issues/4573) documented 97.8% junk rate, 808 hallucinated "User prefers Vim" entries |
| **Zep / Graphiti** | Bi-temporal KG, 94.8% DMR | Retroactive corrections, clean temporal provenance | Neo4j-heavy, not local-first |
| **Cognee** | ECL pipeline, 38+ connectors, $7.5M seed | Enterprise RAG-memory | Not suited to personal/local dev memory |
| **LangMem** | Library, three memory types (episodic/semantic/procedural) | Strong primitives | Framework, not product — no opinion |
| **Cursor Memories** | `.cursor/rules` + manual memories | In-editor surface | Silent-ignore complaints, rot on refactor |
| **Windsurf Cascade** | Auto-generated workspace-scoped memories | Zero-config | Opaque, path-keyed |
| **Claude Code** | `CLAUDE.md` + `MEMORY.md` auto-loaded + Skills + Hooks | Editable markdown, first-class procedural memory | 200-line / 25KB truncation — "a critical config value fell off the bottom and cost an hour" ([HN](https://news.ycombinator.com/item?id=46426624)) |
| **MemPalace** | Spatial Method-of-Loci memory, 19 MCP tools, 96.6% LongMemEval R@5, local-first, MIT | Shot to ~19.5k stars in two weeks (Apr 2026) | Generic cross-agent; no code-awareness, no symbol anchoring |
| **agentmemory (Cognition)** | Global npm, single MCP across Claude Code / Cursor / Gemini CLI / OpenCode | Simple, Cognition-backed distribution | Same schema-less, prose-keyed limits |
| **Serena MCP** | LSP-aware coding agent, `.serena/memories/*.md` | Closest living relative to symbol-anchoring | Memories are free text, **not** keyed to the symbols it reads; no re-anchoring on rename |

## 2. Academic signals (2025 → early 2026)

The 12 papers that matter right now — what we're tracking, what's shipped-worthy, what validates existing mnemos choices:

| # | Paper | One-line takeaway | Mnemos relevance |
|---|---|---|---|
| 1 | [Hindsight is 20/20](https://arxiv.org/abs/2512.12818) (Vectorize, Dec 2025) | Retain/recall/reflect × four typed networks; **91.4% on LongMemEval** with a 20B model | New SOTA reference; informs Bet 2 (causal provenance) |
| 2 | [A-MEM](https://arxiv.org/abs/2502.12110) (NeurIPS 2025) | Zettelkasten-inspired dynamic linking between memory notes | Heavily cited; informs how Bet 2's provenance DAG could auto-link |
| 3 | [Episodic Memory is the Missing Piece](https://arxiv.org/pdf/2502.06975) | Consolidation from episodic → parametric is the unsolved axis | Validates our dream-pass premise |
| 4 | [From Human Memory to AI Memory (survey)](https://arxiv.org/html/2504.15965v2) | Cognitive-science → LLM-memory mapping | Vocabulary for the category |
| 5 | [Memp](https://arxiv.org/html/2508.06433v2) | Procedural memory as compiled executable subroutines | Directly validates our corrections → skills pipeline |
| 6 | [Hierarchical Procedural Memory](https://arxiv.org/html/2512.18950v1) | Playbooks composed of atomic skills | Future direction for skill packs |
| 7 | [Reflective Memory Management (ACL 2025)](https://aclanthology.org/2025.acl-long.413.pdf) | Prospective + retrospective reflection, RL-optimized retrieval | Where rumination goes next |
| 8 | [Evo-Memory](https://arxiv.org/html/2511.20857v1) | Self-evolving memory at test time | Informs active decay / reinforcement |
| 9 | [Language Models Need Sleep (OpenReview 2025)](https://openreview.net/forum?id=iiZy6xyVVE) | Consolidation + dreaming with RL-driven synthetic rehearsal | Directly validates dream pass |
| 10 | [Learning to Forget / SleepGate](https://arxiv.org/abs/2603.14517) | Sleep micro-cycles evict stale KV-cache | Informs active decay |
| 11 | [MemoryGraft](https://arxiv.org/abs/2512.16962) + [MINJA](https://arxiv.org/html/2503.03704v2) + [Poison Once, Exploit Forever](https://arxiv.org/html/2604.02623) | Persistent memory compromise via poisoning is now shipping-real | **Drives Bet 2's quarantine tier** |
| 12 | [Multi-Agent Memory from a Computer Architecture Perspective](https://arxiv.org/html/2603.10062v1) | Frames multi-agent memory as cache-coherence / synchronization | Vocabulary for eventual team federation |

Reading the signals: sleep/dream consolidation going mainstream; procedural memory is the hot axis after episodic; provenance/causal-parent metadata in retrieval called out as "largely unexplored"; poisoning research just got real.

## 3. User pain, ranked by how often it comes up

1. **Memory rot / stale facts re-surfaced as truth.** Canonical evidence: [mem0 issue #4573](https://github.com/mem0ai/mem0/issues/4573) — a 2B model hallucinated "User prefers Vim", that hallucination landed in recall context the next session, the extraction model treated it as ground truth and re-extracted it, and over 32 days 97.8% of 10,134 entries became junk, including 808 Vim entries (191 identical). Root cause: *"there's nothing between extraction and storage that checks whether a fact is grounded in the actual conversation."*
2. **Silent memory wipes / drift.** Two full ChatGPT memory-wipe incidents in 2025 (Feb 5, Nov 6–7). 300+ active complaint threads on r/ChatGPTPro.
3. **Context rot in long sessions.** Instruction burial + recency dominance — system prompts drowned under 80K tokens of noise.
4. **Agents fragment across tools.** Cognition's agentmemory launch post: *"If your team uses Codex CLI for autonomous tasks, Claude Code for architecture reviews, and Cursor for daily editing, your collective project knowledge fragments across three incompatible silos."* This is why MemPalace + agentmemory exploded.
5. **CLAUDE.md / MEMORY.md truncation bites.** The first 200 lines / 25KB load; older notes silently drop. [HN anecdote](https://news.ycombinator.com/item?id=46426624): a critical config value fell off the bottom and cost an hour.
6. **No record of *why*.** Systems save *what* but not *why*. Agents can't explain their own behaviour or re-derive decisions when context changes.
7. **Injection / poisoning.** Palo Alto [Unit 42](https://unit42.paloaltonetworks.com/indirect-prompt-injection-poisons-ai-longterm-memory/) + MemoryGraft + MINJA document real-world persistent compromise. Almost no product sandboxes tool-output-derived memories separately from user-stated facts.
8. **Memory doesn't generalize.** Saved for file A, doesn't fire for sibling file B. No mainstream system keys memories to symbols or code identity.
9. **Cost / latency.** "Every memory operation burns inference tokens… overhead compounds so simple tasks get expensive."

## 4. The frontier directions we considered

Ranked by leverage × novelty × 2026 feasibility:

| # | Direction | Novelty | Feasibility | Pain # | mnemos status |
|---|---|---|---|---|---|
| 1 | Symbol-anchored memory | **Nobody does this** | Medium — SCIP + tree-sitter + gopls | #1, #8 | Lead if shipped |
| 2 | Provenance graph + quarantined tool-output tier | Papers exist, nobody ships | Medium — schema + policy | #1, #6, #7 | Ahead if shipped |
| 3 | `mcp-memory-spec` as an open protocol | No one is driving it | Medium — spec + reference impl | #4 | Category-defining if shipped first |
| 4 | Counterfactual replay / memory ablation | Voyager did offline; nothing ships per-memory value scoring | Medium-Hard | #1, #6, #9 | Ahead potential |
| 5 | Dream-pass → parametric (LoRA consolidation) | "Language Models Need Sleep" paradigm | Hard for hosted; feasible for local llama.cpp / MLX | #3, #5 | Ahead on markdown path |
| 6 | Active decay with retrieval-reinforcement (Ebbinghaus) | Multiple 2025 papers; no product ships properly | Easy | #1 | Partial — we have skill-effectiveness, not full curve |
| 7 | Memory-attributed outputs (inline citations) | Nobody does this well | Easy | #2, #6 | Partial — stats exist, not per-response |
| 8 | Team-federated memory with CRDT | Called out in MAS survey; not shipped | Hard | #4 | Downstream of #3 |
| 9 | Repo-as-memory-substrate (git blame = provenance, PRs = review) | Claude Code does half; nobody does the sidecar + symbol-anchor combo | Medium | #1, #4, #6 | Unique to mnemos's posture |

What we deliberately *didn't* pick and why:

- **Generic cross-agent federation as the headline.** MemPalace + agentmemory already won the undifferentiated slot. Out-schema them (Bet 3) instead of out-polishing them.
- **Another hierarchical/paging architecture.** Letta owns it.
- **Graph-memory as a database play.** Mem0 killed Neo4j for a reason; Cognee owns enterprise-graph.
- **LongMemEval leaderboard chasing.** Wrong benchmark for a code-aware local-first product.

## 5. The three bets — why these, in this order

### Bet 1 — Symbol-anchored memory (launch headline)

Architecture: three-layer anchor. (a) primary: **SCIP** symbol string when an indexer exists (Go/TS/JS/Python/Java/Rust/Scala/Ruby/C/C++ all have indexers). (b) secondary: **tree-sitter AST hash** of the symbol body — content-addressed, survives renames, detects meaningful edits. (c) tertiary: **git `log -L` / `blame --follow`** for orphaned cases. A dream-pass monitor diffs (current SCIP index ∪ AST hashes) against stored anchors and re-keys or orphans memories.

Why first: this is the only bet where mnemos's Go + SQLite + local-first + MCP posture is a structural advantage. The user's code lives on the user's machine; cloud memory vendors can't parse it cleanly without shipping code over the wire. The demo ("I renamed `TransferService.Reconcile`, my memory followed it") is a single `gopls rename` away. Names a pain ("memory rots when I refactor") that is *latent* in the wild — it hasn't surfaced as a top-of-mind complaint because existing memory is too vague to show the rot visibly.

### Bet 2 — Provenance graph + quarantined tool-output tier (depth story)

Schema additions: `source_kind` (user | tool | agent_inference | dream), `derived_from[]` (memory_id DAG), `trust_tier` (raw | curated | skill). Tool-output and browsed-content memories start `raw` and promote to `curated` only via user confirmation, rumination, or dream pass. Every `mnemos_search` response includes its provenance chain.

Why second: closes the attack surface (MemoryGraft / MINJA are live threats) and the "why" gap (explicitly unexplored per the 2025 surveys) simultaneously. Composes with what we already have — `ContradictionDetectedMonitor` becomes much sharper with provenance, and counterfactual replay (candidate #4) comes almost for free on top of the DAG.

### Bet 3 — `mcp-memory-spec` (category play)

Minimal schema as an MCP SEP: `memory/save`, `memory/search`, `memory/link`, `memory/invalidate`, `memory/provenance`, `memory/promote`. Bi-temporal and provenance fields first-class. mnemos is the reference implementation. Submit to modelcontextprotocol/modelcontextprotocol. Blog post frames the cross-agent memory race as needing a common schema.

Why third (not first): the spec is only credible *after* Bets 1 and 2 ship, because its differentiator over every other memory-MCP server is the bi-temporal + provenance + symbol-anchor semantics baked in. If we spec it before we ship it we're just another whitepaper; if we spec it after, we're defining the category with the best reference impl already in hand.

## Sources

All links from the two research passes, de-duplicated. Organized by section.

### Products & landscape
- [Letta V1](https://www.letta.com/blog/letta-v1-agent) · [Letta Code](https://www.letta.com/blog/letta-code) · [Letta next phase](https://www.letta.com/blog/our-next-phase)
- [Mem0 v2.0.0 release](https://newreleases.io/project/github/mem0ai/mem0/release/v2.0.0) · [Mem0 graph memory docs](https://docs.mem0.ai/platform/features/graph-memory) · [Mem0 issue #4573 — 97.8% junk](https://github.com/mem0ai/mem0/issues/4573) · [Mem0 vs Zep — Atlan](https://atlan.com/know/zep-vs-mem0/) · [State of AI Agent Memory 2026](https://mem0.ai/blog/state-of-ai-agent-memory-2026)
- [Zep arXiv](https://arxiv.org/abs/2501.13956) · [Graphiti repo](https://github.com/getzep/graphiti) · [Graphiti — Neo4j](https://neo4j.com/blog/developer/graphiti-knowledge-graph-memory/)
- [Cognee](https://www.cognee.ai/) · [Cognee seed raise](https://www.cognee.ai/blog/cognee-news/cognee-raises-seven-million-five-hundred-thousand-dollars-seed)
- [LangMem](https://langchain-ai.github.io/langmem/)
- [Cursor Memories](https://docs.cursor.com/context/memories) · [Cursor forum: persistent memory](https://forum.cursor.com/t/persistent-ai-memory-for-cursor/145660) · [Why Cursor rules are silently ignored](https://dev.to/vibestackdev/why-your-cursor-rules-are-being-silently-ignored-and-how-to-fix-it-4123)
- [Windsurf Cascade](https://docs.windsurf.com/windsurf/cascade/memories)
- [Claude Code memory — Mem0 analysis](https://mem0.ai/blog/how-memory-works-in-claude-code) · [Claude Code context discipline 2026](https://techtaek.com/claude-code-context-discipline-memory-mcp-subagents-2026/) · [Claude Auto Dream](https://claudefa.st/blog/guide/mechanics/auto-dream) · [Anthropic Agent Skills](https://www.anthropic.com/engineering/equipping-agents-for-the-real-world-with-agent-skills)
- [Claude Code issue #45569 — memory ignored](https://github.com/anthropics/claude-code/issues/45569)
- [MemPalace](https://www.mempalace.tech/) · [agentmemory by Cognition](https://www.cognitionus.com/blog/agentmemory-guide) · [Memorix](https://github.com/AVIDS2/memorix) · [OpenMemory MCP](https://mem0.ai/blog/introducing-openmemory-mcp) · [Cross-tool memory portability — MemPalace origin](https://codex.danielvaughan.com/2026/04/17/cross-tool-agent-memory-mempalace-portability/)
- [Serena MCP](https://github.com/oraios/serena) · [Serena tools](https://oraios.github.io/serena/01-about/035_tools.html)
- [Aider repo map](https://aider.chat/2023/10/22/repomap.html) · [SCIP announcement](https://sourcegraph.com/blog/announcing-scip) · [SCIP proto](https://github.com/sourcegraph/scip/blob/main/scip.proto) · [Stack graphs intro](https://github.blog/open-source/introducing-stack-graphs/) · [Stack graphs talk](https://dcreager.net/talks/stack-graphs/) · [git log -L](https://calebhearth.com/git-method-history) · [Semgrep match_based_id](https://semgrep.dev/docs/semgrep-code/remove-duplicates) · [Diffsitter HN](https://news.ycombinator.com/item?id=44520438)
- [AFT tree-sitter agent toolkit](https://github.com/cortexkit/aft)
- [AI Memory Wars — consumer comparison](https://lumichats.com/blog/chatgpt-memory-vs-claude-memory-vs-gemini-personal-intelligence-2026-which-ai-actually-knows-you) · [OpenAI memory crisis](https://www.allaboutai.com/ai-news/why-openai-wont-talk-about-chatgpt-silent-memory-crisis/)
- [Agent Memory Race of 2026](https://ossinsight.io/blog/agent-memory-race-2026) · [Agent Memory Systems 2026](https://blog.bymar.co/posts/agent-memory-systems-2026/) · [Memory is the Unsolved Problem of AI Agents](https://dev.to/jihyunsama/memory-is-the-unsolved-problem-of-ai-agents-heres-why-everyones-getting-it-wrong-4066) · [The Problem with AI Agent Memory — Giannone](https://medium.com/@DanGiannone/the-problem-with-ai-agent-memory-9d47924e7975)
- [MCP 2026 roadmap](https://blog.modelcontextprotocol.io/posts/2026-mcp-roadmap/)
- [Kiro — refactoring made right](https://kiro.dev/blog/refactoring-made-right/)
- [GAM — context rot (VentureBeat)](https://venturebeat.com/ai/gam-takes-aim-at-context-rot-a-dual-agent-memory-architecture-that) · [Memory for AI Agents — New Stack](https://thenewstack.io/memory-for-ai-agents-a-new-paradigm-of-context-engineering/)
- [HN: Fixing Claude Code's amnesia](https://news.ycombinator.com/item?id=47593178) · [HN: Stop Claude Code from forgetting](https://news.ycombinator.com/item?id=46426624)

### Academic
- [Hindsight 20/20](https://arxiv.org/abs/2512.12818) · [A-MEM](https://arxiv.org/abs/2502.12110) · [Episodic Memory is Missing](https://arxiv.org/pdf/2502.06975) · [From Human Memory to AI Memory survey](https://arxiv.org/html/2504.15965v2) · [Memp](https://arxiv.org/html/2508.06433v2) · [Hierarchical Procedural Memory](https://arxiv.org/html/2512.18950v1)
- [Reflective Memory Management — ACL 2025](https://aclanthology.org/2025.acl-long.413.pdf) · [Evo-Memory](https://arxiv.org/html/2511.20857v1) · [Language Models Need Sleep](https://openreview.net/forum?id=iiZy6xyVVE) · [SleepGate / Learning to Forget](https://arxiv.org/abs/2603.14517) · [LightMem](https://arxiv.org/html/2510.18866v1)
- [MemoryGraft](https://arxiv.org/abs/2512.16962) · [MINJA](https://arxiv.org/html/2503.03704v2) · [Poison Once, Exploit Forever](https://arxiv.org/html/2604.02623) · [Memory Poisoning Attack and Defense](https://arxiv.org/abs/2601.05504) · [Unit 42](https://unit42.paloaltonetworks.com/indirect-prompt-injection-poisons-ai-longterm-memory/) · [Microsoft — AI recommendation poisoning Feb 2026](https://www.microsoft.com/en-us/security/blog/2026/02/10/ai-recommendation-poisoning/) · [InjecMEM](https://openreview.net/forum?id=QVX6hcJ2um)
- [Memory in the Age of AI Agents survey](https://arxiv.org/abs/2512.13564) · [Multi-Agent Memory (architecture perspective)](https://arxiv.org/html/2603.10062v1) · [Novel Memory Forgetting Techniques](https://arxiv.org/html/2604.02280)
- [LOCOMO benchmark](https://snap-research.github.io/locomo/) · [LongMemEval — ICLR 2025](https://arxiv.org/pdf/2410.10813)
- [Episodic Memory paper (long form)](https://arxiv.org/pdf/2502.06975) · [HeLa-Mem](https://arxiv.org/html/2604.16839) · [Memory Transfer Learning in coding agents](https://arxiv.org/html/2604.14004) · [Hindsight 20/20 (preprint mirror)](https://arxiv.org/html/2512.12818v1) · [Interactive Workflow Provenance](https://arxiv.org/html/2509.13978v2)
- [A Practical Guide to Memory for Autonomous LLM Agents — TDS](https://towardsdatascience.com/a-practical-guide-to-memory-for-autonomous-llm-agents/)
