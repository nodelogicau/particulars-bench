# particulars-bench

Benchmarks for implementations of the [Dialectical Knowledge Format (DKF)](https://github.com/nodelogicau/particulars).

DKF implementations do no reasoning; an agent does. So efficacy is measured as a
system: an agent, taught only by what an implementation's MCP server says, using only
the tools the DKF specification defines. Anything that starts a stdio MCP server bound
to a workspace directory can be benchmarked; the reference CLI is one such command:

```sh
--impl cli='particulars serve --mcp --workspace {workspace}'
```

Status: being specified. See `openspec/changes/`.

Scores here are comparable across runs of this harness, not to published LongMemEval
results (the judge differs).
