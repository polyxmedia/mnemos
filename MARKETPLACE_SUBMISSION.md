# Marketplace submission packet

Three Claude Code marketplaces, ranked by likely impact-per-effort. The
manifests in this repo (`plugin.json`, `.claude-plugin/plugin.json`,
`marketplace.extended.json`, `.claude-plugin/marketplace.json`) are the
source of truth — every text asset below is also reflected in those files.
Tag a release before submitting (so each marketplace can pin to a version).

The Claude Code plugin loader reads the manifest at
`.claude-plugin/plugin.json`; the tonsofskills validator and the documented
`jq` checks read the root `plugin.json`. The two are kept byte-identical and
CI fails the build if they drift, so a version bump must touch both.

## Shared assets

**60-char tagline** (cards / one-line listings):
> Persistent memory and skills for AI coding agents.

**180-220 char description** (claudemarketplaces.com cards, GitHub
About):
> The mnemos server provides a persistent memory system for AI coding
> agents that compounds corrections into skills via deterministic
> promotion. Single Go binary, MCP-native, runs locally.

**Long description** (tonsofskills, GitHub topics):
> Persistent memory and learning-loop skills for AI coding agents.
> MCP-native with 20 tools, single Go binary, ships with measured
> efficacy harness.

**Install command** (the format claudemarketplaces.com renders):
```
claude mcp add --transport stdio mnemos $(which mnemos) serve
```
Or the one-liner that handles install + init in one go:
```
curl -fsSL https://raw.githubusercontent.com/polyxmedia/mnemos/main/scripts/install.sh | bash && mnemos init
```

**Keywords / topics**:
`memory mcp claude-code persistent-memory agent-memory knowledge-graph context skill observations provenance learning-loop golang`

**Category mapping**:
- claudemarketplaces.com → `MCP Servers > AI & LLM Tools`
- tonsofskills.com → `ai-ml-assistance`
- skillsmp.com → `Data & AI` (auto-assigned)

---

## 1. tonsofskills.com (most documented, do first)

**Path**: external-repo submission via GitHub issue on
`jeremylongshore/claude-code-plugins-plus-skills`.

**What's already in the repo**:
- `plugin.json` — strict 8-field allowlist, exactly the schema CI accepts
  (mirrored at `.claude-plugin/plugin.json` for the Claude Code plugin loader)
- `marketplace.extended.json` — full required block including
  `category: ai-ml-assistance`, `features`, `requirements`

**Submission flow**:
1. Open an issue on `jeremylongshore/claude-code-plugins-plus-skills`
   with title: `Add plugin: mnemos (persistent memory for AI coding agents)`
2. Body template:
   ```
   Repo URL: https://github.com/polyxmedia/mnemos
   Category: ai-ml-assistance
   Description: Persistent memory and learning-loop skills for AI coding
     agents. MCP-native with 20 tools, single Go binary, ships with measured
     efficacy harness.
   Enterprise validation score: <run their validator first, aim for B+ / 75+>

   Notes:
   - plugin.json and marketplace.extended.json live in repo root; the
     Claude Code plugin manifest is at .claude-plugin/plugin.json.
   - Bundles MCP server (20 tools) + Claude Code skill at
     .claude/skills/mnemos/SKILL.md, both shipped from the same Go binary.
   - Repo includes its own efficacy harness (`mnemos verify`) so future
     changes are tracked against measured numbers.
   ```
3. CI runs the validator on the manifests in this repo. If anything
   fails, fix and bump version + re-PR.

**Quality bar**: B+ / 75+ on their enterprise validation. Run their
validator locally before submitting; do not waste a review cycle.

---

## 2. claudemarketplaces.com (no public submission, email path)

**Path**: email outreach to `mert@vinena.studio`. The site auto-crawls
GitHub repos with a valid `.claude-plugin/marketplace.json` (now in
this repo at `.claude-plugin/marketplace.json`) but has a 500-install
quality gate; manual outreach is the realistic path at v0.5.0.

**Email draft** (subject + body):

> **Subject**: Memory MCP server for Claude Code listing — mnemos v0.5.0
>
> Hi Mert,
>
> mnemos is a persistent-memory system for AI coding agents that lands
> on the same wedge as the Memory MCP and Mem0 listings on
> claudemarketplaces, but takes a different approach: agent-curated
> corrections (tried/wrong_because/fix), deterministic promotion to
> skills (no LLM in the loop), bi-temporal storage, prompt-injection
> scanning at the write boundary.
>
> What's different from the existing memory listings:
> - Single Go binary, zero runtime dependencies (no Python, no Docker,
>   no CGO)
> - Ships measured numbers: behaviour A/B lift +40% on read side,
>   capture rate 7% → 53% after lever tuning, full efficacy harness in
>   the repo (`mnemos verify retrieval | behavior | capture`)
> - Cross-model: Claude Code, Cursor, Windsurf, Codex CLI via MCP
> - Bundles a Claude Code skill at `.claude/skills/mnemos/SKILL.md`
>   alongside the MCP server
>
> Adoption is early (v0.5.0, small star count) but the README leads
> with honest numbers, including where mnemos doesn't help, so future
> changes earn their keep against measurements rather than vibes.
>
> - Repo: https://github.com/polyxmedia/mnemos
> - Install: `claude mcp add --transport stdio mnemos $(which mnemos) serve`
>   then `mnemos init`
> - `.claude-plugin/marketplace.json` lives in repo root for auto-detection
>
> Would appreciate a listing under MCP Servers → AI & LLM Tools, peer
> with Memory and Mem0.
>
> Thanks,
> André

**Backup**: even without manual outreach, the
`.claude-plugin/marketplace.json` in this repo means the auto-crawler
will pick mnemos up once stars / installs cross the 500 threshold.
Email shortcut is cheaper than waiting.

---

## 3. skillsmp.com (auto-discovery, no submission)

**Path**: nothing to submit. The site is a GitHub crawler that indexes
1.2M+ skills from public repos. SKILL.md frontmatter is the listing.

**What's already done**: the `description:` field in
`.claude/skills/mnemos/SKILL.md` (and `.agents/skills/mnemos/SKILL.md`)
has been sharpened to surface for the queries that map to this skill:
"persistent memory", "agent memory", "MCP", "learning loop",
"corrections", "conventions", "context".

**Verification step**: a few days after the next tagged release, search
skillsmp.com for "mnemos" and "persistent memory" — if the skill
appears, no further action needed. If not, contact path is unclear from
the public site; emailing them is the fallback.

---

## Pre-submission checklist

Run before opening any issue / sending any email:

- [ ] Tag a release (e.g. `make release V=v0.5.1`) so each marketplace
      can pin to a version
- [ ] Verify the install command from a clean shell:
      `claude mcp add --transport stdio mnemos $(which mnemos) serve`
- [ ] Run tonsofskills' enterprise validator locally; aim B+ (75+)
- [ ] Sanity-check the three manifests parse:
      ```
      jq . plugin.json
      jq . .claude-plugin/plugin.json
      jq . marketplace.extended.json
      jq . .claude-plugin/marketplace.json
      diff plugin.json .claude-plugin/plugin.json   # must be identical
      ```
- [ ] Confirm SKILL.md frontmatter renders correctly when GitHub previews
      it (paste into a markdown viewer if unsure)

## Post-submission

- [ ] Once mnemos is listed on tonsofskills, add a "Listed on" badge to
      the README to seed the third-party-validation flywheel
- [ ] Drop the listing URLs into the next mnemos blog post / Show HN
- [ ] Track install rate from each source for 4 weeks; double down on
      whichever drives the most actual adopters (not just clicks)
