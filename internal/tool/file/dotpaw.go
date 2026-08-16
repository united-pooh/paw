package file

import (
	"fmt"
	"path/filepath"
)

// isInsideDotPawRoot 判断 target 是否位于 root/.paw 之下。.paw 目录由内部会话
// 存储与任务注册表管理，task worker 的文件工具（Write/Edit）不允许写入它。
// 返回 (inside, err)；路径解析失败时返回 err。
func isInsideDotPawRoot(root, target string) (bool, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return false, err
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return false, err
	}
	pawDir := filepath.Join(filepath.Clean(absRoot), ".paw")
	return isWithinRoot(pawDir, filepath.Clean(absTarget)), nil
}

// forbidDotPaw 在沙箱 worker 写入 .paw 时返回错误；其他路径返回 nil。
func forbidDotPaw(root, target string) error {
	inside, err := isInsideDotPawRoot(root, target)
	if err != nil {
		return err
	}
	if inside {
		return fmt.Errorf(".paw 目录由内部会话存储管理，禁止工具写入: %s", relativePath(root, target))
	}
	return nil
}