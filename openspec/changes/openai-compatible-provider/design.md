## Context

`longmemeval-harness` built every model call on the Anthropic Go SDK. The `agent.Model` interface takes and returns Anthropic types. `impl.MessagesTool` produces Anthropic tool params. The loop reads `tool_use` blocks and appends `resp.ToParam()` to history, which also preserves thinking blocks, as current Claude models require when history is replayed. The judge and the baselines go through the same loop. The CLI refuses to run without `ANTHROPIC_API_KEY`.

The MCP side is already neutral: `tools/list` gives JSON Schema, and `CallTool` takes JSON arguments and returns text. Only the model side is tied to one vendor.

OpenAI-compatible Chat Completions (`POST {base}/chat/completions` with `tools` as functions) is the one protocol shared by OpenAI, OpenRouter, Together, vLLM, and Ollama.

## Goals / Non-Goals

**Goals:**
- Any phase's model (agent or judge) can be an Anthropic model, via the native API, or any OpenAI-compatible endpoint.
- The measurement contract is unchanged on every provider: tools as served, results unmodified, tokens and tool calls counted per call, everything that moved a number recorded.
- Anthropic-only runs behave, cost, and cache exactly as before.

**Non-Goals:**
- Other native vendor APIs (Gemini, Bedrock, Vertex), the OpenAI Responses API, and streaming.
- Normalising behaviour across providers. Differences between providers are what is being measured, so they are recorded, not hidden.
- Leaderboard comparability, which stays a non-goal even with a GPT-4o judge.

## Decisions

### 1. The adapter owns the conversation, not a neutral message list
The interface becomes a conversation that each adapter keeps in its native form:

```go
type Provider interface {
    Start(system []string, user string, tools []ToolDef, s Settings) Conversation
}
type Conversation interface {
    Send(ctx) (Turn, error)            // Turn: text, tool calls (id, name, raw JSON args), stop, usage
    AddToolResults([]ToolResult)       // id, text, isError
}
```

`agent.Run` drives tool calls, turn caps, and accounting against this interface. The Anthropic adapter keeps appending `resp.ToParam()`, so thinking blocks replay byte-for-byte. The OpenAI adapter appends the assistant message with its `tool_calls`, then one `role: tool` message per result.

*Alternative:* a neutral message list converted on every request. It was rejected because the conversion would be lossy (Anthropic thinking blocks, provider-specific fields), and a lossy history changes model behaviour, which is exactly what must not vary.

### 2. Chat Completions through `openai-go`, base URL configurable
Use the official `github.com/openai/openai-go` SDK (module path `github.com/openai/openai-go/v3`) with `option.WithBaseURL`. Chat Completions is what routers and local servers implement; the Responses API is not. The default base URL is `https://api.openai.com/v1`. The system prompt is one `system` message with the instructions and the phase prompt separated by a blank line, because some local chat templates accept only one. `--max-tokens` is sent as `max_completion_tokens`; `--max-tokens 0` sends none, so that a server wanting `max_tokens` can get it through `--extra-body`. A server error carries the server's message, which the SDK's error text leaves out.

*Alternative:* raw HTTP, rejected; the SDK covers base-URL override, retries, and typed errors.

### 3. Tool schemas pass verbatim, strict mode off
An MCP tool becomes `{type: function, function: {name, description, parameters: <inputSchema>}}` with the schema unchanged. `strict: true` is not set, because strict mode requires `additionalProperties: false` and every property required, which would rewrite the server's schema. The existing verbatim test extends to the OpenAI rendering.

### 4. One neutral tool definition from `impl`
`impl` produces `ToolDef{Name, Description, Schema map[string]any}`. Each adapter renders it in its own format. The answer-phase filter (`AnswerTools`) is unchanged.

### 5. Provider parameters are sent only where they are understood
`--effort` maps to Anthropic `output_config.effort`. On `openai` it maps to `reasoning_effort` only when given explicitly, because non-reasoning models such as GPT-4o reject the field. Its default for `openai` is unset. Prompt-cache markers are Anthropic-only; OpenAI caches automatically. A run records each role's provider parameters as sent.

The judge on `openai` sends `temperature: 0` and LongMemEval's settings for the grading call, unless `--judge-effort` is given (reasoning models reject `temperature`).

