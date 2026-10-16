// Package spec pins everything the harness takes from the DKF specification.
// A change to the specification's tool names or workspace format is a change
// to this file, and every report records what was pinned here.
package spec

// DKFFormat is the `format` the harness writes into each workspace's dkf.yaml
// (workspace-config). dkf/0.1 is the flat layout; dkf/0.2 (sharded) is
// specified but the reference implementation does not read it yet.
const DKFFormat = "dkf/0.1"

// QueryTools are the read-only tools of the specification's "MCP Server
// Tools" table. The answer phase offers each of them when a server lists it.
var QueryTools = []string{
	"particular_resolve",
	"knowledge_recall",
	"conflict_detect",
	"lineage_trace",
}

// Tools is every tool name the specification defines. A listed tool outside
// this set is an implementation's extension.
var Tools = []string{
	"particular_define",
	"particular_resolve",
	"claim_assert",
	"claim_retract",
	"synthesis_create",
	"knowledge_recall",
	"conflict_detect",
	"lineage_trace",
	"feed_index",
	"particular_merge",
	"knowledge_publish",
}

// IsQueryTool reports whether name is one of the specification's query tools.
func IsQueryTool(name string) bool { return contains(QueryTools, name) }

// IsSpecTool reports whether name is defined by the specification.
func IsSpecTool(name string) bool { return contains(Tools, name) }

func contains(names []string, name string) bool {
	for _, n := range names {
		if n == name {
			return true
		}
	}
	return false
}
