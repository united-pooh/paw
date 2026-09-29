package loop

import (
	"encoding/json"
	"testing"

	"paw/internal/capability/model"
	"paw/internal/message"
)

func TestContextStatsCapacityKnowledge(t *testing.T) {
	tests := []struct {
		name       string
		usage      string
		known      bool
		totalKnown bool
	}{
		{name: "no report", usage: `{}`},
		{name: "empty report", usage: `{}`, known: true},
		{name: "input only", usage: `{"prompt_tokens":100}`, known: true},
		{name: "output only", usage: `{"completion_tokens":20}`, known: true},
		{name: "separate fields", usage: `{"prompt_tokens":100,"completion_tokens":0}`, known: true},
		{name: "explicit total", usage: `{"total_tokens":120}`, known: true, totalKnown: true},
		{name: "explicit zero total", usage: `{"total_tokens":0}`, known: true, totalKnown: true},
		{name: "untrusted total", usage: `{"total_tokens":120}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var usage model.Usage
			if err := json.Unmarshal([]byte(test.usage), &usage); err != nil {
				t.Fatal(err)
			}
			runner := NewEngine(nil, nil, nil, nil, "")
			runner.usage.setContextKnown(usage, test.known)
			stats := runner.ContextStats(1000, "")
			if stats.TotalKnown != test.totalKnown {
				t.Fatalf("TotalKnown = %t, want %t: %#v", stats.TotalKnown, test.totalKnown, stats)
			}
		})
	}
}

func TestContextStatsCapacityRetainsHistoryEstimate(t *testing.T) {
	runner := NewEngineWithInstructionRoot(nil, nil, nil, nil, "history-capacity", t.TempDir())
	runner.syncContextUsageFromHistory([]message.Message{{Role: message.RoleUser, Content: "preserve restored context capacity"}})
	stats := runner.ContextStats(1000, "")
	if !stats.Known || !stats.TotalKnown || stats.UsedTokens <= 0 {
		t.Fatalf("restored history estimate has no known capacity: %#v", stats)
	}
	if stats.OutputKnown {
		t.Fatalf("history estimate must not invent a reported output count: %#v", stats)
	}
	runner.syncContextUsageFromHistory(nil)
	stats = runner.ContextStats(1000, "")
	if stats.Known || stats.TotalKnown || stats.InputKnown || stats.OutputKnown {
		t.Fatalf("empty history must clear capacity knowledge: %#v", stats)
	}
}
