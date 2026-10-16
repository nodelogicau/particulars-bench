## Why

The harness only runs Anthropic models: the agent loop, the baselines, and the judge all speak the Messages API, and the opt-in check names `ANTHROPIC_API_KEY`. That rules out two comparisons worth having. The first is a GPT-4o judge with LongMemEval's own prompts, which is the configuration behind the published results. The second is whether what an implementation teaches through MCP works for agents other than Claude. One adapter for the OpenAI-compatible Chat Completions API, pointed at a configurable base URL, reaches OpenAI, routers (OpenRouter, Together), and local servers (vLLM, Ollama).

## What Changes

- **A provider per model role.** The agent model (ingest and answers) and the judge model each name a provider: `anthropic` (the default, unchanged) or `openai`, meaning any OpenAI-compatible Chat Completions endpoint. Roles may use different providers, e.g. a Claude agent with a GPT-4o judge.
- **Base URL.** An `openai` role takes a base URL (default `https://api.openai.com/v1`). The API key comes from `OPENAI_API_KEY`, or from a variable the run names for a router.
- **Router pinning.** Extra request-body fields pass through unchanged and are recorded, which is how a router's upstream provider is pinned and its fallbacks disabled. The harness hard-codes no router's schema. When the response says which upstream served it, the harness records that.
- **The same contract on every provider.** MCP tools pass to the model with names, descriptions, and schemas unchanged. Tool calls are forwarded and results returned unmodified. Tokens and tool calls are counted per call. Provider-specific parameters (Anthropic `effort` and prompt caching; OpenAI `reasoning_effort`) are recorded, and are sent only where the provider accepts them.
- **BREAKING (spec): opt-in names the provider's key.** A phase refuses to start when the key for any provider it would call is unset, instead of always requiring `ANTHROPIC_API_KEY`. A run whose roles are all `anthropic` behaves exactly as before.
- **Reports** record each role's provider, base URL, model id, upstream provider when known, and provider parameters. `bench-report/1` gains these fields. Older readers ignore them; the schema name stays the same.
- **Not in scope:** native SDKs for other vendors (Gemini, Bedrock, Vertex), the OpenAI Responses API, embeddings, and claims that scores are leaderboard-comparable. A GPT-4o judge narrows the gap but does not close it, because the sample and the agent still differ.

## Capabilities

### New Capabilities
(none)

### Modified Capabilities
- `memory-benchmark`: opt-in execution names the selected providers' keys; the agent and judge models are selected by provider, model, and base URL; the "agent sees what an MCP client sees" requirement holds for every provider; the report contract records provider details per role.

## Impact

- `internal/agent`: the `Model` interface moves from Anthropic SDK types to a provider-neutral conversation (tool definitions, tool calls, results, usage) whose history each adapter keeps in its native form. The Anthropic adapter keeps today's behaviour. A new OpenAI-compatible adapter uses the official `openai-go` SDK with its base-URL option.
- `internal/impl`: the MCP→tool translation produces the neutral tool definition. Each adapter renders it as an Anthropic tool or an OpenAI function.
- `internal/cli`: new flags `--provider`, `--base-url`, `--api-key-env`, `--extra-body`, and their `--judge-*` counterparts, plus the provider-aware key check.
- `internal/ingest`: the cache key includes the ingest provider, base URL, and extra request fields. These are omitted when they hold the Anthropic defaults, so existing cached workspaces stay valid.
- `internal/report` and `README`: provider fields, and documentation of the providers and the reproducibility caveats of routers.
- New dependency: `github.com/openai/openai-go`. CI still calls no model.
- Depends on `longmemeval-harness`, which should be archived first so that `memory-benchmark` exists in `openspec/specs/` for this change's delta to modify.
