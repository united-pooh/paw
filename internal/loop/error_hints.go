package loop

import (
	"errors"
	"fmt"

	"paw/internal/model"
)

// decorateToolPairError 在检测到「工具调用配对损坏」类 400 时附加修复提示。
// 对齐 CodeWhale 的错误处理：此类错误不可重试，只能修复历史或换新请求；
// 提示仅附加在命中时，错误原文与 errors.As/Is 语义保持不变。
func decorateToolPairError(err error) error {
	if err == nil {
		return nil
	}
	var providerErr *model.ProviderHTTPError
	if !errors.As(err, &providerErr) || !providerErr.IsToolPairingInvalidRequest() {
		return err
	}
	return fmt.Errorf("%w（会话中的工具调用记录不完整，可能由中断导致；已自动修复，若仍复现请使用 /compact 或 /clear 后继续）", err)
}
