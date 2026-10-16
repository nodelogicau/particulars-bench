## Why

Nothing measures whether DKF works as memory for an agent. DKF implementations do no reasoning, so efficacy can only be measured as a system: an agent, taught by what an implementation's MCP server says, using the tools the DKF specification defines. Everything that system depends on is specified upstream — the tool surface ("MCP Server Tools"), what the tools teach (`agent-guidance`), the workspace files (`workspace-layout`, `workspace-config`), and what a free-text recall means (`recall-query`) — so the benchmark belongs beside the spec, not inside one implementation.

The first consumers are the reference CLI's issues #12 (lexical baseline for `knowledge_recall(query)`) and #13 (optional semantic cache): both should land against a harness that already exists, compared on identical workspaces.

LongMemEval (500 questions over timestamped chat histories; information extraction, multi-session reasoning, knowledge updates, temporal reasoning, abstention) exercises what DKF claims to be good at — supersession, `current` by timestamp, empty recall — and where it is weakest today: a free-text question must resolve to a particular.

## What Changes

- **A Go harness** with phases `ingest`, `answer`, `judge`, `report`, each separately runnable.
- **An implementation is a command.** `--impl <label>='<command with {workspace}>'` starts a stdio MCP server bound to that workspace. The harness depends on nothing else an implementation provides: it writes `dkf.yaml` itself, identifies the implementation from the MCP `initialize` `serverInfo`, selects tools by the names the DKF specification defines, and reads ingest results from the files.
- **Ingest** replays each question's sessions through one implementation, backdating assertions to session dates; the resulting workspace is cached under a key of everything that determined it, so one ingest serves every answer-phase comparison.
- **Answer** runs any implementation against a copy of a cached workspace with read-only tools only. Spec query tools are always offered; read-only extension tools are offered by default, labelled, and can be withheld, so an extension's effect is separable from DKF's.
- **Baselines:** no memory, full context, and BM25 over raw sessions.
- **Judge** with LongMemEval's per-type prompts and a recorded model.
- **Reports** as dated JSON plus Markdown: accuracy per question type with n and a 95% interval, tokens and tool calls per answer, ingest metrics, and every configuration that moved the number.
- **Opt-in and cost-bounded:** oracle variant and a stratified sample by default, a printed plan and `--dry-run` before spending, nothing runs without `ANTHROPIC_API_KEY`.
- **Not in scope:** STATE-Bench (deferred until its plug-in interface is read); conformance testing (the adapter contract here is a natural base for it, later); leaderboard-comparable LongMemEval scores.

## Capabilities

### New Capabilities
- `memory-benchmark`: the implementation adapter contract, dataset handling and sampling, ingest and its cache, the read-only answer phase, baselines, judging, and the report contract.

### Modified Capabilities
(none — new repository)

## Impact

- New repository `nodelogicau/particulars-bench`: Go module (Anthropic Go SDK, `modelcontextprotocol/go-sdk` client), prompts, `results/` (git-ignored), CI for lint and unit tests only — no model calls in CI.
- The spec repository's README links here. The reference CLI documents how to point this harness at a local build; its own scale benchmarks stay in that repository (change `benchmark-harness` there).
- The LongMemEval dataset is downloaded to the user cache directory on first use and checksum-verified; it is never committed.
