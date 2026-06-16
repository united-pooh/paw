// 本文件定义 / 命令同步补全和 @ 文件异步补全 TUI 组件。
package bubble

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
)

// completionKind 区分命令补全和文件补全。
type completionKind int

const (
	completionKindCommand completionKind = iota
	completionKindFile
)

// completion 保存补全弹窗的临时 UI 状态。
type completion struct {
	kind completionKind

	// 命令补全专用：过滤后的命令列表
	items []string

	// 文件补全专用
	atByteIndex   int    // @ 在 input.Value() 中的字节偏移
	query         string // @ 之后到词尾的完整文本（含路径前缀）
	searchDir     string // 解析出的搜索目录（用于判断是否需要重载）
	prefix        string // searchDir 内的文件名前缀过滤
	allItems      []string // 目录内加载到的全部条目
	filteredItems []string // 按 prefix 过滤后的条目

	selectedIndex int
	loading       bool
}

// visibleItems 返回当前应展示在弹窗中的候选列表。
func (c *completion) visibleItems() []string {
	if c.kind == completionKindFile {
		return c.filteredItems
	}
	return c.items
}

// navigateUp 向上移动选中光标。
func (c *completion) navigateUp() {
	if c.selectedIndex > 0 {
		c.selectedIndex--
	}
}

