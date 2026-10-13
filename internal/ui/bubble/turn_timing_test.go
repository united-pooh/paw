package bubble

import (
	"context"
	"fmt"
	"testing"
	"time"

	"paw/internal/message"
	"paw/internal/runtime/loop"
	"paw/internal/storage/session"
)

type timedBubbleRunner struct {
	fakeRunner
	execution loop.TurnExecution
	turnID    string
	startedAt time.Time
}

func (r *timedBubbleRunner) RunTurnWithTiming(_ context.Context, _ string, turnID string, startedAt time.Time) (loop.TurnExecution, error) {
	r.turnID = turnID
	r.startedAt = startedAt
	return r.execution, nil
}

func (r *timedBubbleRunner) RunRichTurnWithTiming(_ context.Context, _ message.Message, turnID string, startedAt time.Time) (loop.TurnExecution, error) {
	r.turnID = turnID
	r.startedAt = startedAt
	return r.execution, nil
}

// TestRunTurnCmdCarriesTimingMetadata 验证回合计时 metadata 仍随
// turnFinishedMsg 传输（loop 侧 sidecar 持久化依赖它）；transcript 内的
// footer 展示已退役，由 ⚒ 状态行的「刚完成」窗口接管。
func TestRunTurnCmdCarriesTimingMetadata(t *testing.T) {
	started := time.Unix(100, 0).UTC()
	response := started.Add(time.Second)
	runner := &timedBubbleRunner{execution: loop.TurnExecution{
		Metadata:           session.TurnMetadata{TurnID: "turn-1", StartedAt: started, ResponseAt: &response, DurationMS: 1000, Status: session.TurnStatusCompleted},
		MetadataPersistErr: fmt.Errorf("sidecar unavailable"),
	}}
	msg := runTurnCmd(context.Background(), runner, inputDraft{Text: "hello"}, "turn-1", started)()
	finished, ok := msg.(turnFinishedMsg)
	if !ok || finished.err != nil || finished.metadata == nil || finished.metadataErr == nil {
		t.Fatalf("turnFinishedMsg = %#v", msg)
	}
	if runner.turnID != "turn-1" || !runner.startedAt.Equal(started) {
		t.Fatalf("timing args = %q, %v", runner.turnID, runner.startedAt)
	}
}
