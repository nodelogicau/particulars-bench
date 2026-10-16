package impl

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/nodelogicau/particulars-bench/internal/agent"
	"github.com/nodelogicau/particulars-bench/internal/spec"
)

// ToolDef returns an MCP tool's name, description, and input schema
// unchanged, for a provider to render.
func ToolDef(t *mcp.Tool) (agent.ToolDef, error) {
	schema, err := SchemaMap(t)
	if err != nil {
		return agent.ToolDef{}, err
	}
	return agent.ToolDef{Name: t.Name, Description: t.Description, Schema: schema}, nil
}

// SchemaMap returns a tool's input schema as decoded JSON.
func SchemaMap(t *mcp.Tool) (map[string]any, error) {
	b, err := json.Marshal(t.InputSchema)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("tool %s: input schema: %w", t.Name, err)
	}
	if m == nil {
		m = map[string]any{"type": "object"}
	}
	return m, nil
}

// ToolDefs returns every tool's definition.
func ToolDefs(tools []*mcp.Tool) ([]agent.ToolDef, error) {
	out := make([]agent.ToolDef, 0, len(tools))
	for _, t := range tools {
		d, err := ToolDef(t)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, nil
}

// ReadOnly reports whether a tool is annotated readOnlyHint: true.
func ReadOnly(t *mcp.Tool) bool { return t.Annotations != nil && t.Annotations.ReadOnlyHint }

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
