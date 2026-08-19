package loop

import (
	"context"
	"fmt"

	"paw/internal/message"
)

type RecoveredTurn struct {
	TurnID          string
	History         []message.Message
	Assistant       message.Message
	CompletedResult []message.ToolResult
}

// ContinueRecoveredTurn resumes after a persisted assistant tool-call message.
// Completed results are folded in and never executed again.
func (runner *Engine) ContinueRecoveredTurn(ctx context.Context, recovered RecoveredTurn, timing *TurnTiming) (execution TurnExecution, err error) {
	if runner == nil {
		return execution, fmt.Errorf("runner 未初始化")
	}
	if timing == nil {
		timing = &TurnTiming{TurnID: recovered.TurnID}
	}
	turnCtx, finishTurn := runner.beginActiveTurn(ctx)
	defer finishTurn()
	ctx = WithTurnOwner(turnCtx, runner.sessionID, recovered.TurnID)
	journal := runner.turnJournal()
	if journal == nil {
		return execution, fmt.Errorf("recovered turn requires a turn journal")
	}
	settled := false
	defer func() {
		if settled {
			return
		}
		failure := err
		if failure == nil {
			failure = fmt.Errorf("recovered turn ended before completion")
		}
		_ = journal.FailTurn(context.WithoutCancel(ctx), runner.sessionID, recovered.TurnID, failure)
	}()

	history := message.CloneMessages(recovered.History)
	assistant := message.CloneMessage(recovered.Assistant)
	calls := toolCallsFromMessage(assistant)
	if len(calls) == 0 {
		return execution, fmt.Errorf("recovered turn has no tool calls")
	}
	resultsByID := make(map[string]message.ToolResult, len(recovered.CompletedResult))
	for _, result := range recovered.CompletedResult {
		resultsByID[result.ToolUseID] = result
	}
	missing := make([]message.ToolCall, 0, len(calls))
	missingIndexes := make([]int, 0, len(calls))
	for index, call := range calls {
		if _, ok := resultsByID[call.ID]; ok {
			continue
		}
		missing = append(missing, call)
		missingIndexes = append(missingIndexes, index)
	}
	if len(missing) > 0 {
		checkpoint := func(localIndex int, result message.ToolResult) error {
			return journal.AppendToolResult(ctx, runner.sessionID, recovered.TurnID, missingIndexes[localIndex], result)
		}
		toolMessage, runErr := runner.runToolCallsWithCheckpoint(ctx, missing, checkpoint)
		if runErr != nil {
			return execution, runErr
		}
		for _, result := range toolResultsFromMessage(toolMessage) {
			resultsByID[result.ToolUseID] = result
		}
	}
	ordered := make([]message.ToolResult, 0, len(calls))
	for _, call := range calls {
		result, ok := resultsByID[call.ID]
		if !ok {
			return execution, fmt.Errorf("recovered tool result missing for %s", call.ID)
		}
		ordered = append(ordered, result)
	}
	if len(recovered.CompletedResult) < len(calls) {
		history = append(history, assistant, buildToolResultsMessage(ordered))
	}

	turnState := &TurnState{ToolRounds: 1}
	for round := 1; round < maxToolRounds; round++ {
		assistant, modelErr := runner.runModelTurn(ctx, history, turnState)
		if modelErr != nil {
			if persistErr := runner.persistPartialAssistant(context.WithoutCancel(ctx), journal, recovered.TurnID, assistant); persistErr != nil {
				return execution, persistErr
			}
			return execution, modelErr
		}
		assistantSeq := int64(-1)
		if sequenced, ok := journal.(sequencedTurnJournal); ok {
			assistantSeq, err = sequenced.AppendAssistantWithSequence(ctx, runner.sessionID, recovered.TurnID, assistant)
		} else {
			err = journal.AppendAssistant(ctx, runner.sessionID, recovered.TurnID, assistant)
		}
		if err != nil {
			return execution, err
		}
		history = append(history, assistant)
		toolCalls := toolCallsFromMessage(assistant)
		if len(toolCalls) == 0 {
			if err := journal.CompleteTurn(ctx, runner.sessionID, recovered.TurnID); err != nil {
				return execution, err
			}
			runner.setHistory(history)
			settled = true
			return runner.completeTurnExecution(ctx, timing, assistant, assistantSeq), nil
		}
		checkpoint := func(callIndex int, result message.ToolResult) error {
			return journal.AppendToolResult(ctx, runner.sessionID, recovered.TurnID, callIndex, result)
		}
		toolMessage, toolErr := runner.runToolCallsWithCheckpoint(ctx, toolCalls, checkpoint)
		if toolErr != nil {
			return execution, toolErr
		}
		history = append(history, toolMessage)
		turnState.ToolRounds++
	}
	return execution, fmt.Errorf("tool loop exceeded max rounds: %d", maxToolRounds)
}
