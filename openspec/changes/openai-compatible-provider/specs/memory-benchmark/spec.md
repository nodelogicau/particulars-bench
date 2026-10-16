## ADDED Requirements

### Requirement: Models are selected by provider
Each model role — the agent (ingest, answers, and baselines) and the judge — SHALL be selected by a provider, a model id, and, for the `openai` provider, a base URL. The provider SHALL be `anthropic` (the Anthropic Messages API, the default) or `openai` (any OpenAI-compatible Chat Completions endpoint, default base URL `https://api.openai.com/v1`). Roles MAY use different providers. Each role SHALL accept a JSON object of extra request-body fields, merged into every request of that role unchanged. When an `openai` response carries a top-level `provider` field, the harness SHALL record its value for that call.

#### Scenario: Claude agent, GPT-4o judge
- **WHEN** a run uses the `anthropic` provider for the agent and `--judge-provider openai --judge-model gpt-4o`
- **THEN** answers come from the Anthropic API and verdicts from `https://api.openai.com/v1`

#### Scenario: Router with a pinned upstream
- **WHEN** a role uses base URL `https://openrouter.ai/api/v1` and extra body `{"provider": {"order": ["x"], "allow_fallbacks": false}}`
- **THEN** every request of that role carries that field, and each call's reported upstream is recorded

#### Scenario: Tool-calling failures are findings
- **WHEN** an `openai` model returns a tool call whose arguments are not valid JSON
- **THEN** the call is answered with an error tool result, counted under its tool name, and the loop continues

## MODIFIED Requirements

### Requirement: Opt-in execution
The harness SHALL refuse to start any phase that calls a model when the API key variable of any provider that phase would call is unset, exiting non-zero and naming the variable before creating a workspace. The key variable SHALL be `ANTHROPIC_API_KEY` for `anthropic` and `OPENAI_API_KEY` for `openai`, unless the role names another variable. CI SHALL NOT call a model.

#### Scenario: No API key
- **WHEN** `ingest` is run with the `anthropic` provider and without `ANTHROPIC_API_KEY`
- **THEN** it exits non-zero naming the variable, and no workspace is created

#### Scenario: Judge key missing
- **WHEN** `judge` is run with `--judge-provider openai` and `OPENAI_API_KEY` unset
- **THEN** it exits non-zero naming `OPENAI_API_KEY`, and no model is called

#### Scenario: Router key
- **WHEN** a role names `OPENROUTER_API_KEY` as its key variable and it is set
- **THEN** that variable's value is sent to the role's base URL and `OPENAI_API_KEY` is not required

### Requirement: The agent sees what an MCP client sees
In every phase that uses an implementation, on every provider, the model SHALL receive the offered tools with names, descriptions, and input schemas exactly as returned by `tools/list` — as Anthropic tools or as OpenAI functions with the schema as `parameters` and strict mode off — and the `initialize` instructions as its system prompt followed by the phase prompt. Tool calls SHALL be forwarded to the server and its results returned unmodified. The conversation history SHALL be replayed in the provider's own form, without conversion between providers.

#### Scenario: Tools as served
- **WHEN** the harness builds the tool list for the model
- **THEN** every offered tool's name, description, and input schema equals the server's

#### Scenario: Tools as served to an OpenAI-compatible model
- **WHEN** the agent role uses the `openai` provider
- **THEN** each offered tool is a function whose `name`, `description`, and `parameters` equal the server's name, description, and input schema

### Requirement: Ingested workspaces are cached by their inputs
An ingested workspace SHALL be stored outside the repository under a key derived from the dataset variant and checksum, the question id, the ingest model settings (provider, base URL, extra request fields, model id, and provider parameters), the ingest prompt, and the ingest implementation's `serverInfo`, `initialize` instructions, and `tools/list` result on an empty workspace. Settings at the `anthropic` provider's defaults SHALL NOT change the key's encoding, so that keys computed before providers existed remain valid. After a successful ingest the harness SHALL write, last, a manifest of every file's path and SHA-256. A cached workspace SHALL be reused without a model call only when its manifest exists and every file matches it. The answer implementation SHALL NOT be part of the key.

#### Scenario: Reuse across implementations
- **WHEN** a question has been ingested and the answer phase is run with two different implementations
- **THEN** both answer from the same cached workspace and ingest makes no model calls

#### Scenario: Changed guidance invalidates
- **WHEN** the ingest implementation's `initialize` instructions or any tool description change
- **THEN** the cache key changes and the question is ingested again

#### Scenario: Interrupted ingest
- **WHEN** an ingest was interrupted before its manifest was written
- **THEN** the workspace is not reused and the question is ingested again

#### Scenario: Provider changes the key
- **WHEN** the same question is ingested with the same model id through two different base URLs
- **THEN** the two ingests have different keys

#### Scenario: Anthropic keys unchanged
- **WHEN** a question cached before providers existed is ingested again with the `anthropic` provider and the same settings
- **THEN** the key matches and no model is called

### Requirement: Report contract
Each run SHALL produce a `bench-report/1` JSON file and a Markdown rendering. The JSON SHALL record: run date and harness commit; the DKF version written; per variant the label, command, `serverInfo`, model ids, prompt hashes, offered extension tools, and `--spec-tools-only`; for each model role its provider, base URL, model id, extra request fields, provider parameters as sent, and the set of upstream providers reported by its calls; dataset variant, checksum, seed, and question ids; per question the type, answer, verdict, input and output tokens per phase, tool calls by name, ingest metrics, and any infrastructure failure; and per type and overall, accuracy with sample size and a Wilson 95% interval. The Markdown SHALL show variants as columns and question types as rows, each cell with accuracy, n, and interval, followed by tokens and tool calls per answer, and SHALL label token figures approximate when variants used different providers.

#### Scenario: Interval on every cell
- **WHEN** a report is rendered
- **THEN** every accuracy cell shows n and its 95% interval

#### Scenario: Variants side by side
- **WHEN** a report covers no memory, BM25, and two implementations
- **THEN** the Markdown table has one column per variant over identical question ids

#### Scenario: More than one upstream
- **WHEN** a role's calls were served by more than one reported upstream provider
- **THEN** the report lists every upstream for that role and the Markdown warns that the role's model was not served consistently