// navigateDown 向下移动选中光标。
func (c *completion) navigateDown() {
	items := c.visibleItems()
	if c.selectedIndex < len(items)-1 {
		c.selectedIndex++
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// 触发检测
// ──────────────────────────────────────────────────────────────────────────────

// detectAtTrigger 在 value 中找到最末尾的词边界 @ 触发点。
// 词边界条件：@ 位于字符串开头，或前一个字符为空白符。
// query 为 @ 之后到字符串末尾的内容；query 内不能含空白符（否则说明词已结束）。
// 如果未找到满足条件的 @，返回 (-1, "")。
func detectAtTrigger(value string) (atByteIndex int, query string) {
	runes := []rune(value)
	n := len(runes)
	if n == 0 {
		return -1, ""
	}

	// 从末尾向前扫描，找到当前"词"的起始位置。
	// 词 = 连续的非空白字符序列（位于末尾）。
	wordStart := n // 默认：末尾是空白，没有当前词
	for i := n - 1; i >= 0; i-- {
		if unicode.IsSpace(runes[i]) {
			wordStart = i + 1
			break
		}
		wordStart = i
	}

	if wordStart >= n {
		return -1, "" // 当前词为空（末尾是空白）
	}

	// 当前词必须以 @ 开头
	if runes[wordStart] != '@' {
		return -1, ""
	}

	// 词边界：@ 在行首，或前一个字符为空白
	if wordStart > 0 && !unicode.IsSpace(runes[wordStart-1]) {
		return -1, "" // @ 紧跟非空白字符，不是词边界（如 "text@foo"）
	}

	// 计算 @ 在原始字节串中的偏移
	byteOff := 0
	for _, r := range runes[:wordStart] {
		byteOff += utf8.RuneLen(r)
	}

	// query = @ 之后的全部文本
	q := string(runes[wordStart+1:])
	return byteOff, q
}

// ──────────────────────────────────────────────────────────────────────────────
// 路径解析
// ──────────────────────────────────────────────────────────────────────────────

// resolveSearchDir 根据 @ 之后的 query 解析搜索目录和文件名前缀。
//
//   - @       → (cwd, "")
//   - @foo    → (cwd, "foo")
//   - @sub/   → (cwd/sub, "")
//   - @sub/f  → (cwd/sub, "f")
//   - @~/f    → (HOME, "f")
//   - @/etc/f → ("/etc", "f")
func resolveSearchDir(query string) (dir, prefix string) {
	cwd, _ := os.Getwd()
	if cwd == "" {
		cwd = "."
	}

	switch {
	case query == "" || query == ".":
		return cwd, ""

	case query == "~":
		home, _ := os.UserHomeDir()
		return home, ""

	case strings.HasPrefix(query, "~/"):
		home, err := os.UserHomeDir()
		if err != nil {
			return cwd, ""
		}
		return resolvePathParts(home, query[2:])

	case strings.HasPrefix(query, "/"):
		return resolvePathParts("/", query[1:])

	default:
		return resolvePathParts(cwd, query)
	}
}

// resolvePathParts 将 base/rest 分解为 (directory, filePrefix)。
// 若 rest 含路径分隔符，最后一段作为文件名前缀，其余拼入目录。
func resolvePathParts(base, rest string) (dir, prefix string) {
	if rest == "" {
		return base, ""
	}
	if !strings.Contains(rest, "/") {
		return base, rest
	}
	idx := strings.LastIndex(rest, "/")
	return filepath.Join(base, rest[:idx]), rest[idx+1:]
}

// ──────────────────────────────────────────────────────────────────────────────
// appModel 同步逻辑（在 app.go 的 isTextEditingKey 分支调用）
// ──────────────────────────────────────────────────────────────────────────────

// syncAtCompletion 检测 @ 触发并更新 m.completion。
// 需要重新加载文件时返回 tea.Cmd，否则返回 nil。
func (m *appModel) syncAtCompletion() tea.Cmd {
	// / 命令补全优先，不与 @ 同时激活
	if m.completion != nil && m.completion.kind == completionKindCommand {
		return nil
	}

	val := m.input.Value()
	atIdx, query := detectAtTrigger(val)

	if atIdx < 0 {
		// 没有触发点，清除文件补全
		if m.completion != nil && m.completion.kind == completionKindFile {
			m.completion = nil
		}
		return nil
	}

	searchDir, prefix := resolveSearchDir(query)
	useDefault := query == ""

	if m.completion == nil || m.completion.kind != completionKindFile || m.completion.searchDir != searchDir {
		// 目录发生变化或补全尚未创建：重新加载
		m.completion = &completion{
			kind:        completionKindFile,
			atByteIndex: atIdx,
			query:       query,
			searchDir:   searchDir,
			prefix:      prefix,
			loading:     true,
		}
		m.sessionPicker = nil
		return loadFilesInDirCmd(searchDir, prefix, useDefault)
	}

	// 同一目录：仅更新前缀和过滤结果
	m.completion.atByteIndex = atIdx
	m.completion.query = query
	m.completion.prefix = prefix
	filtered := filterByPrefix(m.completion.allItems, prefix)
	if useDefault && len(filtered) > 5 {
		filtered = filtered[:5]
	}
	m.completion.filteredItems = filtered
	if m.completion.selectedIndex >= len(filtered) {
		m.completion.selectedIndex = 0
	}
	return nil
}

// syncCommandCompletion 检测 / 命令补全并更新 m.completion。
func (m *appModel) syncCommandCompletion() {
	val := m.input.Value()
	if !strings.HasPrefix(val, "/") {
		if m.completion != nil && m.completion.kind == completionKindCommand {
			m.completion = nil
		}
		return
	}
	query := strings.TrimPrefix(val, "/")
	items := commandCompletionItems(query, m.commandRegistry)
	if len(items) == 0 {
		m.completion = nil
		return
	}
	if m.completion == nil || m.completion.kind != completionKindCommand {
		m.completion = &completion{kind: completionKindCommand, items: items, prefix: query}
	} else {
		m.completion.items = items
		m.completion.prefix = query
		if m.completion.selectedIndex >= len(items) {
			m.completion.selectedIndex = 0
		}
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// 异步文件加载
// ──────────────────────────────────────────────────────────────────────────────

// loadFilesInDirCmd 异步列出 searchDir 目录下的条目，用 filePrefix 过滤。
// useDefault=true 时最多返回前 5 条。
func loadFilesInDirCmd(searchDir, filePrefix string, useDefault bool) tea.Cmd {
	return func() tea.Msg {
		entries, err := os.ReadDir(searchDir)
		if err != nil {
			return fileCompletionLoadedMsg{searchDir: searchDir}
		}

		var all []string
		for _, e := range entries {
			name := e.Name()
			if strings.HasPrefix(name, ".") {
				continue
			}
			if e.IsDir() {
				all = append(all, name+"/")
			} else {
				all = append(all, name)
			}
		}

		filtered := filterByPrefix(all, filePrefix)
		if useDefault && filePrefix == "" && len(filtered) > 5 {
			filtered = filtered[:5]
		}

		return fileCompletionLoadedMsg{
			searchDir: searchDir,
			items:     all,
			filtered:  filtered,
		}
	}
}

// filterByPrefix 筛选以 prefix 开头的条目（大小写不敏感）。
func filterByPrefix(items []string, prefix string) []string {
	if prefix == "" {
		out := make([]string, len(items))
		copy(out, items)
		return out
	}
	lower := strings.ToLower(prefix)
	var out []string
	for _, item := range items {
		if strings.HasPrefix(strings.ToLower(item), lower) {
			out = append(out, item)
		}
	}
	return out
}

// ──────────────────────────────────────────────────────────────────────────────
// 命令补全构造
// ──────────────────────────────────────────────────────────────────────────────

// newCommandCompletion 创建命令补全（同步）。
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

// commandCompletionItems 从注册表中筛选匹配前缀的命令名。
func commandCompletionItems(prefix string, registry *CommandRegistry) []string {
	if registry == nil {
		return nil
	}
	var items []string
	for _, name := range registry.order {
		if prefix == "" || strings.HasPrefix(name, "/"+prefix) {
			items = append(items, name)
		}
	}
	return items
}

// ──────────────────────────────────────────────────────────────────────────────
// 补全确认 — 写回输入框
// ──────────────────────────────────────────────────────────────────────────────

// applyFileCompletion 将选中文件写回输入框，只替换 @query 段，保留之前的文本。
// trailingSpace=true（Enter）在路径后追加空格以结束引用；false（Tab）不追加，光标停在路径末尾。
func (m appModel) applyFileCompletion(selected string, trailingSpace bool) appModel {
	if m.completion == nil {
		return m
	}
	val := m.input.Value()
	atIdx, query := detectAtTrigger(val)
	if atIdx < 0 {
		return m
	}

	before := val[:atIdx]
	ref := buildAtRef(query, m.completion.searchDir, selected)
	suffix := ""
	if trailingSpace {
		suffix = " "
	}
	m.input.SetValue(before + ref + suffix)
	m.input.CursorEnd()
	m.relayout()
	return m
}

// buildAtRef 根据原始 query 和 selected 构造 @path 引用。
// 保留 query 中的路径前缀（如 @~/ 或 @/），只替换最末的文件名片段。
func buildAtRef(query, searchDir, selected string) string {
	// 找到最后一个 / 的位置，保留 @ + 路径前缀
	if idx := strings.LastIndex(query, "/"); idx >= 0 {
		return "@" + query[:idx+1] + selected
	}
	// 没有 / 分隔符，直接 @selected
	_ = searchDir
	return "@" + selected
}

// applyCommandCompletion 将命令填入输入框。
func (m appModel) applyCommandCompletion(selected string) appModel {
	m.input.SetValue(selected + " ")
	m.input.CursorEnd()
	return m
}

// ──────────────────────────────────────────────────────────────────────────────
// 渲染
// ──────────────────────────────────────────────────────────────────────────────

// renderCompletionBox 渲染补全弹窗（显示在输入框上方）。
func (m appModel) renderCompletionBox() string {
	if m.completion == nil {
		return ""
	}
	width := maxInt(32, m.width-2)
	return wizardPanelStyle.Width(width).Render(m.renderCompletionContent())
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
		lines = append(lines, "  Loading...")
		return strings.Join(lines, "\n")
	}

	visible := c.visibleItems()
	if len(visible) == 0 {
		lines = append(lines, "  No matches.")
		return strings.Join(lines, "\n")
	}

	maxVisible := 8
	start := 0
	if c.selectedIndex >= maxVisible {
		start = c.selectedIndex - maxVisible + 1
	}
	end := start + maxVisible
	if end > len(visible) {
		end = len(visible)
	}

	for i := start; i < end; i++ {
		label := visible[i]
		if i == c.selectedIndex {
			lines = append(lines, selectedProviderStyle.Render(fmt.Sprintf("> %s", label)))
		} else {
			lines = append(lines, unselectedProviderStyle.Render(fmt.Sprintf("  %s", label)))
		}
	}
	return strings.Join(lines, "\n")
}

// ──────────────────────────────────────────────────────────────────────────────
// 兼容占位（避免外部调用编译错误）
// ──────────────────────────────────────────────────────────────────────────────

// newFileCompletion 兼容旧调用签名。
func newFileCompletion(_ string) *completion {
	return &completion{kind: completionKindFile, loading: true}
}

// loadFileCompletionCmd 兼容旧调用，加载 cwd 默认 5 个文件。
func loadFileCompletionCmd() tea.Cmd {
	cwd, _ := os.Getwd()
	return loadFilesInDirCmd(cwd, "", true)
}
