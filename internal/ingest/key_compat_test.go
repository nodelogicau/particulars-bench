package ingest_test

import (
	"encoding/json"
	"testing"

	"github.com/nodelogicau/particulars-bench/internal/agent"
	"github.com/nodelogicau/particulars-bench/internal/impl"
	"github.com/nodelogicau/particulars-bench/internal/ingest"
)

// legacyKey is the key of legacyInputs as computed before model providers
// existed. Anthropic settings must keep hashing to it, so that cached
// workspaces stay valid.
const legacyKey = "d74d3b2b848d5d737a16766394c3c758a960fdb4b36255c662d11e9b926c5731"

var legacyInputs = ingest.KeyInputs{
	DatasetVariant: "oracle",
	DatasetSHA256:  "821a2034",
	QuestionID:     "q1",
	Settings:       agent.Settings{Model: "claude-opus-5-5", Effort: "medium", MaxTokens: 16000, MaxTurns: 30},
	PromptSHA256:   "abc",
	Probe: ingest.Probe{
		Info:         impl.ServerInfo{Name: "particulars", Version: "0.14.0"},
		Instructions: "Use {workspace}.",
		Tools:        json.RawMessage(`[{"name":"claim_assert"}]`),
	},
}

func TestAnthropicKeyUnchanged(t *testing.T) {
	if got := legacyInputs.Key(); got != legacyKey {
		t.Errorf("key %s, want %s", got, legacyKey)
	}
}

func TestProviderChangesKey(t *testing.T) {
	a, b := legacyInputs, legacyInputs
	a.Settings.Provider, a.Settings.BaseURL = agent.ProviderOpenAI, "https://api.openai.com/v1"
	b.Settings.Provider, b.Settings.BaseURL = agent.ProviderOpenAI, "https://openrouter.ai/api/v1"
	c := b
	c.Settings.ExtraBody = map[string]any{"provider": map[string]any{"order": []any{"x"}}}
	keys := map[string]bool{legacyKey: true}
	for _, k := range []ingest.KeyInputs{a, b, c} {
		keys[k.Key()] = true
	}
	if len(keys) != 4 {
		t.Errorf("provider, base URL, and extra body must each change the key")
	}
}
