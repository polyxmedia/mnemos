# Contributing to Mnemos

Thanks for considering a contribution. Mnemos aims to be the cleanest, fastest persistent memory layer for AI coding agents — code quality, test coverage, and developer experience are non-negotiable.

## Ground rules

- **Every change ships with tests.** Table-driven tests are the default pattern.
- **No new dependencies without discussion.** Mnemos ships as a single static binary with zero CGO. Anything that jeopardises that needs an issue first.
- **Every exported identifier has a godoc comment.** No exceptions.
- **All errors are wrapped** with `fmt.Errorf("context: %w", err)`. No naked returns.
- **All public methods accept `context.Context` first.** No background goroutines without a cancellable context.
- **`log/slog` only.** No third-party logging libraries.
- **No global state, no `init()` side effects, no reflection-based wiring.**

## Getting set up

```bash
git clone git@github.com:polyxmedia/mnemos.git
cd mnemos
make test        # runs full suite with race detector
make build       # produces bin/mnemos
make lint        # runs golangci-lint
```

Go 1.23+ required. Pure Go — no CGO, no system libraries.

## Workflow

1. **Open an issue first** for anything larger than a typo or docs fix. Saves both of us time.
2. **Branch from `main`**, name it `feat/<thing>` or `fix/<thing>` or `docs/<thing>`.
3. **One logical change per PR.** Drive-by refactors get their own PR.
4. **Run `make fmt lint test` before pushing.** CI runs the same checks.
5. **Write the commit message for the reader six months from now.** Explain the *why*, not the *what*.
6. **Link the issue** in the PR description.

## Architecture principles

- **Interfaces at boundaries, not inside services.** Storage, services, and transports communicate through interfaces. Internal service methods pass concrete types.
- **Dependency injection via constructors.** No service discovery, no globals, no singletons.
- **Services own their domain; storage owns the bytes.** Domain packages (`memory`, `session`, `skills`) define interfaces. `storage/sqlite` implements them.
- **Transports (`mcp`, `api`) are thin adapters.** They translate protocol-specific shapes into service calls. No business logic in transports.

Read `docs/ARCHITECTURE.md` before proposing architectural changes.

## What not to build

- **Embedding providers baked into core.** Hybrid retrieval hooks go through an optional `Embedder` interface. Core stays embedding-free by default.
- **Multi-tenant SaaS wiring.** Mnemos is local-first. Team/server deployments use the HTTP API, but auth and ACL live downstream.
- **LLM calls from inside the memory layer.** Reflection, skill extraction, and summaries are agent-provided. Mnemos stores; the agent thinks.
- **Automatic context injection.** Agents call `mnemos_context` when they want memory. We never push.

## Filing bugs

Include:
- Mnemos version (`mnemos version`)
- OS + architecture
- Minimal reproduction (a failing test is ideal)
- Actual vs expected behaviour

## Security

Do not file security issues as public GitHub issues. Email security@polyxmedia.com with details. We'll acknowledge within 72 hours.

## Code of conduct

Be kind. Disagree on the technical merits. Assume good intent. Don't make it personal.

## License

By contributing, you agree that your contributions will be licensed under the MIT License (see `LICENSE`).
