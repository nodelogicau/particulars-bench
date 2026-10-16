## 1. Repository

- [ ] 1.1 Go module, `cmd/particulars-bench` entry point with phases `ingest`, `answer`, `judge`, `report` and shared flags (`--impl`, `--per-type`, `--seed`, `--types`, `--ids`, `--limit`, `--concurrency`, `--dry-run`, `--repeats`, `--spec-tools-only`); the `ANTHROPIC_API_KEY` refusal.
- [ ] 1.2 Makefile (`build`, `test`, `lint`) and CI running unit tests and `golangci-lint` only — no model calls.
- [ ] 1.3 Create `nodelogicau/particulars-bench` on GitHub and link it from the spec repository's README.

## 2. Dataset and sampling

- [ ] 2.1 Check LongMemEval's licence for reproducing its grading prompts; record the finding in the README.
- [ ] 2.2 Download the oracle and S variants to the user cache directory on first use; pin and verify checksums.
- [ ] 2.3 Parse questions, types, `_abs` flags, question dates, and dated sessions.
- [ ] 2.4 Stratified seeded sampling and the plan printer; tests for same-seed-same-ids and `--dry-run` making no calls.

## 3. Adapter and MCP loop

- [ ] 3.1 Write `dkf.yaml` per the current `workspace-config` spec; pin the DKF version and spec query-tool names in one place.
- [ ] 3.2 Start an implementation from its command template (`{workspace}`, `DKF_WORKSPACE`, working directory); connect with the go-sdk client; capture `serverInfo`; record start failures with stderr.
- [ ] 3.3 Translate `tools/list` to Messages API tools verbatim; the answer-phase filter (spec query tools, read-only extensions, `--spec-tools-only`); test against the reference CLI.
- [ ] 3.4 Tool-use loop: forward calls, cap turns, record tokens per call and tool calls by name; `initialize` instructions as system prompt.

## 4. Ingest

- [ ] 4.1 Ingest phase prompt (session date, backdate assertions, the user as a particular) and its hash.
- [ ] 4.2 Fresh workspace per question; sessions replayed oldest first.
- [ ] 4.3 Cache key and completion manifest; reuse only on a matching manifest; tests for guidance-change invalidation and interrupted ingest.
- [ ] 4.4 Ingest metrics read from object files per `workspace-layout`.

## 5. Answer, baselines, judge

- [ ] 5.1 Answer phase on a copy of the cached workspace; test the cache still matches its manifest afterwards.
- [ ] 5.2 Baselines: no memory, full context, BM25 top-k.
- [ ] 5.3 Judge with per-type prompts and a recorded model; keep raw output.

## 6. Report

- [ ] 6.1 `bench-report/1` JSON with Wilson 95% intervals and infrastructure failures counted separately.
- [ ] 6.2 Markdown renderer: variants as columns, types as rows, n and interval per cell, tokens, tool calls, extensions used, ingest metrics summary.

## 7. First run

- [ ] 7.1 Run the default sample against no memory, full context, BM25, and the current CLI release (with and without `--spec-tools-only`); record cost and wall time in the README.
- [ ] 7.2 Settle the per-type default from the intervals; comment on particulars-cli #12 and #13 with how to report against the harness.
