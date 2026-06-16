// 本文件定义 / 命令同步补全和 @ 文件异步补全 TUI 组件。
package bubble

import (
	"fmt"
	tea "github.com/charmbracelet/bubbletea"
	"os"
	"path/filepath"
	"strings"
)

// completionKind 区分命令补全和文件补全。
type completionKind int

const (
	completionKindCommand completionKind = iota
	completionKindFile
)

// completion 保存补全弹窗的临时 UI 状态。
type completion struct {
	kind          completionKind
	items         []string
	selectedIndex int
	prefix        string // 用于过滤的前缀（不含 / 或 @）
	loading       bool
}

// newCommandCompletion 创建命令补全（同步，从注册表获取）。
func newCommandCompletion(prefix string, registry *CommandRegistry) *completion {
	items := commandCompletionItems(prefix, registry)
	if len(items) == 0 {
		return nil
	}
	return &completion{
		kind:   completionKindCommand,
		items:  items,
		prefix: prefix,
	}
}

// newFileCompletion 创建文件补全（异步加载）。
func newFileCompletion(prefix string) *completion {
	return &completion{
		kind:    completionKindFile,
		prefix:  prefix,
		loading: true,
	}
}

// commandCompletionItems 从注册表中筛选匹配前缀的命令名。
func commandCompletionItems(prefix string, registry *CommandRegistry) []string {
	if registry == nil {
		return nil
	}
	var items []string
	for _, name := range registry.order {
		if strings.HasPrefix(name, "/"+prefix) || prefix == "" {
			items = append(items, name)
		}
	}
	return items
}

// loadFileCompletionCmd 异步扫描 cwd，深度 ≤ 2，跳过隐藏目录和 .ccagent/，最多 50 个。
func loadFileCompletionCmd() tea.Cmd {
	return func() tea.Msg {
		cwd, err := os.Getwd()
		if err != nil {
			return fileCompletionLoadedMsg{err: err}
		}

		var items []string
		maxItems := 50

		err = filepath.WalkDir(cwd, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return nil // 跳过错误
			}
			if d.IsDir() {
				name := d.Name()
				// 跳过隐藏目录和 .ccagent/
				if name != "." && (strings.HasPrefix(name, ".") || name == ".ccagent") {
					return filepath.SkipDir
				}
				// 计算相对深度
				rel, err := filepath.Rel(cwd, path)
				if err != nil {
					return nil
				}
				depth := strings.Count(rel, string(filepath.Separator))
				if depth >= 2 {
					return filepath.SkipDir
				}
				return nil
			}
			if len(items) >= maxItems {
				return filepath.SkipAll
			}
			rel, err := filepath.Rel(cwd, path)
			if err != nil {
				return nil
			}
			items = append(items, rel)
			return nil
		})
		if err != nil && !strings.Contains(err.Error(), "SkipAll") {
			return fileCompletionLoadedMsg{err: err}
		}
		return fileCompletionLoadedMsg{items: items}
	}
}

// handleCompletionKey 处理补全弹窗中的方向键、确认键和取消键。
func (m appModel) handleCompletionKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.completion == nil {
		return m, nil
	}

	switch msg.String() {
	case "ctrl+c", "esc":
		m.completion = nil
		return m, nil
	case "up", "k":
		if m.completion.selectedIndex > 0 {
			m.completion.selectedIndex--
		}
		return m, nil
	case "down", "j":
		if m.completion.selectedIndex < len(m.completion.items)-1 {
			m.completion.selectedIndex++
		}
		return m, nil
	case "tab", "enter":
		if m.completion.loading || len(m.completion.items) == 0 {
			m.completion = nil
			return m, nil
		}
		selected := m.completion.items[m.completion.selectedIndex]
		m = m.applyCompletion(selected)
		m.completion = nil
		return m, nil
	}

	return m, nil
}

// applyCompletion 将选中的补全项写入输入框。
func (m appModel) applyCompletion(selected string) appModel {
	switch m.completion.kind {
	case completionKindCommand:
		m.input.SetValue(selected + " ")
		// 将光标移到末尾
		m.input.CursorEnd()
	case completionKindFile:
		m.input.SetValue("@" + selected)
		m.input.CursorEnd()
	}
	return m
}

// renderCompletionBox 渲染补全弹窗（显示在输入框上方）。
func (m appModel) renderCompletionBox() string {
	if m.completion == nil {
		return ""
	}
	width := maxInt(32, m.width-2)
	body := m.renderCompletionContent()
	return wizardPanelStyle.Width(width).Render(body)
}

// renderCompletionContent 渲染补全弹窗内容。
func (m appModel) renderCompletionContent() string {
	c := m.completion
	var title string
	switch c.kind {
	case completionKindCommand:
		title = wizardTitleStyle.Render("Commands")
	case completionKindFile:
		title = wizardTitleStyle.Render("Files")
	}
	lines := []string{title}

	if c.loading {
		lines = append(lines, "Loading...")
		return strings.Join(lines, "\n")
	}

	if len(c.items) == 0 {
		lines = append(lines, "No matches.")
		return strings.Join(lines, "\n")
	}

	maxVisible := 8
	start := 0
	if c.selectedIndex >= maxVisible {
		start = c.selectedIndex - maxVisible + 1
	}
	end := start + maxVisible
	if end > len(c.items) {
		end = len(c.items)
	}

	for i := start; i < end; i++ {
		item := c.items[i]
		label := item
		if c.kind == completionKindCommand {
			// 显示命令描述
			label = item
		}
		if i == c.selectedIndex {
			lines = append(lines, selectedProviderStyle.Render(fmt.Sprintf("> %s", label)))
		} else {
			lines = append(lines, unselectedProviderStyle.Render(fmt.Sprintf("  %s", label)))
		}
	}

	return strings.Join(lines, "\n")
}