### 6. Routers: pass-through request fields, record the upstream
`--extra-body` and `--judge-extra-body` take a JSON object that is merged into each request body and recorded. For OpenRouter, `{"provider": {"order": ["<upstream>"], "allow_fallbacks": false}}` pins the upstream. The harness does not hard-code any router's fields. When a response carries a top-level `provider` field (as OpenRouter's does), its value is recorded per call. A run whose calls were served by more than one upstream gets a report warning.

*Alternative:* a router-specific `--route` flag, rejected; it would bake one vendor's schema into the harness.

### 7. Keys per role, refusal per provider
Each role has a key variable: `ANTHROPIC_API_KEY` for `anthropic`, `OPENAI_API_KEY` for `openai`, overridable with `--api-key-env` / `--judge-api-key-env` (for example `OPENROUTER_API_KEY`). A phase refuses to start, before creating anything, when the variable for any role it will call is unset, and the message names that variable. Local servers that need no key still need the variable set to any value, which keeps opt-in explicit.

### 8. Token accounting maps onto the existing fields
`input_tokens` = `prompt_tokens` (which includes cached), `cache_read_tokens` = `prompt_tokens_details.cached_tokens`, `output_tokens` = `completion_tokens` (which includes reasoning tokens), and `cache_write_tokens` = 0. Per-call records keep provider-native totals, so cross-provider token comparisons are labelled approximate in the report.

### 9. Stop reasons
`finish_reason: tool_calls` continues the loop. `stop` ends it. `length` ends it as capped. `content_filter` is an infrastructure failure, like an Anthropic `refusal`. Some servers finish with `stop` while returning tool calls; the calls are answered and the loop continues, since they are what the model asked for. Empty arguments are read as `{}`. The Anthropic adapter keeps its stop handling exactly as before (`max_tokens` is not marked capped).

### 10. Cache-key encoding unchanged for Anthropic
`agent.Settings` gains `provider`, `base_url`, `extra_body`, and `temperature` (set only for the `openai` judge), each omitted from JSON when it holds the Anthropic default. An Anthropic-only ingest therefore hashes exactly as before, and existing cached workspaces stay valid. Any non-default provider setting enters the key.

### 11. Scope: every phase
The adapter serves ingest, answers, baselines, and the judge. The judge and the baselines are tool-free and low-risk. Agent phases on other providers are what measures whether an implementation's guidance transfers beyond Claude, and the adapter costs the same either way.

*Alternative:* judge and baselines only, rejected; the agent case is what the router reach is for, and limiting it would be an artificial restriction.

### 12. Judgments per judge
Verdicts go to `judgments/<judge-label>/<variant>.jsonl`, the label defaulting to the judge model id (`--judge-label` overrides it, e.g. for one model through two routers). `report` scores with one primary judge: the one `--judge-label` or `--judge-model` names, else the only one, else the default judge model's. Every other judge is reported with its agreement with the primary per question type, on the answers both graded without failure. Runs from before this change have no paid results, so the old `judgments/<variant>.jsonl` layout is not read.

## Risks / Trade-offs

- **Router serves different weights under one model id** → upstream pinning via `--extra-body`, the upstream recorded per call, and a report warning when calls were served by more than one upstream.
- **Open-weight tool calling varies (malformed JSON arguments, tool calls written as text)** → unparseable arguments go back as an error tool result, as the server would return them, and are counted. Tool calls written as text count as no call. Both show in the tool-call counts, so they read as findings, not crashes.
- **OpenAI-compatible servers differ in what they accept** (`reasoning_effort`, `temperature`, `parallel_tool_calls`) → send the minimum by default and let `--extra-body` add more. A 400 from the server is an infrastructure failure that carries the server's message.
- **Token counts are not comparable across tokenisers** → the report labels cross-provider token columns approximate.
- **Larger surface to keep working** → both adapters are covered by the scripted-model tests through one interface, and the OpenAI adapter additionally against a local `httptest` server that speaks Chat Completions. CI still calls no model.

## Migration Plan

No data migration. Anthropic runs and their cache entries are unaffected (decision 10). New report fields are additive under `bench-report/1`. Rollback is reverting the change; runs recorded with a non-Anthropic provider would then lack an adapter to re-run them, which affects only re-running, not reading them.

## Open Questions

- Should the judge default to `gpt-4o` when `--judge-provider openai` is given without `--judge-model`, to match LongMemEval?
- Do any targeted servers reject `tool_choice: auto` or the `tools` field entirely, needing a capability probe before the run?
