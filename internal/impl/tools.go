package impl

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/nodelogicau/particulars-bench/internal/spec"
)

// MessagesTool translates an MCP tool to a Messages API tool with its name,
// description, and input schema unchanged.
func MessagesTool(t *mcp.Tool) (anthropic.ToolUnionParam, error) {
	schema, err := SchemaMap(t)
	if err != nil {
		return anthropic.ToolUnionParam{}, err
	}
	in := anthropic.ToolInputSchemaParam{ExtraFields: map[string]any{}}
	for k, v := range schema {
		switch k {
		case "type":
			if v != "object" {
				return anthropic.ToolUnionParam{}, fmt.Errorf("tool %s: input schema type %v is not object", t.Name, v)
			}
		case "properties":
			in.Properties = v
		case "required":
			req, ok := v.([]any)
			if !ok {
				return anthropic.ToolUnionParam{}, fmt.Errorf("tool %s: required is not an array", t.Name)
			}
			for _, r := range req {
				s, ok := r.(string)
				if !ok {
					return anthropic.ToolUnionParam{}, fmt.Errorf("tool %s: required entry %v is not a string", t.Name, r)
				}
				in.Required = append(in.Required, s)
			}
		default:
			in.ExtraFields[k] = v
		}
	}
	tp := anthropic.ToolParam{Name: t.Name, InputSchema: in}
	if t.Description != "" {
		tp.Description = anthropic.String(t.Description)
	}
	return anthropic.ToolUnionParam{OfTool: &tp}, nil
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

// MessagesTools translates every tool.
func MessagesTools(tools []*mcp.Tool) ([]anthropic.ToolUnionParam, error) {
	out := make([]anthropic.ToolUnionParam, 0, len(tools))
	for _, t := range tools {
		mt, err := MessagesTool(t)
		if err != nil {
			return nil, err
		}
		out = append(out, mt)
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
