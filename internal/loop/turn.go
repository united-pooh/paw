package loop

import (
	"context"
	"fmt"
	"paw/internal/message"
	"paw/internal/model"
	"paw/internal/session"
	"reflect"
	"strings"
	"time"
)

func (runner *Engine) RunTurn(ctx context.Context, input string) (msg message.Message, err error) {
	execution, err := runner.runTurn(ctx, message.Message{Role: message.RoleUser, Content: input})
	return execution.Message, err
}

func (runner *Engine) RunRichTurn(ctx context.Context, input message.Message) (msg message.Message, err error) {
	if input.Role == "" {
		input.Role = message.RoleUser
	}
	if input.Role != message.RoleUser {
		return message.Message{}, fmt.Errorf("rich turn 必须使用 user role")
	}
	execution, err := runner.runTurn(ctx, input)
	return execution.Message, err
}

func (runner *Engine) runTurn(ctx context.Context, userInput message.Message) (TurnExecution, error) {
	return runner.runTurnWithTiming(ctx, userInput, nil)
}

func (runner *Engine) runTurnWithTiming(ctx context.Context, userInput message.Message, timing *TurnTiming) (TurnExecution, error) {
	if runner == nil {
		return TurnExecution{}, fmt.Errorf("runner 未初始化")
	}
	turnCtx, finishTurn := runner.beginActiveTurn(ctx)
	defer finishTurn()
	ctx = turnCtx
	config, broker := runner.gate.state()

	var execution TurnExecution
	var err error
	if !config.Enabled || broker == nil {
		execution, err = runner.runSingleTurnWithTiming(ctx, userInput, timing)
	} else {
		runner.gate.resetProgress()
		execution, err = runner.runTask(ctx, userInput, timing)
	}
	// 工具配对损坏类 400 不可重试，附加修复提示后交给 UI 展示
	// （对齐 CodeWhale：分类 → 不重试 → 展示给用户手动恢复）。
	return execution, decorateToolPairError(err)
}

func (runner *Engine) runSingleTurnWithTiming(ctx context.Context, userInput message.Message, timing *TurnTiming) (execution TurnExecution, err error) {
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
			if runner.contextModeState() {
				// 模式 B：状态块 + 最近 N 轮清洗对话，不送全量历史。
				stateMessages, stateRecovery, stateErr := runner.loadStateModeHistory(ctx)
				if stateErr == nil && stateMessages != nil {
					messages = stateMessages
					recovery = stateRecovery
				} else if stateErr != nil {
					err = stateErr
				}
				// stateMessages == nil（无 LoadRecentTurns 支持）时回退全量。
			}
			if messages == nil && err == nil {
				snapshot, snapshotErr := journal.LoadSnapshot(ctx, runner.sessionID)
				if snapshotErr == nil {
					messages = snapshot.ActiveHistory
					recovery = snapshot.Recovery
				} else {
					err = snapshotErr
				}
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
			// 模式 B 恢复的历史（状态块 + 最近 N 轮）小而稳定，无需上下文维护。
			if !runner.contextModeState() {
				maintenance, maintenanceErr := runner.maintainContextProjection(ctx, messages, true)
				if maintenanceErr != nil {
					runner.notifySystem("context-compaction", "cold-resume context cleanup skipped: "+maintenanceErr.Error())
				} else {
					messages = maintenance.history
					runner.notifyContextMaintenance(maintenance)
				}
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
	turnID, turnIDErr := runner.resolveTurnID(timing)
	if turnIDErr != nil {
		return execution, turnIDErr
	}

	// 每一轮都基于“已提交的历史副本”工作。Recovery 只存在于本轮
	// prompt，不会作为 synthetic user message 写入持久化 transcript。
	pendingRecovery := runner.takeRecovery()
	retryingEmptyTurn := false
	if pendingRecovery != nil && pendingRecovery.TurnID == turnID && len(pendingRecovery.CompletedToolResults) == 0 && len(pendingRecovery.DroppedToolCalls) == 0 {
		current := runner.currentHistory()
		if len(current) > 0 && reflect.DeepEqual(current[len(current)-1], userInput) {
			runner.setHistory(current[:len(current)-1])
			pendingRecovery = nil
			retryingEmptyTurn = true
		}
	}
	history, injectedSupplements := runner.buildTurnHistory(userInput)
	if pendingRecovery != nil {
		history = insertRecoveryMessage(history, pendingRecovery)
	}
	journal := runner.turnJournal()
	journalStarted := false
	settled := false
	ctx = WithTurnOwner(ctx, runner.sessionID, turnID)
	defer func() {
		if !settled {
			if cleaner := runner.taskEnv.cleaner(); cleaner != nil {
				failure := err
				if failure == nil {
					failure = fmt.Errorf("turn ended before completion")
				}
				cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
				cleaner.StopOwnedTasks(cleanupCtx, runner.sessionID, turnID, "interrupted: parent turn failed: "+failure.Error())
				cancel()
			}
		}
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
		if !retryingEmptyTurn {
			startMessages := stripRecoveryMessages(history)
			previousLen := len(runner.currentHistory())
			if previousLen < len(startMessages) {
				startMessages = startMessages[previousLen:]
			}
			if err := journal.BeginTurn(ctx, runner.sessionID, turnID, startMessages...); err != nil {
				return execution, fmt.Errorf("开始保存 turn 失败: %w", err)
			}
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

		var assistantMessage message.Message
		var modelErr error
		// 空响应（无工具调用且正文为空）是病态回合：用户将看不到任何输出，
		// 完成门还会把它计为无进展。静默重试有限次后再接受。
		for emptyAttempt := 0; ; emptyAttempt++ {
			for recoveryAttempt := 0; ; recoveryAttempt++ {
				assistantMessage, modelErr = runner.runModelTurn(ctx, history, turnState)
				if modelErr == nil || recoveryAttempt > 0 || assistantMessageHasPartialStream(assistantMessage) || !model.IsContextOverflowError(modelErr) {
					break
				}

				var compaction *ContextCompactionResult
				history, compaction, modelErr = runner.recoverContextLimit(ctx, history, modelErr, round > 0)
				if modelErr != nil {
					break
				}
				runner.notifySystem("context-recovery", fmt.Sprintf(
					"provider context limit reached; compacted %d messages (%d → %d) and retrying the current model round",
					compaction.FoldedMessages,
					compaction.BeforeMessages,
					compaction.AfterMessages,
				))
			}
			if modelErr != nil || !emptyFinalAssistantMessage(assistantMessage) {
				break
			}
			if emptyAttempt >= maxEmptyResponseRetries {
				runner.notifySystem("model", "模型连续返回空响应（已自动重试），本轮没有可见输出")
				break
			}
		}
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

// maxEmptyResponseRetries 是空响应回合的静默重试次数。
const maxEmptyResponseRetries = 1

// emptyFinalAssistantMessage 报告回合是否以“无工具调用且正文为空”结束。
func emptyFinalAssistantMessage(msg message.Message) bool {
	return len(toolCallsFromMessage(msg)) == 0 && strings.TrimSpace(msg.Content) == ""
}

func assistantMessageHasPartialStream(msg message.Message) bool {
	return msg.Role == message.RoleAssistant || msg.Content != "" || len(toolCallsFromMessage(msg)) > 0 || len(msg.ProviderData) > 0
}
