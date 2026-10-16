package testutil

import "github.com/nodelogicau/particulars-bench/internal/agent"

// Start implements agent.Provider through the Anthropic adapter, so that
// scripted tests exercise the adapter's requests.
func (m *Model) Start(s agent.Settings, system []string, user string, tools []agent.ToolDef) (agent.Conversation, error) {
	return agent.AnthropicWith(m).Start(s, system, user, tools)
}
