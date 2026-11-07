# Retrieval experiment: protocol and decision rule

Written 2026-11-07, before any data under these rules came in. The rules in
`internal/experiment/report.go` implement this page. If either changes after a
result has been looked at, say so here with the date and the reason.

## What is compared

Every new Claude Code conversation is assigned an arm by hashing its host
session id (`[retrieval] mode = "experiment"`).

- **push**: the prompt hook searches memory on every typed prompt and injects
  up to three hits that clear the score floor.
- **index**: the session starts with a titles-only list of saved memories and
  the agent opens what it needs with `mnemos_get`. The prompt hook still
  searches and logs what it would have shown, and shows nothing.

Both arms get the same session-start block, the same cues, the same pre-edit
search and the same capture directive. `mnemos_search` and `mnemos_get` load
with the session on both arms (`anthropic/alwaysLoad`), so pulling memory
costs no tool-search round trip.

Conversations run from Claude Code's own temp area (`tmp/claude-<uid>/...`)
are scripted runs. They get no arm and leave nothing in the event log.

## The window

`experiment_start = "2026-11-07T00:30:00Z"` in `~/.mnemos/config.toml`, just
after the build with the event log was installed. Everything before it is out: the first run mixed in scripted conversations, compared per
conversation means that one long conversation could swing, and measured
delivery from transcript hook labels that Claude Code later renamed.

Move `experiment_start` forward whenever what either arm receives changes.
Data from two versions of an arm cannot be pooled.

## Measures

Rates pool typed prompts across an arm's conversations. Intervals are 95%
percentile intervals from 2000 resamples of whole conversations with a fixed
seed, so the same data always gives the same report.

| Role | Measure |
| --- | --- |
| primary | saved memories mentioned in assistant replies, per 100 prompts |
| guardrail | prompts that push back on the agent's work, per 100 prompts |
| cost | characters mnemos injected, per prompt, from the event log |
| context | `mnemos_get` opens and `mnemos_search` calls, per 100 prompts |

A mention is a memory's distinctive title words appearing together in one
reply. It counts some coincidences, and the same title list applies to both
arms, so they land on both alike.

## When the result can be read

All of these must hold. The report prints which ones fail.

1. Each arm has at least 40 measured conversations and 400 typed prompts.
2. In each arm, the two longest conversations hold at most half the prompts.
3. In each arm, at least 80% of typed prompts have a prompt hook event. Less
   means the window reaches back before the event log, or a profile ran
   without the hooks.
4. The arms are within three standard deviations of an even split.

At the rate seen in the first week (about 40 real conversations a week), the
size rule is met around 2026-11-21 and the prompt count rule depends on how
long conversations run. Plan the read for 2026-12-05.

## Decision rule

1. If the primary interval is clear of zero in favour of **index**, switch to
   index, unless the guardrail interval is clear of zero against index. Then
   keep push.
2. If the primary interval is clear of zero in favour of **push**, keep push.
3. If the primary interval includes zero, choose on cost: the arm that injects
   fewer characters per prompt wins when that interval is clear of zero and
   its guardrail is clean. Otherwise keep push, the default with the fewest
   moving parts.

`mnemos experiment` prints the verdict and the decision under this rule.

## Precision, from blind labels

The arm comparison says which delivery gets memory into the work. It does not
say whether what was shown was right. For that, label a sample of logged
events blind:

```
mnemos experiment sample --n 60 --seed 20261205 --out ~/mnemos-sample.jsonl
```

Each line is one prompt and one memory the hook considered or showed. Lines
carry no arm, no score and no sign of whether the memory was shown, and the
memories of each prompt are shuffled. Set `"relevant"` to true when knowing
the memory would change or support the response to that prompt, else false.

Use two labellers who see only the file (separate agents work), and count a
pair relevant only when both say so. Then:

```
mnemos experiment score ~/mnemos-sample.jsonl
```

It reports precision of what each channel showed, the share of prompts where
any candidate was relevant, and the prompt hook replayed at other score floors
over both arms (the index arm logs what push would have shown).

Rules for acting on it:

- **Prompt hook floor**: move the floor to the lowest value whose precision
  interval has a lower bound of at least 25%. If no floor gets there, the push
  channel is not earning its context. Report that next to the arm decision
  and do not override it.
- **Cues**: label a cue sample too (`--channel cue`). Any cue whose fires are
  mostly irrelevant gets narrowed or removed with `mnemos cue rm`.

## Baseline from 2026-11-06

A gold set built by the same method before this protocol: real prompts,
labelled blind by separate agents against every live memory, pairs dropped
where the memory was newer than the prompt, and hook retrieval replayed on a
snapshot of the store, with half held out when tuning. The prompt hook was
about 6% precise. Gating on the gap between the top score and the third
peaked near 13%, so it is logged (`gap` in the event log) and not used. Most
relevant memories were same-project rules tied to an action, which is what
cues deliver.
