package loop

import (
	"context"
	"fmt"
	"paw/internal/message"
	"paw/internal/session"
)

func (runner *Runner) RunTurn(ctx context.Context, input string) (msg message.Message, err error) {
	execution, err := runner.runTurn(ctx, message.Message{Role: message.RoleUser, Content: input})
	return execution.Message, err
}

func (runner *Runner) RunRichTurn(ctx context.Context, input message.Message) (msg message.Message, err error) {
	if input.Role == "" {
		input.Role = message.RoleUser
	}
	if input.Role != message.RoleUser {
		return message.Message{}, fmt.Errorf("rich turn 必须使用 user role")
	}
	execution, err := runner.runTurn(ctx, input)
	return execution.Message, err
}

func (runner *Runner) runTurn(ctx context.Context, userInput message.Message) (TurnExecution, error) {
	return runner.runTurnWithTiming(ctx, userInput, nil)
}

func (runner *Runner) runTurnWithTiming(ctx context.Context, userInput message.Message, timing *TurnTiming) (TurnExecution, error) {
	if runner == nil {
		return TurnExecution{}, fmt.Errorf("runner 未初始化")
	}
	config, broker := runner.autoContinueState()
	if !config.Enabled || broker == nil {
		return runner.runSingleTurnWithTiming(ctx, userInput, timing)
	}

	runner.mu.Lock()
	runner.lastProgressHash = ""
	runner.mu.Unlock()
	return runner.runTask(ctx, userInput, timing)
}

