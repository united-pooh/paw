// 本文件定义 / 命令同步补全和 @ 文件异步补全 TUI 组件。
package bubble

import (
	"fmt"
	"os"
	"paw/internal/complete"
	"paw/internal/skill"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// completionKind 区分命令补全和文件补全。
type completionKind int

const (
	completionKindCommand completionKind = iota
	completionKindFile
	completionKindSkill
)

// completion 保存补全弹窗的临时 UI 状态。
type completion struct {
	kind completionKind

	// 命令补全专用：过滤后的命令列表
	items []string

	// 文件补全专用
	atByteIndex   int      // @ 在 input.Value() 中的字节偏移
	query         string   // @ 之后到词尾的完整文本（含路径前缀）
	searchDir     string   // 解析出的搜索目录（用于判断是否需要重载）
	prefix        string   // searchDir 内的文件名前缀过滤
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

// 触发检测与路径解析的纯实现已提取到 internal/complete，供 TUI 与 Web 复用；
// 以下为保持既有调用与测试不变的薄封装。

func detectAtTrigger(value string) (atByteIndex int, query string) {
	return complete.DetectAtTrigger(value)
}

func detectSkillTrigger(value string) (dollarByteIndex int, query string) {
	return complete.DetectSkillTrigger(value)
}

func detectWordTrigger(value string, trigger rune) (byteIndex int, query string) {
	return complete.DetectWordTrigger(value, trigger)
}

// ──────────────────────────────────────────────────────────────────────────────
// 路径解析
// ──────────────────────────────────────────────────────────────────────────────

func resolveSearchDir(query string) (dir, prefix string) {
	return complete.ResolveSearchDirCWD(query)
}

func resolvePathParts(base, rest string) (dir, prefix string) {
	return complete.ResolvePathParts(base, rest)
}

// ──────────────────────────────────────────────────────────────────────────────
// appModel 同步逻辑（在 app.go 的 isTextEditingKey 分支调用）
// ──────────────────────────────────────────────────────────────────────────────

// syncAtCompletion 检测 @ 触发并更新 m.completion。
// 需要重新加载文件时返回 tea.Cmd，否则返回 nil。
func (m *appModel) syncAtCompletion() tea.Cmd {
	// / 命令和 $ skill 补全优先，不与 @ 同时激活
	if m.completion != nil && (m.completion.kind == completionKindCommand || m.completion.kind == completionKindSkill) {
		return nil
	}

	val := m.input.Value()
	atIdx, query := detectAtTrigger(val)

	if atIdx < 0 {
		// 没有触发点，清除文件补全
		if m.completion != nil && m.completion.kind == completionKindFile {
			m.clearCompletionAndRelayout()
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

// syncSkillCompletion 检测 $ skill 补全并更新 m.completion。
func (m *appModel) syncSkillCompletion() {
	if m.completion != nil && m.completion.kind == completionKindCommand {
		return
	}
	val := m.input.Value()
	dollarIdx, query := detectSkillTrigger(val)
	if dollarIdx < 0 {
		if m.completion != nil && m.completion.kind == completionKindSkill {
			m.clearCompletionAndRelayout()
		}
		return
	}
	items := skillCompletionItems(query, m.skillRegistry)
	if len(items) == 0 {
		if m.completion != nil && m.completion.kind == completionKindSkill {
			m.clearCompletionAndRelayout()
		}
		return
	}
	if m.completion == nil || m.completion.kind != completionKindSkill {
		m.completion = &completion{
			kind:        completionKindSkill,
			items:       items,
			atByteIndex: dollarIdx,
			prefix:      query,
		}
		m.sessionPicker = nil
	} else {
		m.completion.items = items
		m.completion.atByteIndex = dollarIdx
		m.completion.prefix = query
		if m.completion.selectedIndex >= len(items) {
			m.completion.selectedIndex = 0
		}
	}
}

// syncCommandCompletion 检测 / 命令补全并更新 m.completion。
func (m *appModel) syncCommandCompletion() {
	val := m.input.Value()
	slashIdx, query := detectWordTrigger(val, '/')
	if slashIdx < 0 {
		if m.completion != nil && m.completion.kind == completionKindCommand {
			m.clearCompletionAndRelayout()
		}
		return
	}
	inline := strings.TrimSpace(val[:slashIdx]) != ""
	items := commandCompletionItemsForContext(query, m.commandRegistry, m.skillRegistry, inline)
	if len(items) == 0 {
		m.clearCompletionAndRelayout()
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

// loadFilesInDirCmd 异步递归列出 searchDir 目录树下的条目，用 filePrefix 过滤。
// useDefault=true 时最多返回前 5 条。
func loadFilesInDirCmd(searchDir, filePrefix string, useDefault bool) tea.Cmd {
	return func() tea.Msg {
		all, err := listFilesRecursive(searchDir)
		if err != nil {
			return fileCompletionLoadedMsg{searchDir: searchDir}
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

func listFilesRecursive(searchDir string) ([]string, error) {
	return complete.ListFilesRecursive(searchDir)
}

func filterByPrefix(items []string, prefix string) []string {
	return complete.FilterByPrefix(items, prefix)
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
func commandCompletionItems(prefix string, registry *CommandRegistry, skillRegistries ...*skill.Registry) []string {
	return commandCompletionItemsForContext(prefix, registry, firstSkillRegistry(skillRegistries), false)
}

// commandCompletionItemsForContext 根据斜杠词所在位置构造候选。行首保留全部
// 命令；已有普通文本时仅保留可嵌入 prompt 的 task/streamma 命令。
// Skill 在两种场景下都完整保留。
func commandCompletionItemsForContext(prefix string, registry *CommandRegistry, skillRegistry *skill.Registry, inline bool) []string {
	if registry == nil && skillRegistry == nil {
		return nil
	}
	var items []string
	if registry != nil {
		for _, name := range registry.order {
			if inline && !isInlinePromptCommand(name) {
				continue
			}
			if prefix == "" || strings.HasPrefix(name, "/"+prefix) {
				items = append(items, name)
			}
		}
	}
	seen := map[string]bool{}
	for _, item := range items {
		seen[item] = true
	}
	for _, name := range skillCompletionItems(prefix, skillRegistry) {
		item := "/" + name
		if !seen[item] {
			items = append(items, item)
			seen[item] = true
		}
	}
	return items
}

func firstSkillRegistry(registries []*skill.Registry) *skill.Registry {
	if len(registries) == 0 {
		return nil
	}
	return registries[0]
}

func isInlinePromptCommand(name string) bool {
	return name == "/task" || name == "/streamma"
}

// skillCompletionItems 从 skill 注册表中筛选匹配前缀的技能名。
func skillCompletionItems(prefix string, registry *skill.Registry) []string {
	if registry == nil {
		return nil
	}
	matches := registry.Find(prefix)
	if len(matches) > 8 {
		matches = matches[:8]
	}
	items := make([]string, 0, len(matches))
	for _, sk := range matches {
		items = append(items, sk.Name)
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

	ref := buildAtRef(query, m.completion.searchDir, selected)
	start := len([]rune(val[:atIdx]))
	end := len([]rune(val))
	if !trailingSpace {
		// Directory traversal is an intermediate completion step and remains
		// ordinary editable text.
		m.replaceInputRange(start, end, ref)
		m.relayout()
		return m
	}
	m.replaceInputRangeWithToken(
		start,
		end,
		ref,
		strings.TrimPrefix(ref, "@"),
		inputTokenFile,
		true,
	)
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
	val := m.input.Value()
	slashIdx, _ := detectWordTrigger(val, '/')
	if slashIdx < 0 && val != "" {
		return m
	}
	start := 0
	if slashIdx >= 0 {
		start = len([]rune(val[:slashIdx]))
	}
	end := len([]rune(val))
	raw := selected
	label := strings.TrimPrefix(strings.TrimSpace(selected), "/")
	if m.commandRegistry != nil {
		if _, ok := m.commandRegistry.Lookup(selected); ok {
			m.replaceInputRangeWithToken(start, end, raw, label, inputTokenCommand, true)
			m.relayout()
			return m
		}
	}
	if ref, ok := m.slashSkillReference(selected); ok {
		raw = ref
		m.replaceInputRangeWithToken(start, end, raw, label, inputTokenSkill, true)
		m.relayout()
		return m
	}
	m.replaceInputRangeWithToken(start, end, raw, label, inputTokenCommand, true)
	m.relayout()
	return m
}

// applySkillCompletion 将选中 skill 写成 Codex 风格的 markdown reference。
func (m appModel) applySkillCompletion(selected string) appModel {
	val := m.input.Value()
	dollarIdx, _ := detectSkillTrigger(val)
	if dollarIdx < 0 {
		return m
	}
	ref := "$" + selected
	if m.skillRegistry != nil {
		if sk, ok := m.skillRegistry.Resolve(selected); ok && strings.TrimSpace(sk.Path) != "" {
			ref = skillMarkdownReference(sk)
		}
	}
	start := len([]rune(val[:dollarIdx]))
	m.replaceInputRangeWithToken(
		start,
		len([]rune(val)),
		ref,
		selected,
		inputTokenSkill,
		true,
	)
	m.relayout()
	return m
}

func (m appModel) slashSkillReference(selected string) (string, bool) {
	if m.skillRegistry == nil {
		return "", false
	}
	name := strings.TrimPrefix(strings.TrimSpace(selected), "/")
	if name == "" {
		return "", false
	}
	sk, ok := m.skillRegistry.Resolve(name)
	if !ok || strings.TrimSpace(sk.Path) == "" {
		return "", false
	}
	return skillMarkdownReference(sk), true
}

func skillMarkdownReference(sk skill.Skill) string {
	return fmt.Sprintf("[$%s](%s)", sk.Name, sk.Path)
}

func (m *appModel) clearCompletionAndRelayout() {
	if m.completion == nil {
		return
	}
	m.completion = nil
	m.relayout()
}

// ──────────────────────────────────────────────────────────────────────────────
// 渲染
// ──────────────────────────────────────────────────────────────────────────────

// renderCompletionBox 渲染补全弹窗（显示在输入框上方）。
func (m appModel) renderCompletionBox() string {
	if m.completion == nil {
		return ""
	}
	return m.renderCompletionPanel(m.renderCompletionContent())
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
	case completionKindSkill:
		title = wizardTitleStyle.Render("Skills")
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
			lines = append(lines, m.styles.SelectionSelected.Render(fmt.Sprintf("> %s", label)))
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
