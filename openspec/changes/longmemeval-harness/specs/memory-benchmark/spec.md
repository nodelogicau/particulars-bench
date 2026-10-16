## ADDED Requirements

### Requirement: An implementation is a command template
The harness SHALL accept implementations as `--impl <label>=<command>`, where `<command>` contains `{workspace}`. To start an implementation it SHALL substitute the absolute workspace path, set `DKF_WORKSPACE` to it and the working directory to it, run the command, and speak MCP over its stdio. It SHALL identify the implementation by `serverInfo.name` and `serverInfo.version` from `initialize`. It SHALL NOT invoke any other command of an implementation.

#### Scenario: Reference CLI
- **WHEN** the harness is run with `--impl cli='particulars serve --mcp --workspace {workspace}'`
- **THEN** it starts that command per workspace and records the server's name and version

#### Scenario: Server fails to start
- **WHEN** the command exits before `initialize` completes
- **THEN** that question is recorded as an infrastructure failure with the command's stderr, excluded from accuracy, and counted in the report

### Requirement: Workspaces are created by the harness
For each question the harness SHALL create an empty directory and write a `dkf.yaml` conforming to the DKF `workspace-config` specification, with default scope `personal`. It SHALL record the DKF version it wrote in every report.

#### Scenario: No implementation init
- **WHEN** a workspace is created for ingest
- **THEN** no implementation command has been run on it before the MCP server starts

### Requirement: Opt-in execution
The harness SHALL refuse to start any phase that calls a model when `ANTHROPIC_API_KEY` is unset, exiting non-zero before creating a workspace. CI SHALL NOT call a model.

#### Scenario: No API key
- **WHEN** `ingest` is run without `ANTHROPIC_API_KEY`
- **THEN** it exits non-zero naming the variable, and no workspace is created

### Requirement: Dataset fetched, verified, never committed
The harness SHALL download LongMemEval on first use to the user cache directory and verify it against a pinned checksum, refusing to proceed on mismatch. Dataset files SHALL NOT be written inside the repository.

#### Scenario: Checksum mismatch
- **WHEN** the cached dataset file does not match the pinned checksum
- **THEN** the harness exits non-zero naming the file and the expected checksum

### Requirement: Plan before spending
Before the first model call of any phase the harness SHALL print the dataset variant, questions by type, sessions to replay, and estimated model calls. `--dry-run` SHALL print the plan and exit 0 without calling a model.

#### Scenario: Dry run
- **WHEN** `ingest --dry-run` is run with the default sample
- **THEN** the plan is printed, no model is called, and no workspace is created

### Requirement: Stratified, seeded sampling
By default the harness SHALL use the oracle variant and a stratified sample of 10 questions per question type, with abstention questions (`_abs` suffix) as their own stratum, chosen by a fixed seed. `--per-type`, `--seed`, `--types`, `--ids`, and `--limit` SHALL narrow or change the sample. The selected question ids SHALL be recorded in every report.

#### Scenario: Same seed, same sample
- **WHEN** two runs use the same dataset checksum, seed, and per-type count
- **THEN** they select the same question ids

### Requirement: The agent sees what an MCP client sees
In every phase that uses an implementation, the model SHALL receive the offered tools with names, descriptions, and input schemas exactly as returned by `tools/list`, and the `initialize` instructions as its system prompt followed by the phase prompt. Tool calls SHALL be forwarded to the server and its results returned unmodified.

#### Scenario: Tools as served
- **WHEN** the harness builds the tool list for the model
- **THEN** every offered tool's name, description, and input schema equals the server's

### Requirement: Ingest replays sessions chronologically
Ingest SHALL replay a question's history sessions oldest first, one agent loop per session, offering every tool the server lists, and telling the agent the session's date and to backdate assertions to it. All writes SHALL go through the MCP server.

#### Scenario: Chronological replay
- **WHEN** a question's history has sessions dated 2023-05-01 and 2023-04-10
- **THEN** the 2023-04-10 session is replayed first

