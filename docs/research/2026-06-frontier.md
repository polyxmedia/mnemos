# Agent Memory Frontier — June 2026

Research notes from a deep-research pass (105 agents, 23 sources, 112 claims extracted, 25 adversarially verified with 3-vote refutation: 21 confirmed, 4 killed). Follow-up to `2026-04-frontier.md`, focused on what moved since April and what the six bets are missing. Kept here so future decisions trace back to evidence.

Dated June 2026. Re-run before committing to anything here if more than six months have passed.

## TL;DR

The field moved from "can agents remember" to "can memory be governed and proven useful." Three verified findings dominate:

1. **OpenMemory (CaviraOSS) has claimed the local-first SQLite MCP niche.** "Local-first SQLite memory over MCP" is now table stakes, not a differentiator. Our defensible ground is code-awareness, governance, and measured usefulness.
2. **Causal attribution and in-loop usefulness measurement are explicitly named unclaimed frontiers** in the literature. This is the strongest external validation any of our bets has received: it is Bet 4, almost verbatim.
3. **Our corrections-to-skills promotion gate is measurably behind the state of the art.** Skill-Pro (ICML 2026 spotlight) gates skill admission on verified outcomes over past trajectories; `promote.go` gates on a count of 3 plus trust tier, with no outcome check.

## 1. Competitive update since April

Only OpenMemory survived adversarial verification; no claims about mem0, Zep/Graphiti, Letta, MemPalace, agentmemory, Serena, Cursor, Windsurf, or Claude Code native memory for May–June made it through, so the competitive map beyond OpenMemory is unverified (see Open questions).

**OpenMemory (CaviraOSS)** — 4.2k stars, TypeScript/Python, v1.2.3 Dec 2025, mid-rewrite ("expect breaking changes"). Verified against README and official docs:

| Has | Lacks |
|---|---|
| Self-hosted, local-first (SQLite or Postgres) | Transaction-time axis (uni-temporal only: `valid_from`/`valid_to`) |
| Native MCP server (Claude Desktop, Cursor, Windsurf) | Code-awareness / symbol anchoring |
| Temporal knowledge graph with point-in-time queries | Causal attribution |
| Multi-sector memory (episodic/semantic/procedural/emotional/reflective) | Provenance / trust governance |
| Per-sector adaptive decay and reinforcement | Corrections-to-skills machinery, hook guards |
| Composite retrieval scoring (salience + recency + coactivation) | |

Distinct from mem0's separately named "OpenMemory MCP" product.

## 2. Verified academic findings

All votes are adversarial 3-vote panels; "3-0" means zero refutations.

