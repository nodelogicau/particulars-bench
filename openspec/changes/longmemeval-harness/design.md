## Context

The reference implementation is `particulars` (nodelogicau/particulars-cli). Its `serve --mcp` exposes the spec tool surface plus labelled extensions (`topics_list`, `unresolved_list`, `workspace_status`), and its `initialize` instructions carry the skill body, a generated register, and the workspace conventions document. `claim_assert` and `synthesis_create` already accept a backdated `timestamp`.

Upstream, `recall-query` defines `knowledge_recall(query)` as two-stage resolution with a required lexical baseline (#12 in the CLI) and an optional semantic stage (#13). Ordering *within* lexical matches is left to implementations — the first thing this harness will be used to choose.

There is one DKF implementation today. The adapter contract costs little now and is what makes "variant A vs B of one implementation" and "implementation X vs Y" the same comparison.

## Goals / Non-Goals

**Goals:**
- Measure agent + implementation on LongMemEval per question type, against baselines, at a cost an individual can repeat.
- Depend only on the DKF specification and the MCP protocol, never on an implementation's CLI.
- Make one expensive ingest serve many cheap answer comparisons.
- Report every input that moved a number.

**Non-Goals:**
- Leaderboard comparability (published judge is GPT-4o; this uses a recorded Claude judge with the same prompts).
- STATE-Bench, conformance testing, implementation performance (the CLI's `scale-benchmarks` cover its own).
- Running in CI against models.

## Decisions

### 1. Go, own repository
Same toolchain and MCP client as the reference implementation; LongMemEval's grading prompts are short and reproduced with attribution once the licence is checked. A separate repository keeps non-normative results out of the spec repository and API-key workflows out of both.

*Alternative:* Python reusing LongMemEval's scripts — revisit only if leaderboard comparability becomes a goal. *Alternative:* in the spec repository — rejected because results are not normative and the harness will iterate on implementation cadence.

### 2. The adapter is a command template
`--impl label='cmd … {workspace} …'`. The harness substitutes an absolute workspace path, also sets `DKF_WORKSPACE` and the working directory to it, and speaks MCP over stdio. Everything else is derived:

| Need | Source |
|---|---|
| create a workspace | harness writes `dkf.yaml` per `workspace-config` (scope `personal`, author `user`, harness `longmemeval-bench`) |
| identify the implementation | `initialize` → `serverInfo.name` / `version` |
| what the agent is taught | `initialize` instructions and `tools/list` descriptions, verbatim |
| read-only tool set | spec query tools by name: `particular_resolve`, `knowledge_recall`, `conflict_detect`, `lineage_trace` |
| ingest metrics | object files read per `workspace-layout` |

Streamable HTTP servers are an open question; stdio covers every implementation that exists.

### 3. The agent sees exactly what an MCP client sees
Tools are passed to the Messages API with names, descriptions, and schemas unchanged; the `initialize` instructions are the system prompt, followed by a short phase prompt. This measures the implementation's guidance (the `agent-guidance` surface), not a harness paraphrase.

*Alternative:* drive Claude Code headless — heavier, harder to account tokens, and adds its own prompt to what is measured.

### 4. Extensions are offered, labelled, separable
Tools outside the spec surface are extensions. In ingest every tool is offered. In answer, read-only extensions (by `readOnlyHint`) are offered by default and `--spec-tools-only` withholds them; the report lists each variant's offered extensions and their call counts. A gain from an implementation's extension is then visible as such.

### 5. Ingest replays sessions; answer is read-only
A fresh workspace per question; sessions replayed oldest first, one agent loop per session, each told the session's date and to backdate assertions. Ingest metrics are first-class — claims, particulars, topics, syntheses, and the fraction of claims timestamped on their session's date — because a poor ingest is a guidance finding, not a recall finding.

### 6. Cache key and validity without an implementation
Key: SHA-256 of dataset variant and checksum, question id, ingest model id, ingest prompt hash, and the ingest implementation's `serverInfo`, `initialize` instructions, and `tools/list` (on an empty workspace). Workspaces live under `os.UserCacheDir()/particulars-bench/longmemeval/<key>/` with a completion manifest — file paths and SHA-256s — written last. A workspace is reused only if its manifest exists and matches; no implementation's `validate` is needed. The answer implementation is not in the key.

### 7. Baselines in the harness
No memory; full context (oracle ceiling); BM25 top-k sessions (k recorded). No embedding RAG: no embeddings API at Anthropic, and BM25 is the stronger model-free comparison.

### 8. Judge and statistics
LongMemEval per-type prompts, abstention graded as abstention, judge model recorded with raw output. Every accuracy carries n and a Wilson 95% interval; `--repeats` reruns answers for variance.

### 9. Sampling and cost
Default oracle variant, 10 per type (abstention its own stratum), fixed seed; `--per-type`, `--seed`, `--types`, `--ids`, `--limit`, `--concurrency`. A plan (questions by type, sessions, estimated calls) prints before any model call; `--dry-run` stops there.

### 10. Reports
`bench-report/1` JSON and a Markdown table (variants as columns, types as rows, n and interval per cell; tokens, tool calls, extensions used). Written to `results/` (git-ignored); a report cited elsewhere is committed deliberately.

## Risks / Trade-offs

- **Claude judging Claude** → judge configurable and recorded; open question on an agreement check.
- **Ingest dominates** → ingest metrics in every report; answer variants share one ingest.
- **Cost** → oracle default, sample, plan first, cached ingest.
- **Model drift** → exact model ids recorded; comparisons claimed only within a report.
- **Small samples read as findings** → n and interval on every cell.
- **Spec drift** (tool names or `dkf.yaml` fields change) → the harness records the DKF version it wrote and pins the spec query-tool names in one place; a spec change is a harness change.
- **Only one implementation** → the adapter is the cheap part; the cost of not having it is a rewrite of cache keys and report schema later.

## Open Questions

- Default per-type count: is 10 enough to separate CLI variants A and B? Decide from the first run's intervals.
- Cross-judge or hand-label a subset to estimate judge bias?
- When LongMemEval_S (≈115k-token haystacks) joins the standard run.
- Streamable HTTP adapter.
- STATE-Bench: read `RUN_BENCHMARK.md`; can its "bring your own memory" interface be an MCP server?
- Should a conformance suite share this repository and its adapter?