### Requirement: Ingested workspaces are cached by their inputs
An ingested workspace SHALL be stored outside the repository under a key derived from the dataset variant and checksum, the question id, the ingest model id, the ingest prompt, and the ingest implementation's `serverInfo`, `initialize` instructions, and `tools/list` result on an empty workspace. After a successful ingest the harness SHALL write, last, a manifest of every file's path and SHA-256. A cached workspace SHALL be reused without a model call only when its manifest exists and every file matches it. The answer implementation SHALL NOT be part of the key.

#### Scenario: Reuse across implementations
- **WHEN** a question has been ingested and the answer phase is run with two different implementations
- **THEN** both answer from the same cached workspace and ingest makes no model calls

#### Scenario: Changed guidance invalidates
- **WHEN** the ingest implementation's `initialize` instructions or any tool description change
- **THEN** the cache key changes and the question is ingested again

#### Scenario: Interrupted ingest
- **WHEN** an ingest was interrupted before its manifest was written
- **THEN** the workspace is not reused and the question is ingested again

### Requirement: Answer phase is read-only
The answer phase SHALL serve a copy of the cached workspace with the given implementation. It SHALL offer the DKF specification's query tools — `particular_resolve`, `knowledge_recall`, `conflict_detect`, `lineage_trace` — when listed, and SHALL offer other tools only when annotated `readOnlyHint: true`. `--spec-tools-only` SHALL withhold every tool outside the specification's names. The report SHALL list, per variant, the offered extension tools and their call counts.

#### Scenario: Writes unavailable
- **WHEN** the answer phase builds the tool list
- **THEN** `claim_assert`, `synthesis_create`, and every tool neither a spec query tool nor annotated read-only are absent

#### Scenario: Extensions withheld
- **WHEN** the answer phase is run with `--spec-tools-only` against a server that lists `topics_list`
- **THEN** `topics_list` is not offered

#### Scenario: Cached workspace untouched
- **WHEN** an answer run completes
- **THEN** the cached workspace's files still match its manifest

### Requirement: Ingest metrics from the files
For each ingested workspace the harness SHALL compute, by reading object files per the DKF `workspace-layout` specification: claims, particulars, distinct topics, syntheses, and the fraction of claims whose timestamp falls on the date of the session they were asserted in.

#### Scenario: Metrics without the implementation
- **WHEN** ingest metrics are computed
- **THEN** no implementation command is running

### Requirement: Baselines
The harness SHALL provide three baselines answered by the same model as the implementations: no memory (question and question date only), full context (all history sessions in the prompt), and BM25 (top-k sessions by BM25 over raw session text, k recorded). Baselines SHALL NOT start an implementation.

#### Scenario: BM25 is model-free
- **WHEN** the BM25 baseline retrieves sessions
- **THEN** no model or embedding service is called for retrieval

### Requirement: Judging
Each answer SHALL be graded by a recorded judge model using LongMemEval's grading prompt for the question's type, with abstention questions graded on whether the answer correctly declines. The verdict and raw judge output SHALL be kept per question.

#### Scenario: Abstention graded as abstention
- **WHEN** a question id ends in `_abs` and the answer says the information was never given
- **THEN** the abstention prompt is used and the verdict is correct

### Requirement: Report contract
Each run SHALL produce a `bench-report/1` JSON file and a Markdown rendering. The JSON SHALL record: run date and harness commit; the DKF version written; per variant the label, command, `serverInfo`, model ids, prompt hashes, offered extension tools, and `--spec-tools-only`; dataset variant, checksum, seed, and question ids; per question the type, answer, verdict, input and output tokens per phase, tool calls by name, ingest metrics, and any infrastructure failure; and per type and overall, accuracy with sample size and a Wilson 95% interval. The Markdown SHALL show variants as columns and question types as rows, each cell with accuracy, n, and interval, followed by tokens and tool calls per answer.

#### Scenario: Interval on every cell
- **WHEN** a report is rendered
- **THEN** every accuracy cell shows n and its 95% interval

#### Scenario: Variants side by side
- **WHEN** a report covers no memory, BM25, and two implementations
- **THEN** the Markdown table has one column per variant over identical question ids
