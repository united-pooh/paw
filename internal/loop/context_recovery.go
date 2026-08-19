package loop

import (
	"context"
	"fmt"

	"paw/internal/message"
)

const contextRecoveryFocus = "recovering from provider context limit"

func (runner *Engine) recoverContextLimit(ctx context.Context, history []message.Message, providerErr error, preserveRecentToolPair bool) ([]message.Message, *ContextCompactionResult, error) {
	protectedTail := -1
	if preserveRecentToolPair {
		head := pinnedHistoryPrefix(history, runner.contextLimit())
		protectedTail = latestToolCallGroupStart(history, head)
	}
	compacted, result, err := runner.compactHistoryWithProtectedTail(ctx, history, contextRecoveryFocus, true, protectedTail)
	if err != nil {
		return history, nil, fmt.Errorf("%w; context recovery failed: %v", providerErr, err)
	}
	if result == nil || result.FoldedMessages == 0 {
		return history, nil, fmt.Errorf("%w; context recovery unavailable: no compactable history", providerErr)
	}

	runner.syncContextUsageFromHistory(compacted)
	return compacted, result, nil
}
