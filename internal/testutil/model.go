// Package testutil holds test doubles shared across packages.
package testutil

import "github.com/nodelogicau/agentloop/agenttest"

// Reply is a scripted assistant turn: text, then tool calls.
type Reply = agenttest.Reply

// ToolUse is one scripted tool call.
type ToolUse = agenttest.ToolUse

// Model is an agent.Provider, through the Anthropic adapter, whose replies
// come from Script. It records every request.
type Model = agenttest.Model

// Message builds the n-th API response from a Reply.
var Message = agenttest.Message

// ToolNames lists the names of the tools offered in a request.
var ToolNames = agenttest.ToolNames

// Has reports whether names contains name.
func Has(names []string, name string) bool {
	for _, n := range names {
		if n == name {
			return true
		}
	}
	return false
}
