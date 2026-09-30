package loop

import (
	"context"
	"time"

	"paw/internal/message"
)

const maxStreamRecoveries = 10

const streamRecoveryInstruction = "[Paw stream recovery]\n上一次模型响应因连接中断而未完成。沿用原任务、已有计划、调查证据与用户约束，从历史中最后已完成的步骤继续执行，不重写调查清单。历史中的工具结果已经完成，不要重复执行；未完成响应的正文、思考和工具参数不作为已完成结果。若正在撰写答案，请在新消息中完整回答，不尝试拼接或重放中断的输出。"

func streamRecoveryHistory(history []message.Message) []message.Message {
	messages := make([]message.Message, 0, len(history)+1)
	messages = append(messages, history...)
	return append(messages, message.Message{Role: message.RoleUser, Content: streamRecoveryInstruction})
}

func streamRecoveryDelay(attempt int) time.Duration {
	if attempt >= 4 {
		return 5 * time.Second
	}
	return time.Second << max(attempt-1, 0)
}

func (runner *Engine) pauseStreamRecovery(ctx context.Context, attempt int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	delay := streamRecoveryDelay(attempt)
	if runner.waitForStreamRecovery != nil {
		return runner.waitForStreamRecovery(ctx, delay)
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return ctx.Err()
	}
}
