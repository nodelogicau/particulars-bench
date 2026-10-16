## 1. Provider-neutral conversation

- [x] 1.1 Define `ToolDef`, `Turn`, `ToolResult`, `Provider`, and `Conversation` in `internal/agent`; have `impl` produce `ToolDef`s from `tools/list`, and keep `AnswerTools` unchanged.
- [x] 1.2 Move today's Anthropic code behind the interface (history via `resp.ToParam()`, effort, cache markers, refusal as infrastructure failure). Port the existing agent tests to drive `agent.Run` through the interface.
- [x] 1.3 Extend `agent.Settings` with `provider`, `base_url`, and `extra_body`, omitted at Anthropic defaults. Add a test that an Anthropic `KeyInputs` hashes to the same key as before the change.

## 2. OpenAI-compatible adapter

- [x] 2.1 Add `github.com/openai/openai-go`. Build a Chat Completions conversation with configurable base URL and key: tools as functions with verbatim `parameters` and strict mode off, an assistant message with `tool_calls`, then one `tool` message per result.
- [x] 2.2 Map usage (`prompt_tokens`, `cached_tokens`, `completion_tokens`) and `finish_reason` (`tool_calls`, `stop`, `length` as capped, `content_filter` as infrastructure failure). Answer invalid JSON arguments with an error tool result.
- [x] 2.3 Send `reasoning_effort` only when given. For the judge, send `temperature: 0` unless a judge effort is given. Merge `extra_body` into every request, and record a response's top-level `provider` per call.
- [x] 2.4 Tests against an `httptest` Chat Completions server: verbatim function schemas, a tool round trip, usage mapping, extra body present, upstream recorded, a 400 surfacing as an infrastructure failure. No real endpoint.

## 3. CLI and opt-in

- [x] 3.1 Add the flags `--provider`, `--base-url`, `--api-key-env`, and `--extra-body`, and their `--judge-*` counterparts. Validate the providers, the JSON in extra body, and the efforts each provider accepts.
- [x] 3.2 Make the key check per provider: each phase checks the variables of the roles it will call, before creating anything. Tests: a missing `OPENAI_API_KEY` for an `openai` judge, a named router variable, and an Anthropic-only run unchanged.
- [x] 3.3 Print each role's provider, base URL, and model in the plan.

## 4. Reports and documentation

- [x] 4.1 Record per role: provider, base URL, model, extra body, parameters as sent, and the set of upstreams. In the Markdown, mark token figures approximate across providers, and warn when a role was served by more than one upstream.
- [x] 4.2 README: the providers, examples (OpenAI judge, OpenRouter with a pinned upstream, a local vLLM or Ollama server), the reproducibility caveats of routers, and token comparability.
- [ ] 4.3 After `longmemeval-harness` is archived, check that this change's delta validates against `openspec/specs/memory-benchmark`.

## 5. First comparison

- [x] 5.1 Keep judgments per judge (`judgments/<judge-label>/<variant>`, with the report choosing one primary judge) so that a re-judge sits beside the earlier verdicts instead of overwriting them.
- [ ] 5.2 Re-judge an existing run's answers with `--judge-provider openai --judge-model gpt-4o`, and report agreement with the Claude judge per question type (this also bears on the harness's open question about judge bias).
