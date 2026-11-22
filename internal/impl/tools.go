package impl

import (
	"sort"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/nodelogicau/agentloop/agent"
	"github.com/nodelogicau/agentloop/mcpclient"

	"github.com/nodelogicau/particulars-bench/internal/spec"
)

// ToolDef returns an MCP tool's name, description, and input schema
// unchanged, for a provider to render.
func ToolDef(t *mcp.Tool) (agent.ToolDef, error) { return mcpclient.ToolDef(t) }

// SchemaMap returns a tool's input schema as decoded JSON.
func SchemaMap(t *mcp.Tool) (map[string]any, error) { return mcpclient.SchemaMap(t) }

// ToolDefs returns every tool's definition.
func ToolDefs(tools []*mcp.Tool) ([]agent.ToolDef, error) { return mcpclient.ToolDefs(tools) }

// ReadOnly reports whether a tool is annotated readOnlyHint: true.
func ReadOnly(t *mcp.Tool) bool { return mcpclient.ReadOnly(t) }

// AnswerTools selects the tools the answer phase offers: every specification
// query tool the server lists, and — unless specOnly — every extension
// annotated read-only. A specification tool that is not a query tool is
// never offered, whatever its annotations say. extensions names the offered
// tools outside the specification, sorted.
func AnswerTools(tools []*mcp.Tool, specOnly bool) (offered []*mcp.Tool, extensions []string) {
	for _, t := range tools {
		switch {
		case spec.IsQueryTool(t.Name):
			offered = append(offered, t)
		case spec.IsSpecTool(t.Name), specOnly, !ReadOnly(t):
		default:
			offered = append(offered, t)
			extensions = append(extensions, t.Name)
		}
	}
	sort.Strings(extensions)
	return offered, extensions
}

// Extensions names the tools outside the specification, sorted.
func Extensions(tools []*mcp.Tool) []string {
	var out []string
	for _, t := range tools {
		if !spec.IsSpecTool(t.Name) {
			out = append(out, t.Name)
		}
	}
	sort.Strings(out)
	return out
}
