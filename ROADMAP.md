# Roadmap

Open work on Mnemos. Pick anything here and ship it. If you want to claim an item, open an issue or drop a PR.

## Shipped

- [x] v0.1.0 — storage, memory service, MCP server (14 tools), installer, HTTP API
- [x] v0.2.0 — skill packs, session replay, bi-temporal timestamp precision fix

## Next up — high-leverage

- [ ] **`mnemos digest`** — nightly markdown summary ("yesterday you saved 12 observations, promoted 2 skills, dreamed at 2am"). Ship as `mnemos digest --since 24h`. Small feature, big "numbers on the screen" moment for devs who like to see their memory store earn its keep.
- [ ] **VS Code extension** — sidebar that reads `mnemos_touch` heat map, surfaces corrections as hover hints on files with saved corrections. Status-bar widget: "Session active · 12 obs · 3 corrections matched". Makes Mnemos *visible* in the editor, which is what gets shared.
- [ ] **Homebrew tap** — create `polyxmedia/homebrew-tap`, add `HOMEBREW_TAP_GITHUB_TOKEN` to Actions secrets, uncomment the brews block in `.goreleaser.yml`. One-line install becomes `brew install polyxmedia/tap/mnemos`.
- [ ] **Skill pack registry** — a static site (or a GitHub repo) listing public skill packs. Registry entries point at raw JSON URLs. `mnemos skill search <query>` queries it.

## Interesting but bigger

- [ ] **Team federation** — opt-in sync of observations tagged `shared:true` to a team HTTP endpoint. Other team members pull conventions + corrections the team agreed on. Boundary-aware: personal stays local.
- [ ] **Promptware injection leaderboard** — bench our safety scanner against published injection corpuses. Ship the benchmark as `mnemos safety bench`. Turns a bullet-point claim into a number.
- [ ] **Session pre-warm for the MCP client side** — right now we return the prewarm block but clients might not render it prominently. Could ship an example Claude Code skill / slash command that reads the prewarm and restates it.
- [ ] **Memory-aware prompt compression** — use importance/recency/access scores to produce a losslesss-where-it-matters compressed snapshot of memory in N tokens. Expose as `mnemos_pack_context`.

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
