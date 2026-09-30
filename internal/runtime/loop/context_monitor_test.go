package loop

import (
	"encoding/json"
	"testing"

	"paw/internal/capability/model"
)

type contextMonitorTaskTokens struct {
	tokens int
}

func (p contextMonitorTaskTokens) TotalTaskTokens(string) int { return p.tokens }

func TestContextStatsSeparatesCurrentContextFromChildTaskUsage(t *testing.T) {
	runner := NewEngine(nil, nil, nil, nil, "")
	runner.usage.setCurrent(model.Usage{PromptTokens: 1000, CompletionTokens: 100, PromptCacheHitTokens: 600})
	runner.usage.setSessionKnown(model.Usage{PromptTokens: 5000, CompletionTokens: 500}, true)
	runner.SetTaskTokensProvider(contextMonitorTaskTokens{tokens: 9000})

	stats := runner.ContextStats(100000, "")
	if stats.UsedTokens != 1100 || stats.CacheTokens != 600 {
		t.Fatalf("current context = %#v, want 1100 used and 600 cached without child usage", stats)
	}
	if stats.SessionUsedTokens != 14500 {
		t.Fatalf("session usage = %d, want 5500 parent + 9000 child tokens", stats.SessionUsedTokens)
	}
	if stats.InputTokens != 1000 || stats.OutputTokens != 100 || !stats.Known || !stats.InputKnown || !stats.OutputKnown || !stats.CacheKnown {
		t.Fatalf("current request breakdown = %#v, want known input 1000, output 100, cache 600", stats)
	}
}

func TestContextStatsPreservesUsageKnowledgeForMonitor(t *testing.T) {
	tests := []struct {
		name        string
		usage       string
		known       bool
		input       int
		output      int
		inputKnown  bool
		outputKnown bool
		cacheKnown  bool
	}{
		{name: "reported zero", usage: `{"prompt_tokens":0,"completion_tokens":0,"prompt_tokens_details":{"cached_tokens":0}}`, known: true, inputKnown: true, outputKnown: true, cacheKnown: true},
		{name: "input only", usage: `{"prompt_tokens":100}`, known: true, input: 100, inputKnown: true},
		{name: "total only", usage: `{"total_tokens":120}`, known: true, input: 120},
		{name: "uncertain context", usage: `{"prompt_tokens":100,"completion_tokens":20,"prompt_tokens_details":{"cached_tokens":10}}`},
		{name: "cache inclusive input", usage: `{"input_tokens":500,"output_tokens":50,"cache_read_input_tokens":300}`, known: true, input: 800, output: 50, inputKnown: true, outputKnown: true, cacheKnown: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var usage model.Usage
			if err := json.Unmarshal([]byte(test.usage), &usage); err != nil {
				t.Fatal(err)
			}
			runner := NewEngine(nil, nil, nil, nil, "")
			runner.usage.setContextKnown(usage, test.known)
			stats := runner.ContextStats(100000, "")
			if stats.Known != test.known || stats.InputTokens != test.input || stats.OutputTokens != test.output || stats.InputKnown != test.inputKnown || stats.OutputKnown != test.outputKnown || stats.CacheKnown != test.cacheKnown {
				t.Fatalf("context usage = %#v, want known %t, input %d/%t, output %d/%t, cache known %t", stats, test.known, test.input, test.inputKnown, test.output, test.outputKnown, test.cacheKnown)
			}
		})
	}
}
