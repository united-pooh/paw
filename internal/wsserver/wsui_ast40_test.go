package wsserver

import (
	"testing"

	"codex-agent-go/internal/subagent"
)

func TestAgentStatusForTaskAST40(t *testing.T) {
	tests := []struct {
		input subagent.TaskStatus
		want  AgentStatus
		ok    bool
	}{
		{input: subagent.TaskCompleted, want: AgentStatusDone, ok: true},
		{input: subagent.TaskFailed, want: AgentStatusFailed, ok: true},
		{input: subagent.TaskStopped, want: AgentStatusStopped, ok: true},
		{input: subagent.TaskRunning, ok: false},
	}

	for _, tt := range tests {
		got, ok := agentStatusForTask(tt.input)
		if got != tt.want || ok != tt.ok {
			t.Fatalf("agentStatusForTask(%q) = %q/%v, want %q/%v", tt.input, got, ok, tt.want, tt.ok)
		}
	}
}
