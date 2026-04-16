# Embeddings

Mnemos ships with optional hybrid retrieval: BM25 (lexical) + cosine
similarity (semantic), fused via Reciprocal Rank Fusion. Embeddings are
never required — turn them off and pure FTS5 still works — but turning
them on is a ~10pp recall bump on paraphrased queries (per LongMemEval
2026 research).

## Why embeddings live as BLOBs, not a vector DB

`modernc.org/sqlite` is a pure-Go port of SQLite and cannot load runtime
C extensions like `sqlite-vec`. We preserve the zero-CGO single-binary
promise by storing embeddings as little-endian float32 BLOBs in the
`observations.embedding` column and computing cosine similarity in Go
over BM25-filtered candidates (top-N × 3 by default).

The performance impact is negligible at realistic agent-memory sizes
(<100k observations). At 10k observations × 768 dims × 4 bytes, that's
30 MB of vectors — trivial to load and score.

## Providers

Three providers ship out of the box.

### Ollama (default when available)

Zero-config. On startup Mnemos probes `http://localhost:11434/`; if it
responds, the Ollama provider is enabled with the configured model
(default `nomic-embed-text`, 768 dims).

```bash
# install ollama, pull the model:
ollama pull nomic-embed-text
# start mnemos — embeddings on automatically
mnemos serve
```

### OpenAI-compatible

Works with OpenAI proper, Together.ai, vLLM, LM Studio, or any endpoint
implementing `/v1/embeddings`.

```toml
[embedding]
provider  = "openai"
base_url  = "https://api.openai.com/v1"
model     = "text-embedding-3-small"
dimension = 1536
api_key   = "sk-..."
```

### Noop (fallback)

Disables vector search entirely. Mnemos runs pure FTS5. No error, no
warning — a valid mode.

```toml
[embedding]
provider = "none"
```

## Auto-detect

Default provider is `"auto"`:

1. Probe Ollama on `base_url` (default localhost:11434) with a 500 ms
   timeout.
2. If reachable: use Ollama with the configured model.
3. Otherwise: fall back to Noop.

This is what "piss easy" looks like — if you have Ollama installed,
you get hybrid retrieval. If you don't, Mnemos still works.

## Backfill

If you enable embeddings after you've already saved observations, run:

```bash
mnemos embed backfill
```

This walks observations with `embedding IS NULL` in batches of 100, calls
the provider, and stores the resulting vectors. Restartable — failures
don't lose progress.

## Switching models

Each observation's embedding stores the model ID in `embedding_model`.
When you change models, a future backfill run can detect mismatches and
re-embed (planned — current backfill only fills NULL). For now: clear the
column manually before changing models, then re-backfill.

```sql
UPDATE observations SET embedding = NULL, embedding_model = NULL;
```

(Run `mnemos prune` first if you want to drop expired ones too.)

## Hybrid ranking maths

```
final_rank = alpha * rrf(bm25_rank) + (1 - alpha) * rrf(cos_rank)
rrf(rank)  = 1 / (k + rank)    # k=60 per the original RRF paper
```

Then the recency / importance / access multipliers from `internal/memory/
decay.go` are applied on top.

`alpha` defaults to `0.5` — BM25 catches exact identifiers and function
names; cosine catches paraphrases. Research says 0.5 is the sweet spot.
Tune via `[search].hybrid_alpha` in config: `1.0` = pure BM25, `0.0` =
pure vector.

## Performance

- `mnemos_save` embedding call is synchronous by default. A failure is
  non-fatal (observation persists; hybrid misses it until backfill).
- Ollama on an M-series Mac embeds a typical observation in <50 ms.
- OpenAI text-embedding-3-small adds ~200–400 ms network latency.
- Cosine over 100 candidates at 768 dims is <1 ms in pure Go.

## Privacy

OpenAI-compatible providers **send your observation content over the
network**. If that's not acceptable (most agent memory is personal or
proprietary code), use Ollama or set `provider = "none"`.
