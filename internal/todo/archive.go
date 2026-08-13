package todo

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// archiveMarker 是归档行的去重标记：`<!-- todo:<id> -->`。
// 以文件内容为去重状态，保证跨进程/重启幂等。
var archiveMarker = regexp.MustCompile(`<!-- todo:([^ ]+) -->`)

// ArchiveWriter 把已完成条目沉淀到 memory/progress.md（跨会话档案）。
// 每次快照中 status=completed 且未归档的条目追加为一行：
//
//   - [x] <content> <!-- todo:<id> -->
//
// 与 update_todo 的过程跟踪职责分离：只沉淀完成结果，不镜像 todo 状态。
type ArchiveWriter struct {
	mu       sync.Mutex
	path     string
	archived map[string]bool
}

// NewArchiveWriter 扫描已有档案中的 todo 标记，用于幂等去重。
func NewArchiveWriter(path string) (*ArchiveWriter, error) {
	w := &ArchiveWriter{path: path, archived: map[string]bool{}}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return w, nil
		}
		return nil, err
	}
	for _, m := range archiveMarker.FindAllSubmatch(data, -1) {
		w.archived[string(m[1])] = true
	}
	return w, nil
}

// ArchiveCompleted 追加快照中新增的已完成条目，返回实际追加条数。
// best-effort：写入失败返回错误，调用方决定是否忽略。
func (a *ArchiveWriter) ArchiveCompleted(ctx context.Context, snapshot Snapshot) (int, error) {
	if a == nil || a.path == "" {
		return 0, nil
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()

	var lines []string
	for _, item := range snapshot.Items {
		if item.Status != StatusCompleted {
			continue
		}
		if a.archived[item.ID] {
			continue
		}
		content := strings.Join(strings.Fields(item.Content), " ")
		lines = append(lines, fmt.Sprintf("- [x] %s <!-- todo:%s -->", content, item.ID))
		a.archived[item.ID] = true
	}
	if len(lines) == 0 {
		return 0, nil
	}

	var buf strings.Builder
	data, err := os.ReadFile(a.path)
	if err != nil && !os.IsNotExist(err) {
		return 0, err
	}
	if len(data) == 0 {
		buf.WriteString("# Progress\n\n")
	} else if !strings.HasSuffix(string(data), "\n") {
		buf.Write(data)
		buf.WriteByte('\n')
	} else {
		buf.Write(data)
	}
	for _, line := range lines {
		buf.WriteString(line)
		buf.WriteByte('\n')
	}
	if err := os.MkdirAll(filepath.Dir(a.path), 0o755); err != nil {
		return 0, err
	}
	if err := os.WriteFile(a.path, []byte(buf.String()), 0o644); err != nil {
		return 0, err
	}
	return len(lines), nil
}
