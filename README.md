# particulars-bench

Benchmarks for implementations of the [Dialectical Knowledge Format (DKF)](https://github.com/nodelogicau/particulars).

DKF implementations do no reasoning; an agent does. So efficacy is measured as a
system: an agent, taught only by what an implementation's MCP server says, using only
the tools the DKF specification defines. Anything that starts a stdio MCP server bound
to a workspace directory can be benchmarked; the reference CLI is one such command:

```sh
--impl cli='particulars serve --mcp --workspace {workspace}'
```

Scores here are comparable across runs of this harness, not to published LongMemEval
results (the sample and the agent differ, and by default the judge). Comparisons are claimed
only within one report.

## Running

Every phase that calls a model refuses to start without the API key of the provider it
calls (`ANTHROPIC_API_KEY` by default; see [Providers](#providers)), and prints a plan
(models, questions by type, sessions to replay, estimated model calls) before its first
call. `--dry-run` prints the plan and exits without a key.

```sh
make build
export ANTHROPIC_API_KEY=...
CLI='cli=particulars serve --mcp --workspace {workspace}'

# 1. Ingest: replay each question's sessions into a cached workspace.
./dist/particulars-bench ingest --impl "$CLI" --run results/first

# 2. Answer: implementations read a copy of the cached workspace; baselines start none.
./dist/particulars-bench answer --run results/first --impl "$CLI" --baselines no-memory,full-context,bm25
./dist/particulars-bench answer --run results/first --impl "cli-spec=particulars serve --mcp --workspace {workspace}" \
    --ingest-impl "$CLI" --spec-tools-only

# 3. Judge, then 4. report (results/first/report.json and report.md).
./dist/particulars-bench judge --run results/first
./dist/particulars-bench report --run results/first
```

The answer phase ingests any question not yet cached, so step 1 is optional; running it
separately lets one ingest serve many answer runs. A run directory fixes its dataset and
questions when it starts; later phases reuse them. Results go under `results/`, which is
git-ignored — commit a report deliberately when it is cited.

To compare two builds of one implementation, ingest once and answer with both:

```sh
./dist/particulars-bench answer --run results/ab --ingest-impl "$CLI" \
    --impl "a=$HOME/src/particulars-cli/dist/particulars serve --mcp --workspace {workspace}" \
    --impl "b=/tmp/particulars-b serve --mcp --workspace {workspace}"
```

### Flags

| Flag | Default | |
|---|---|---|
| `--impl label='cmd {workspace}'` | | implementation under test (repeatable) |
| `--ingest-impl label='cmd'` | the only `--impl` | the implementation that ingests for `answer` |
| `--variant` | `oracle` | `oracle` or `s` (LongMemEval_S, ≈115k-token histories) |
| `--per-type`, `--seed` | `10`, `1` | stratified sample; abstention (`_abs`) is its own stratum |
| `--types`, `--ids`, `--limit` | | narrow the sample |
| `--concurrency` | `4` | questions in flight |
| `--repeats` | `1` | answers per question per variant, for variance |
| `--spec-tools-only` | off | withhold every tool outside the DKF specification while answering |
| `--baselines` | | `no-memory`, `full-context`, `bm25` (`--bm25-k`, default 3) |
| `--model`, `--effort`, `--max-turns`, `--max-tokens` | `claude-opus-5-5`, `medium`, `30`, `16000` | ingest and answer model |
| `--provider`, `--base-url`, `--api-key-env`, `--extra-body` | `anthropic` | ingest and answer endpoint ([Providers](#providers)) |
| `--judge-model`, `--judge-effort` | `claude-opus-5-5`, `low` | judge |
| `--judge-provider`, `--judge-base-url`, `--judge-api-key-env`, `--judge-extra-body` | `anthropic` | judge endpoint |
| `--judge-label` | the judge model id | names a judge's verdicts in the run; `report` scores with it |
| `--dry-run` | | print the plan only |

## Providers

Each model role (the agent, which ingests and answers, and the judge) names a provider:

- `anthropic` (default): the Anthropic Messages API, key `ANTHROPIC_API_KEY`. `--effort` is
  sent as `output_config.effort`; the system prompt and tools are marked for prompt caching,
  and in a loop with tools so is the conversation so far.
- `openai`: any OpenAI-compatible Chat Completions endpoint at `--base-url` (default
  `https://api.openai.com/v1`), key `OPENAI_API_KEY`. Tools go as functions with the
  server's schema as `parameters`, unchanged, and strict mode off. `--effort` is sent as
  `reasoning_effort` only when given, since non-reasoning models reject it; the judge sends
  `temperature: 0`, as LongMemEval does, unless `--judge-effort` is given. `--max-tokens` is
  sent as `max_completion_tokens`; `--max-tokens 0` sends none, for servers that want
  `max_tokens` through `--extra-body` instead.

`--api-key-env` / `--judge-api-key-env` name another variable holding the key. A local server
that needs no key still needs that variable set to something, which keeps running a model
an explicit choice. `--extra-body` / `--judge-extra-body` take a JSON object merged into
every request of that role unchanged; the harness knows no router's fields.

```sh
# Claude agent, GPT-4o judge (LongMemEval's published judge).
./dist/particulars-bench judge --run results/first --judge-provider openai --judge-model gpt-4o

# An open-weight agent through OpenRouter, pinned to one upstream with no fallbacks.
export OPENROUTER_API_KEY=...
./dist/particulars-bench answer --run results/or --impl "$CLI" \
    --provider openai --base-url https://openrouter.ai/api/v1 --api-key-env OPENROUTER_API_KEY \
    --model qwen/qwen3-235b-a22b \
    --extra-body '{"provider": {"order": ["<upstream>"], "allow_fallbacks": false}}'

# A local vLLM or Ollama server.
export LOCAL_KEY=none
./dist/particulars-bench answer --run results/local --impl "$CLI" \
    --provider openai --base-url http://localhost:11434/v1 --api-key-env LOCAL_KEY --model llama3.1:70b
```

Each judge's verdicts are kept apart (`judgments/<judge-label>/<variant>.jsonl`), so re-judging
a run with another judge adds to it. `report` scores with one primary judge (`--judge-label`,
or the only one) and reports every other judge's agreement with it per question type.

Caveats:

- **Routers.** One model id at a router can be served by different upstreams, with different
  weights, quantisation, or templates. Pin the upstream with `--extra-body` and disable
  fallbacks. When a response names its upstream (OpenRouter's top-level `provider` field), each
  call records it; the report lists each role's upstreams and warns when there was more than one.
- **Tool calling varies.** A call of a tool that was not offered, or with arguments that are
  not valid JSON, goes back to the model as an error tool result without reaching the server,
  and counts as a call of that tool and a tool error. A tool call written as text
  counts as no call. Both show in the tool-call counts as findings, not crashes. Each ingest
  and answer record keeps its first three errors of each tool, with the arguments sent and
  the error returned (500 bytes of each), and the report's ingest table quotes one per tool.
- **Tokens.** Token counts come from each provider's tokeniser. The report labels token figures
  approximate when variants used different providers. On `openai`, input tokens include cached
  tokens (also reported separately), output tokens include reasoning tokens, and cache writes
  are zero.
- **Cache keys.** The ingest cache key includes the provider, base URL, and extra body when they
  differ from the Anthropic defaults, so Anthropic keys are as before providers existed.

## What an implementation must provide

Only an MCP server over stdio. For each workspace the harness substitutes the absolute
path for `{workspace}`, sets `DKF_WORKSPACE` and the working directory to it, and:

- writes `dkf.yaml` itself (`format: dkf/0.1`, scope `personal`, author `user`, harness
  `longmemeval-bench`) — no implementation command runs first;
- identifies the implementation by `initialize` → `serverInfo`;
- gives the model the `initialize` instructions as its system prompt (followed by a short
  phase prompt) and the `tools/list` tools with names, descriptions, and schemas unchanged;
- in the answer phase offers the specification's query tools (`particular_resolve`,
  `knowledge_recall`, `conflict_detect`, `lineage_trace`) and any other tool annotated
  `readOnlyHint: true`, the latter reported as extensions;
- reads ingest metrics (claims, particulars, topics, syntheses, fraction of claims dated on
  their session's date) from the object files per `workspace-layout`.

The DKF format and the specification's tool names are pinned in `internal/spec`.

## Caches

Under `$PARTICULARS_BENCH_CACHE`, else the user cache directory (`~/Library/Caches/particulars-bench`
on macOS, `~/.cache/particulars-bench` on Linux):

- `longmemeval/data/` — the dataset, downloaded on first use from
  [`xiaowu0162/longmemeval-cleaned`](https://huggingface.co/datasets/xiaowu0162/longmemeval-cleaned)
  at a pinned revision and verified against pinned SHA-256s. Never written inside this repository.
- `longmemeval/workspaces/<key>/` — ingested workspaces. The key hashes the dataset variant and
  checksum, the question id, the ingest model settings (provider, base URL, extra body, model, and
parameters), the ingest prompt, and the ingest
  implementation's `serverInfo`, `initialize` instructions, and `tools/list` on an empty workspace
  (its path and id replaced by placeholders). `manifest.json`, written last, lists every file's
  SHA-256; an entry is reused only if every file matches.

## Licence of reproduced material

LongMemEval ([xiaowu0162/LongMemEval](https://github.com/xiaowu0162/LongMemEval)) is released
under the MIT License, Copyright (c) 2024 Di Wu; the dataset is published under MIT as well.
The grading prompts in `prompts/judge-*.md` are reproduced verbatim from
`src/evaluation/evaluate_qa.py` at commit `9e0b455`, which the MIT License permits with this
notice. As there, a verdict is correct when the judge's reply contains "yes". The published
results use GPT-4o as judge; this harness defaults to a recorded Claude model with the same
prompts, and can judge with GPT-4o (`--judge-provider openai --judge-model gpt-4o`).

## Development

`make test` and `make lint` run unit tests and golangci-lint; neither calls a model, and CI
runs nothing else. Tests use a fake MCP server (`internal/fakeserver`) and a scripted model;
the reference-CLI test runs when `particulars` is on PATH.

Design and requirements: `openspec/changes/longmemeval-harness/`.