func (runner *Runner) runSingleTurnWithTiming(ctx context.Context, userInput message.Message, timing *TurnTiming) (execution TurnExecution, err error) {
	if err := runner.validate(); err != nil {
		return execution, err
	}
	if err := runner.persistInputAttachments(ctx, &userInput); err != nil {
		return execution, err
	}
	runner.activateSkillContext(userInput.Content)
	defer runner.clearActiveSkillContext()
	turnState := &TurnState{SkillContext: runner.currentSkillContext()}

	if runner.historyIsNil() && runner.store != nil {
		var messages []message.Message
		var recovery *session.RecoveryState
		if journal := runner.turnJournal(); journal != nil {
			snapshot, snapshotErr := journal.LoadSnapshot(ctx, runner.sessionID)
			if snapshotErr == nil {
				messages = snapshot.ActiveHistory
				recovery = snapshot.Recovery
			} else {
				err = snapshotErr
			}
		} else {
			messages, err = runner.store.LoadResolvedHistory(ctx, runner.sessionID)
		}
		if err != nil {
			if existsStore, ok := runner.store.(historyExistenceStore); ok {
				exists, existsErr := existsStore.Exists(ctx, runner.sessionID)
				if existsErr != nil {
					return execution, existsErr
				}
				if !exists {
					runner.setHistoryIfNil(nil)
				} else {
					return execution, err
				}
			} else {
				return execution, err
			}
		} else {
			maintenance, maintenanceErr := runner.maintainContextProjection(ctx, messages, true)
			if maintenanceErr != nil {
				runner.notifySystem("context-compaction", "cold-resume context cleanup skipped: "+maintenanceErr.Error())
			} else {
				messages = maintenance.history
				runner.notifyContextMaintenance(maintenance)
			}
			runner.setHistoryIfNil(messages)
			runner.syncContextUsageFromHistory(messages)
			runner.setRecoveryIfNil(recovery)
		}
	}

	if invocation, ok := parseStreamMAInvocation(userInput.Content); ok {
		if !runner.currentStreamMAEnabled() {
			return execution, fmt.Errorf("streamma is disabled; start with -streamma=true or set PAW_STREAMMA=1 to enable /streamma")
		}
		assistant, err := runner.runStreamMATurn(ctx, userInput.Content, invocation)
		if err != nil {
			return execution, err
		}
		return runner.completeTurnExecution(ctx, timing, assistant, -1), nil
	}

	trace := runner.beginTraceTurn(userInput.Content, "conversation")
	defer func() {
		runner.finishTraceTurn(trace, err)
	}()

	// 每一轮都基于“已提交的历史副本”工作。Recovery 只存在于本轮
	// prompt，不会作为 synthetic user message 写入持久化 transcript。
	history, injectedSupplements := runner.buildTurnHistory(userInput)
	pendingRecovery := runner.takeRecovery()
	if pendingRecovery != nil {
		history = insertRecoveryMessage(history, pendingRecovery)
	}
	journal := runner.turnJournal()
	turnID, turnIDErr := runner.resolveTurnID(timing)
	if turnIDErr != nil {
		runner.setRecovery(pendingRecovery)
		return execution, turnIDErr
	}
	journalStarted := false
	settled := false
	defer func() {
		if !settled && len(injectedSupplements) > 0 {
			runner.prependSupplements(injectedSupplements)
		}
		if !journalStarted && pendingRecovery != nil {
			runner.setRecovery(pendingRecovery)
		}
		if journal == nil || !journalStarted || settled {
			return
		}
		failure := err
		if failure == nil {
			failure = fmt.Errorf("turn ended before completion")
		}
		failureCtx := context.WithoutCancel(ctx)
		if failErr := journal.FailTurn(failureCtx, runner.sessionID, turnID, failure); failErr != nil && err == nil {
			err = fmt.Errorf("保存 turn failure 失败: %w", failErr)
		}
		if snapshot, snapshotErr := journal.LoadSnapshot(failureCtx, runner.sessionID); snapshotErr == nil {
			runner.setHistory(snapshot.ActiveHistory)
			runner.setRecovery(snapshot.Recovery)
		} else {
			runner.setHistory(stripRecoveryMessages(history))
			runner.setRecovery(&session.RecoveryState{TurnID: turnID, Error: failure.Error()})
		}
	}()
	if journal != nil {
		startMessages := stripRecoveryMessages(history)
		previousLen := len(runner.currentHistory())
		if previousLen < len(startMessages) {
			startMessages = startMessages[previousLen:]
		}
		if err := journal.BeginTurn(ctx, runner.sessionID, turnID, startMessages...); err != nil {
			return execution, fmt.Errorf("开始保存 turn 失败: %w", err)
		}
		journalStarted = true
	}
	for round := 0; round < maxToolRounds; round++ {
		var injected []string
		history, injected = runner.appendPendingSupplements(history)
		injectedSupplements = append(injectedSupplements, injected...)
		if journal != nil && len(injected) > 0 {
			if appendErr := runner.store.Append(ctx, runner.sessionID, buildSupplementMessages(injected)...); appendErr != nil {
				return execution, fmt.Errorf("保存 supplement 失败: %w", appendErr)
			}
		}

		if round == 0 {
			maintenance, maintenanceErr := runner.maintainContextProjection(ctx, history, true)
			if maintenanceErr != nil {
				runner.notifySystem("context-compaction", "context cleanup skipped: "+maintenanceErr.Error())
			} else {
				history = maintenance.history
				if maintenance.estimatedTokensSaved > 0 {
					runner.syncContextUsageFromHistory(history)
				}
				runner.notifyContextMaintenance(maintenance)
			}
		}

		assistantMessage, modelErr := runner.runModelTurn(ctx, history, turnState)
		if modelErr != nil {
			if persistErr := runner.persistPartialAssistant(context.WithoutCancel(ctx), journal, turnID, assistantMessage); persistErr != nil {
				return execution, persistErr
			}
			return execution, modelErr
		}
		assistantSeq := int64(-1)
		if journal != nil {
			if sequencedJournal, ok := journal.(sequencedTurnJournal); ok {
				assistantSeq, err = sequencedJournal.AppendAssistantWithSequence(ctx, runner.sessionID, turnID, assistantMessage)
			} else {
				err = journal.AppendAssistant(ctx, runner.sessionID, turnID, assistantMessage)
			}
			if err != nil {
				return execution, fmt.Errorf("保存 assistant turn 失败: %w", err)
			}
		}
		history = append(history, assistantMessage)

		toolCalls := toolCallsFromMessage(assistantMessage)
		if len(toolCalls) == 0 {
			// 只有当本轮完整结束时，才把本轮消息提交为新的会话历史。
			if journal != nil {
				if err := journal.CompleteTurn(ctx, runner.sessionID, turnID); err != nil {
					return execution, fmt.Errorf("完成保存 turn 失败: %w", err)
				}
			} else {
				var commitErr error
				assistantSeq, commitErr = runner.commitHistory(ctx, stripRecoveryMessages(history))
				if commitErr != nil {
					return execution, commitErr
				}
			}
			runner.setHistory(stripRecoveryMessages(history))
			settled = true
			return runner.completeTurnExecution(ctx, timing, assistantMessage, assistantSeq), nil
		}

		var checkpoint toolResultCheckpoint
		if journal != nil {
			checkpoint = func(callIndex int, result message.ToolResult) error {
				return journal.AppendToolResult(ctx, runner.sessionID, turnID, callIndex, result)
			}
		}
		toolResult, err := runner.runToolCallsWithCheckpoint(ctx, toolCalls, checkpoint)
		if err != nil {
			return execution, err
		}
		history = append(history, toolResult)
		turnState.ToolRounds++
	}

	return execution, fmt.Errorf("tool loop exceeded max rounds: %d", maxToolRounds)
}