| Finding | Source | Vote | Mnemos relevance |
|---|---|---|---|
| **Memory governance quality, not recall capacity, is the differentiator for secure agents.** No published architecture covers all nine governance primitives the survey identifies. Confidentiality, availability, store/forget-phase security, and benign-persistence failures are under-researched; write/retrieve-time integrity attacks are saturated. | [Mnemonic sovereignty survey, arXiv 2604.16548](https://arxiv.org/abs/2604.16548) (Apr 2026) | 3-0 ×3 | We partially instantiate sovereignty (write governance via quarantine, recoverable forgetting via invalidate-never-delete) but not read confidentiality. A self-scored governance scorecard is both roadmap and positioning. Window is narrowing: May–June follow-on work exists. |
| **Static trust cutoffs fail in both directions; adaptive trust-threshold calibration is named open future work.** Composite trust scoring across orthogonal signals plus trust-aware retrieval with temporal decay matches our trust_tier + quarantine design family. | [arXiv 2601.05504](https://arxiv.org/abs/2601.05504) (Jan 2026, EHR domain only) | 3-0 ×2 | Validates shipped Bet 2. Next step: composite multi-signal trust score layered over the discrete tiers. Medium confidence (single non-coding domain). |
| **Explicit, interpretable write-time admission control beats opaque LLM-driven storage policies.** A-MAC scores five factors before storage: future utility, factual confidence, semantic novelty, temporal recency, content type prior. F1 0.583 on LoCoMo with 31% latency reduction. | [A-MAC, arXiv 2603.04549](https://arxiv.org/abs/2603.04549) (ICLR 2026 MemAgent workshop) | 3-0 ×2 | Sits upstream of trust_tier. A deterministic admission gate gives a rule-visible "why was this stored" no competitor surfaces. Directly attacks the mem0 97.8%-junk failure at the front door. |
| **Recall benchmarks do not predict agentic usefulness.** Models near-saturated on LoCoMo collapse when memory is embedded in agentic tasks (MemoryArena; best category-average task SR ~0.19). Causally grounded retrieval is an open problem: "semantic similarity answers what looks like this, not what caused this." No surveyed system does causal retrieval or systematic attribution. Selective forgetting is handled crudely everywhere (hard TTLs, eviction, or nothing). | [MemoryArena, arXiv 2602.16313](https://arxiv.org/abs/2602.16313) · [survey, arXiv 2603.07670](https://arxiv.org/abs/2603.07670) · [MemoryAgentBench, arXiv 2507.05257](https://arxiv.org/abs/2507.05257) | 3-0 ×3 | The strongest validation of Bet 4. The survey even proposes the causal-parent metadata our `derived_from` graph already has. Also supports the active-decay roadmap item. |
| **Outcome-gated skill admission sets the quality bar for procedural memory.** Skill-Pro represents skills as (activation, procedure, termination) tuples admitted via a "PPO Gate" (clipped-surrogate verification over past trajectories). Results: 816 stored tokens vs 40k–392k baselines; 0.925 in-domain reuse vs 0.091 (G-Memory); 0.900 cross-agent reuse across different backbone models. | [Skill-Pro, arXiv 2602.01869](https://arxiv.org/pdf/2602.01869) (ICML 2026 spotlight) | 3-0 ×2, 2-1 | Our `promote.go` gate (count ≥ 3 + trust tier, no outcome check) is behind this bar — verified in our own code during the pass. Cross-agent transfer strengthens Bet 3's model-agnostic spec story. Caveat: measured on text games, not coding; reuse rate measures selection, not success. |
| **Role-aware memory placement dominates in multi-agent systems.** LEGOMem splits procedural memory into orchestrator-level (full-task) and subagent-level (subtask) stores. Procedural memory adds +12.6 to +13.4 absolute success points on OfficeBench; removing orchestrator memory costs far more than removing agent memory; memory lets smaller-model teams beat larger memory-less ones. | [LEGOMem, arXiv 2510.04851](https://arxiv.org/pdf/2510.04851) (Microsoft, AAMAS 2026) | 3-0 ×4 | Upgrades the roadmap's SubagentStart/Stop item: inject at the orchestrator (planning/delegation) layer first. "Local memory disproportionately boosts cheaper local models" is a natural mnemos narrative. Oct 2025 paper, included as landmark. |
| **Runtime tool synthesis is demonstrated in coding agents.** Live-SWE-agent evolves its own scaffold at runtime, synthesizing custom tools as a first-class action, LLM-agnostic. | [Live-SWE-agent, arXiv 2511.13646](https://arxiv.org/pdf/2511.13646) (UIUC) | 3-0 | Adjacent territory. The companion claim that cross-task tool persistence is unclaimed was REFUTED 0-3; verify their persistence story before treating "tool artifacts as skills" as differentiation. |

## 3. Refuted in verification (do not build on these)

1. **"Pre-existing legitimate memories dramatically reduce poisoning attack effectiveness"** — 0-3. Not what arXiv 2601.05504 shows.
2. **"Multi-agent memory governance is wide open per the 2603.07670 survey"** — 1-2. The role-aware gap claim rests on LEGOMem's positioning only.
3. **"Live-SWE-agent discards evolved tools after each task; persistence is named future work"** — 0-3. Their persistence story is unresolved, not absent.
4. **"arXiv 2602.01869 was listed as ProcMEM"** — 1-2. Title/venue metadata dispute; the Skill-Pro findings themselves verified separately.

## 4. New candidates beyond the six bets

Ranked by leverage × novelty × feasibility for a small local-first Go project:

| # | Candidate | What | Builds on | Effort |
|---|---|---|---|---|
| 1 | **Outcome-gated skill promotion** | Gate dream-pass promotion on observed outcomes (correction recurrence after the skill existed, test pass rates, later Bet 6 replay evidence) instead of raw frequency. Wire `skill_score`'s existing machinery into the gate instead of leaving it post-hoc. | Skill-Pro; fixes verified weakness in `internal/dream/promote.go` | Small |
| 2 | **Role-aware subagent memory routing** | Orchestrator-first injection for multi-agent workflows; subagent learnings cascade second. Replaces the flat SubagentStart/Stop roadmap item. | LEGOMem | Medium |
| 3 | **Interpretable admission gate** | Deterministic five-factor scoring (utility, confidence, novelty, recency, type prior) upstream of trust_tier, logged into provenance so every memory answers "why was this stored." | A-MAC | Small–medium |
| 4 | **Composite adaptive trust scoring** | Multi-signal trust score layered over the discrete tiers without breaking provenance auditability. | arXiv 2601.05504 | Medium |
| 5 | **Governance scorecard** | Self-assessment against the nine mnemonic-sovereignty primitives, published; close the cheap gaps. | arXiv 2604.16548 | Small (doc) + gaps TBD |
| 6 | **Agent-synthesized tools as skills** | Capture runtime-synthesized tools as durable skills. UNRESOLVED: verify Live-SWE-agent's persistence story first. | Live-SWE-agent | Unknown |

## 5. Implications for the existing bets

- **Bet 4 (causal attribution): promote it up the order.** Strongest external validation of anything planned; also the answer to "is mnemos actually useful," which the store cannot currently answer (passive injection is unlogged; observations have no feedback loop, unlike skills).
- **Bet 1 (symbol anchoring): still unclaimed, still the launch headline.** But ship the measurement substrate first so Bet 1's launch claim is provable rather than asserted.
- **Bet 2: validated and shipped.** Candidates 3 and 4 above are its natural extensions.
- **Bet 3: strengthened.** Skill-Pro's 0.900 cross-agent skill reuse is empirical support for model-agnostic shareable procedural memory.

## 6. Caveats

Nearly all academic findings rest on non-peer-reviewed arXiv preprints; Skill-Pro (ICML 2026 spotlight) and LEGOMem (AAMAS 2026) are the strongest venues. All quantitative results are author-self-reported with no independent replication found. None of the headline numbers come from coding workloads: Skill-Pro used text games/ALFWorld, LEGOMem used OfficeBench with GPT-4o-mini as the "small" model, the poisoning paper used clinical EHR data. Extrapolation to our setting is directional. Several relevance clauses ("maps directly onto", "unclaimed niche") are analyst inference layered on accurate source quotes, not statements in the sources.

## 7. Open questions

1. What is Live-SWE-agent's actual cross-task tool-persistence story? Determines whether candidate 6 is viable.
2. What did mem0, Zep/Graphiti, Letta, MemPalace, agentmemory, Serena, Cursor, Windsurf, and Claude Code native memory ship in May–June 2026? No claims survived verification; the map beyond OpenMemory is unverified.
3. Can an outcome-based admission gate (the PPO Gate's function) be implemented without action log-probabilities, using test-pass rates, correction recurrence, or Bet 6 replay as the advantage signal, and does it hold on real coding workloads?
4. How does composite multi-signal trust scoring interact with discrete trust tiers without breaking provenance auditability?

## Sources

Primary academic: [2604.16548](https://arxiv.org/abs/2604.16548) · [2601.05504](https://arxiv.org/abs/2601.05504) · [2603.04549](https://arxiv.org/abs/2603.04549) · [2603.07670](https://arxiv.org/abs/2603.07670) · [2602.16313](https://arxiv.org/abs/2602.16313) · [2507.05257](https://arxiv.org/abs/2507.05257) · [2602.01869](https://arxiv.org/pdf/2602.01869) · [2510.04851](https://arxiv.org/pdf/2510.04851) · [2511.13646](https://arxiv.org/pdf/2511.13646)

Competitive: [OpenMemory (CaviraOSS)](https://github.com/CaviraOSS/OpenMemory) · [OpenMemory docs](https://openmemory.cavira.app) · [mem0 OpenMemory MCP (distinct product)](https://mem0.ai/blog/introducing-openmemory-mcp) · [State of AI Agent Memory 2026](https://mem0.ai/blog/state-of-ai-agent-memory-2026)
