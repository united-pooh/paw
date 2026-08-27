package bubble

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"

	configv2 "paw/internal/config"
	"paw/internal/settings"
	"paw/internal/theme"
)

// configCenterTabLabels 是顶部 tab 行的顺序（通用 / 服务商 / 模型 /
// 凭据 / 连接 / 诊断）。各子页归属其对应的顶层 tab。
var configCenterTabLabels = []string{"通用", "服务商", "模型", "凭据", "连接", "诊断"}

// configCenterTabPages 是每个 tab 切换后落入的顶层页。
var configCenterTabPages = []configCenterPage{
	configCenterGeneral,
	configCenterProviders,
	configCenterModels,
	configCenterCredentials,
	configCenterConnection,
	configCenterDiagnostics,
}

type configCenterOptionMatch struct {
	option configCenterOption
	index  int
}

// configCenterDisplayedOptions 把非 General 页的普通选项也接入搜索框。index
// 始终保留原始选项下标，因此过滤后 Enter 仍会触发正确动作。
func (m appModel) configCenterDisplayedOptions() []configCenterOptionMatch {
	options := m.configCenterOptions()
	query := strings.ToLower(strings.TrimSpace(m.configCenter.search))
	matches := make([]configCenterOptionMatch, 0, len(options))
	for index, option := range options {
		if query != "" && !strings.Contains(strings.ToLower(option.label+" "+option.description), query) {
			continue
		}
		matches = append(matches, configCenterOptionMatch{option: option, index: index})
	}
	return matches
}

func (m *appModel) resetConfigCenterSelectionForSearch() {
	if m.configCenter == nil || m.configCenter.page == configCenterGeneral {
		if m.configCenter != nil {
			m.configCenter.selected = 0
		}
		return
	}
	matches := m.configCenterDisplayedOptions()
	if len(matches) == 0 {
		m.configCenter.selected = 0
		return
	}
	m.configCenter.selected = matches[0].index
}

// configCenterTabForPage 把当前页映射到所属 tab 索引；Edit 无归属，返回 -1。
func configCenterTabForPage(page configCenterPage) int {
	switch page {
	case configCenterGeneral:
		return 0
	case configCenterProviders, configCenterProviderActions, configCenterAddProvider:
		return 1
	case configCenterModels, configCenterModelActions, configCenterAddModelProvider:
		return 2
	case configCenterCredentials, configCenterCredentialActions:
		return 3
	case configCenterConnection:
		return 4
	case configCenterDiagnostics:
		return 5
	}
	return -1
}

// switchConfigCenterTab 在 tab 之间循环切换：left 退一档、right/tab 进一档。
// 切 tab 清空搜索并把 selected 复位；落入新顶层页时按需失效 catalog 缓存。
func (m *appModel) switchConfigCenterTab(key string) {
	state := m.configCenter
	if state == nil {
		return
	}
	current := configCenterTabForPage(state.page)
	if current < 0 {
		current = 0
	}
	delta := 1
	if key == "left" {
		delta = -1
	}
	next := (current + delta + len(configCenterTabPages)) % len(configCenterTabPages)
	target := configCenterTabPages[next]
	state.page = target
	state.selected = 0
	state.search = ""
	state.searchActive = false
	state.err = ""
	state.confirmAction = ""
	if target == configCenterModels {
		m.refreshConfigCenterCatalog(target)
	} else {
		state.invalidateCatalog()
	}
}

// renderConfigCenterTabs 渲染顶部导航。Settings 是模块标题，当前 tab 使用
// 终端常见的反色块；不再额外加方括号，避免产生两套选中符号。窄终端切换为
// 紧凑标签，保证最后一个 tab 不被截断。
func (m appModel) renderConfigCenterTabs(contentWidth int) string {
	state := m.configCenter
	active := configCenterTabForPage(state.page)
	render := func(labels []string, gap string) string {
		parts := make([]string, 0, len(labels)+1)
		parts = append(parts, m.styles.InputPrompt.Copy().Bold(true).Render("设置"))
		for i, label := range labels {
			if i == active {
				parts = append(parts, m.styles.SelectionSelected.Copy().Padding(0, 1).Render(label))
			} else {
				parts = append(parts, m.styles.Body.Copy().Bold(true).Render(label))
			}
		}
		return strings.Join(parts, gap)
	}
	row := render(configCenterTabLabels, "  ")
	if terminalCellWidth(row) <= contentWidth {
		return row
	}
	row = render([]string{"通用", "服务", "模型", "凭据", "连接", "诊断"}, " ")
	return truncateStyledCellLine(row, contentWidth)
}

// renderConfigCenterSearch 渲染真正的单行输入框，而不是旧实现里一行裸文字
// 再跟一条横线。搜索框与 tab/footer 使用同一页面宽度和左缘。搜索模式下
// 在搜索词末尾渲染反色光标块，提示当前按键都进入搜索框。
func (m appModel) renderConfigCenterSearch(contentWidth int) string {
	state := m.configCenter
	query := state.search
	icon := m.styles.InputPrompt.Copy().Bold(true).Render("⌕")
	var text string
	switch {
	case state.searchActive:
		text = m.styles.Body.Render(query) + m.styles.SelectionSelected.Render(" ")
	case query == "":
		text = m.styles.StatusMuted.Render("按 / 搜索…")
	default:
		text = m.styles.Body.Render(query)
	}
	searchStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(m.styles.InputPrompt.GetForeground()).
		Padding(0, 1)
	return renderFixedStyledPanel(searchStyle, contentWidth, 3, icon+" "+text)
}

// configCenterHintBar 返回底部常驻 hint。通用页使用“编辑”，其余 tab 使用
// “选择”（Enter 是选中/激活而非编辑）。搜索模式下提示退出方式，避免用户
// 发现 b/j/k 突然“失灵”。
func (m appModel) configCenterHintBar() string {
	if m.configCenter.searchActive {
		return m.styles.StatusMuted.Render("输入筛选 · ↑/↓ 选择 · Enter 确认 · Esc 退出搜索")
	}
	switch m.configCenter.page {
	case configCenterGeneral:
		return m.styles.StatusMuted.Render("/ 搜索 · Enter 编辑 · ↑/↓ 选择 · Tab/←/→ 切换 · Esc 关闭")
	case configCenterModels:
		return m.styles.StatusMuted.Render("/ 搜索 · Enter 设为当前 · Space 管理 · ↑/↓ 选择 · Tab/←/→ 切换 · Esc 关闭")
	default:
		return m.styles.StatusMuted.Render("/ 搜索 · Enter 选择 · ↑/↓ 选择 · Tab/←/→ 切换 · b 返回 · Esc 关闭")
	}
}

// configGeneralKind 描述 General 扁平列表中一个字段的编辑方式。
type configGeneralKind int

const (
	configGeneralEnum            configGeneralKind = iota // settings 枚举/布尔：Enter 循环并保存
	configGeneralInt                                      // settings 整数：Enter 内联输入
	configGeneralFloat                                    // settings 浮点：Enter 内联输入
	configGeneralModelParameter                           // 当前激活模型参数：Enter 循环并写回 config-v2
	configGeneralDocumentBoolean                          // config.jsonc 顶层布尔：Enter 循环并热应用
)

const (
	configGeneralYoloKey            = "security.yolo"
	configGeneralThinkingKey        = "model.thinking"
	configGeneralReasoningEffortKey = "model.reasoning_effort"
)

// configGeneralField 描述 General 页的一个扁平字段：显示 key、当前值和编辑方式。
// settings.json 字段使用 get/set/parse；config.jsonc 与当前模型参数由专用分支处理。
type configGeneralField struct {
	key     string
	kind    configGeneralKind
	options []string // enum/bool 的循环候选（按显示顺序）
	get     func(cfg settings.Config) string
	set     func(cfg *settings.Config, value string) // value 已校验/归一化
	parse   func(raw string) (string, error)         // int/float 内联：校验并归一化；enum 为 nil
}

// themeIDOptions 返回所有内置主题 ID（ui.theme 枚举的循环候选）。
func themeIDOptions() []string {
	out := make([]string, 0, 8)
	for _, item := range theme.List() {
		out = append(out, string(item.ID))
	}
	return out
}

// meterLocationOptions 是 ui.context_meter_location 的循环候选。
// 注意 NormalizeMeterLocation 当前强制 input-above，故循环保存后会归一回
// input-above（这是既有 normalize 行为，不在本轮改动范围）。
func meterLocationOptions() []string {
	return []string{
		string(settings.MeterLocationInputAbove),
		string(settings.MeterLocationInputTitle),
		string(settings.MeterLocationHeader),
	}
}

// configGeneralFields 按界面顺序返回 General 页字段。全局安全开关与当前模型
// 常用推理参数放在最前，其余字段保持 settings.json 的 spec 表顺序。
func configGeneralFields() []configGeneralField {
	return []configGeneralField{
		{
			key:     configGeneralYoloKey,
			kind:    configGeneralDocumentBoolean,
			options: []string{"false", "true"},
		},
		{
			key:  configGeneralThinkingKey,
			kind: configGeneralModelParameter,
		},
		{
			key:  configGeneralReasoningEffortKey,
			kind: configGeneralModelParameter,
		},
		{
			key:     "compression.mode",
			kind:    configGeneralEnum,
			options: []string{string(settings.CompressionModeState), string(settings.CompressionModeSummary)},
			get: func(cfg settings.Config) string {
				return string(settings.NormalizeCompressionMode(cfg.ContextCompression.Mode))
			},
			set: func(cfg *settings.Config, value string) {
				cfg.ContextCompression.Mode = settings.CompressionMode(value)
			},
		},
		{
			key:  "compression.resume_recent_turns",
			kind: configGeneralInt,
			get:  func(cfg settings.Config) string { return strconv.Itoa(cfg.ContextCompression.ResumeRecentTurns) },
			set: func(cfg *settings.Config, value string) {
				n, _ := strconv.Atoi(value)
				cfg.ContextCompression.ResumeRecentTurns = n
			},
			parse: parseGeneralInt,
		},
		{
			key:  "compression.state_compaction_ratio",
			kind: configGeneralFloat,
			get: func(cfg settings.Config) string {
				return formatGeneralFloat(cfg.ContextCompression.StateCompactionRatio)
			},
			set: func(cfg *settings.Config, value string) {
				f, _ := strconv.ParseFloat(value, 64)
				cfg.ContextCompression.StateCompactionRatio = f
			},
			parse: parseGeneralFloat,
		},
		{
			key:     "task.default_context_mode",
			kind:    configGeneralEnum,
			options: []string{string(settings.ContextModeEmpty), string(settings.ContextModeFork)},
			get:     func(cfg settings.Config) string { return string(cfg.Task.DefaultContextMode) },
			set: func(cfg *settings.Config, value string) {
				cfg.Task.DefaultContextMode = settings.ContextMode(value)
			},
		},
		{
			key:     "task.default_run_mode",
			kind:    configGeneralEnum,
			options: []string{string(settings.RunModeSync), string(settings.RunModeBackground)},
			get:     func(cfg settings.Config) string { return string(cfg.Task.DefaultRunMode) },
			set:     func(cfg *settings.Config, value string) { cfg.Task.DefaultRunMode = settings.RunMode(value) },
		},
		{
			key:   "task.wait_timeout_ms",
			kind:  configGeneralInt,
			get:   func(cfg settings.Config) string { return strconv.Itoa(cfg.Task.WaitTimeoutMs) },
			set:   func(cfg *settings.Config, value string) { n, _ := strconv.Atoi(value); cfg.Task.WaitTimeoutMs = n },
			parse: parseGeneralInt,
		},
		{
			key:     "ui.theme",
			kind:    configGeneralEnum,
			options: themeIDOptions(),
			get:     func(cfg settings.Config) string { return string(cfg.UI.Theme) },
			set:     func(cfg *settings.Config, value string) { cfg.UI.Theme = theme.ThemeID(value) },
		},
		{
			key:   "ui.context_limit_tokens",
			kind:  configGeneralInt,
			get:   func(cfg settings.Config) string { return strconv.Itoa(cfg.UI.ContextLimitTokens) },
			set:   func(cfg *settings.Config, value string) { n, _ := strconv.Atoi(value); cfg.UI.ContextLimitTokens = n },
			parse: parseGeneralInt,
		},
		{
			key:     "ui.context_meter_location",
			kind:    configGeneralEnum,
			options: meterLocationOptions(),
			get:     func(cfg settings.Config) string { return string(cfg.UI.ContextMeterLocation) },
			set:     func(cfg *settings.Config, value string) { cfg.UI.ContextMeterLocation = settings.MeterLocation(value) },
		},
		{
			key:     "ui.transcript_output_mode",
			kind:    configGeneralEnum,
			options: []string{string(settings.TranscriptOutputModeLine), string(settings.TranscriptOutputModeChar)},
			get: func(cfg settings.Config) string {
				return string(settings.NormalizeTranscriptOutputMode(cfg.UI.TranscriptOutputMode))
			},
			set: func(cfg *settings.Config, value string) {
				cfg.UI.TranscriptOutputMode = settings.TranscriptOutputMode(value)
			},
		},
		{
			key:     "ui.translate_on_double_click",
			kind:    configGeneralEnum,
			options: []string{"false", "true"},
			get:     func(cfg settings.Config) string { return boolGeneralLabel(cfg.UI.TranslateOnDoubleClick) },
			set:     func(cfg *settings.Config, value string) { cfg.UI.TranslateOnDoubleClick = value == "true" },
		},
		{
			key:  "context_maintenance.soft_compact_ratio",
			kind: configGeneralFloat,
			get:  func(cfg settings.Config) string { return formatGeneralFloat(cfg.ContextMaintenance.SoftCompactRatio) },
			set: func(cfg *settings.Config, value string) {
				f, _ := strconv.ParseFloat(value, 64)
				cfg.ContextMaintenance.SoftCompactRatio = f
			},
			parse: parseGeneralFloat,
		},
		{
			key:  "context_maintenance.tool_result_snip_ratio",
			kind: configGeneralFloat,
			get: func(cfg settings.Config) string {
				return formatGeneralFloat(cfg.ContextMaintenance.ToolResultSnipRatio)
			},
			set: func(cfg *settings.Config, value string) {
				f, _ := strconv.ParseFloat(value, 64)
				cfg.ContextMaintenance.ToolResultSnipRatio = f
			},
			parse: parseGeneralFloat,
		},
		{
			key:  "context_maintenance.compact_ratio",
			kind: configGeneralFloat,
			get:  func(cfg settings.Config) string { return formatGeneralFloat(cfg.ContextMaintenance.CompactRatio) },
			set: func(cfg *settings.Config, value string) {
				f, _ := strconv.ParseFloat(value, 64)
				cfg.ContextMaintenance.CompactRatio = f
			},
			parse: parseGeneralFloat,
		},
		{
			key:  "context_maintenance.compact_force_ratio",
			kind: configGeneralFloat,
			get:  func(cfg settings.Config) string { return formatGeneralFloat(cfg.ContextMaintenance.CompactForceRatio) },
			set: func(cfg *settings.Config, value string) {
				f, _ := strconv.ParseFloat(value, 64)
				cfg.ContextMaintenance.CompactForceRatio = f
			},
			parse: parseGeneralFloat,
		},
		{
			key:  "context_maintenance.compact_target_ratio",
			kind: configGeneralFloat,
			get:  func(cfg settings.Config) string { return formatGeneralFloat(cfg.ContextMaintenance.CompactTargetRatio) },
			set: func(cfg *settings.Config, value string) {
				f, _ := strconv.ParseFloat(value, 64)
				cfg.ContextMaintenance.CompactTargetRatio = f
			},
			parse: parseGeneralFloat,
		},
		{
			key:  "context_maintenance.tail_tokens",
			kind: configGeneralInt,
			get:  func(cfg settings.Config) string { return strconv.Itoa(cfg.ContextMaintenance.TailTokens) },
			set: func(cfg *settings.Config, value string) {
				n, _ := strconv.Atoi(value)
				cfg.ContextMaintenance.TailTokens = n
			},
			parse: parseGeneralInt,
		},
		{
			key:  "context_maintenance.min_tool_result_bytes",
			kind: configGeneralInt,
			get:  func(cfg settings.Config) string { return strconv.Itoa(cfg.ContextMaintenance.MinToolResultBytes) },
			set: func(cfg *settings.Config, value string) {
				n, _ := strconv.Atoi(value)
				cfg.ContextMaintenance.MinToolResultBytes = n
			},
			parse: parseGeneralInt,
		},
		{
			key:     "context_maintenance.keep_errors",
			kind:    configGeneralEnum,
			options: []string{"false", "true"},
			get:     func(cfg settings.Config) string { return boolGeneralLabel(cfg.ContextMaintenance.KeepErrors) },
			set:     func(cfg *settings.Config, value string) { cfg.ContextMaintenance.KeepErrors = value == "true" },
		},
		{
			key:     "context_maintenance.keep_user_marked",
			kind:    configGeneralEnum,
			options: []string{"false", "true"},
			get:     func(cfg settings.Config) string { return boolGeneralLabel(cfg.ContextMaintenance.KeepUserMarked) },
			set:     func(cfg *settings.Config, value string) { cfg.ContextMaintenance.KeepUserMarked = value == "true" },
		},
		{
			key:     "context_maintenance.archive_enabled",
			kind:    configGeneralEnum,
			options: []string{"false", "true"},
			get:     func(cfg settings.Config) string { return boolGeneralLabel(cfg.ContextMaintenance.ArchiveEnabled) },
			set:     func(cfg *settings.Config, value string) { cfg.ContextMaintenance.ArchiveEnabled = value == "true" },
		},
	}
}

type configGeneralPresentation struct {
	label       string
	description string
}

// configGeneralPresentations 只负责显示层。持久化 key 和枚举值保持不变，
// 避免中文界面影响 settings.json 兼容性及运行时热更新。
var configGeneralPresentations = map[string]configGeneralPresentation{
	configGeneralYoloKey: {
		label:       "YOLO 模式",
		description: "允许 Read 访问工作区外路径，并传递给后续 worker",
	},
	configGeneralThinkingKey: {
		label:       "推理开关",
		description: "控制当前模型是否启用推理过程",
	},
	configGeneralReasoningEffortKey: {
		label:       "推理强度",
		description: "设置当前模型的推理投入等级",
	},
	"compression.mode": {
		label:       "压缩模式",
		description: "选择状态快照压缩或 LLM 摘要压缩",
	},
	"compression.resume_recent_turns": {
		label:       "保留最近对话轮数",
		description: "压缩后保留的最近完整对话轮数",
	},
	"compression.state_compaction_ratio": {
		label:       "状态压缩触发比例",
		description: "状态模式触发历史压缩的上下文占用比例",
	},
	"task.default_context_mode": {
		label:       "worker上下文模式",
		description: "worker使用空上下文，或继承父会话历史",
	},
	"task.default_run_mode": {
		label:       "worker运行模式",
		description: "worker同步执行，或在后台运行",
	},
	"task.wait_timeout_ms": {
		label:       "worker等待超时",
		description: "等待worker结果的最长时间（毫秒）",
	},
	"ui.theme": {
		label:       "界面主题",
		description: "设置终端界面的配色主题",
	},
	"ui.context_limit_tokens": {
		label:       "上下文 Token 上限",
		description: "计算用量和触发压缩时使用的 Token 上限",
	},
	"ui.context_meter_location": {
		label:       "上下文用量显示位置",
		description: "设置上下文用量指示器的显示位置",
	},
	"ui.transcript_output_mode": {
		label:       "助手输出节奏",
		description: "按完整行或逐字显示已缓冲的助手回复",
	},
	"ui.translate_on_double_click": {
		label:       "双击翻译",
		description: "双击单词时是否调用翻译功能",
	},
	"context_maintenance.soft_compact_ratio": {
		label:       "温和压缩触发比例",
		description: "达到该比例后提示上下文压力",
	},
	"context_maintenance.tool_result_snip_ratio": {
		label:       "工具结果裁剪比例",
		description: "达到该比例后裁剪较大的工具结果",
	},
	"context_maintenance.compact_ratio": {
		label:       "常规压缩触发比例",
		description: "达到该比例后清理工具结果并允许摘要压缩",
	},
	"context_maintenance.compact_force_ratio": {
		label:       "强制压缩触发比例",
		description: "达到该比例后强制执行摘要压缩",
	},
	"context_maintenance.compact_target_ratio": {
		label:       "压缩目标比例",
		description: "摘要压缩后希望回落到的上下文占用比例",
	},
	"context_maintenance.tail_tokens": {
		label:       "尾部保留 Token",
		description: "压缩时优先原样保留的最近消息 Token 预算",
	},
	"context_maintenance.min_tool_result_bytes": {
		label:       "工具结果最小保留字节",
		description: "小于该字节数的工具结果不参与裁剪",
	},
	"context_maintenance.keep_errors": {
		label:       "保留错误信息",
		description: "裁剪或清理工具结果时保留错误信息",
	},
	"context_maintenance.keep_user_marked": {
		label:       "保留用户标记内容",
		description: "压缩时保留用户明确标记的重要内容",
	},
	"context_maintenance.archive_enabled": {
		label:       "启用压缩归档",
		description: "清理工具结果前将原始内容写入归档",
	},
}

// configGeneralLabel 把持久化 key 转成适合扫描的中文界面文案。
func configGeneralLabel(key string) string {
	if presentation, ok := configGeneralPresentations[key]; ok {
		return presentation.label
	}
	return key
}

func configGeneralDescription(key string) string {
	if presentation, ok := configGeneralPresentations[key]; ok {
		return presentation.description
	}
	return ""
}

// configGeneralDisplayValue 将枚举值翻译为用户可读文案；底层 get/set 仍使用
// 原始英文枚举，所以保存格式和既有配置文件完全兼容。
func configGeneralDisplayValue(key, value string) string {
	switch key + "\x00" + value {
	case "compression.mode\x00state":
		return "状态压缩"
	case "compression.mode\x00summary":
		return "摘要压缩"
	case "task.default_context_mode\x00empty":
		return "空上下文"
	case "task.default_context_mode\x00fork":
		return "继承父会话"
	case "task.default_run_mode\x00sync":
		return "同步"
	case "task.default_run_mode\x00background":
		return "后台"
	case "ui.transcript_output_mode\x00line":
		return "逐行"
	case "ui.transcript_output_mode\x00char":
		return "逐字"
	case "ui.context_meter_location\x00input-above":
		return "输入框上方"
	case "ui.context_meter_location\x00input-title":
		return "输入框标题"
	case "ui.context_meter_location\x00header":
		return "顶部"
	}
	if value == "true" {
		return "开启"
	}
	if value == "false" {
		return "关闭"
	}
	return value
}

func boolGeneralLabel(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

func formatGeneralFloat(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}

func parseGeneralInt(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	n, err := strconv.Atoi(raw)
	if err != nil {
		return "", err
	}
	return strconv.Itoa(n), nil
}

func parseGeneralFloat(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	f, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return "", err
	}
	return formatGeneralFloat(f), nil
}

// lookupConfigGeneralField 按 key 查 General 扁平字段描述符（内联编辑保存时用）。
func lookupConfigGeneralField(key string) *configGeneralField {
	fields := configGeneralFields()
	for i, f := range fields {
		if f.key == key {
			return &fields[i]
		}
	}
	return nil
}

// configGeneralRawValue 返回 General 行的底层当前值。settings 字段从
// settings.Config 读取；推理字段读取当前激活模型的 config-v2 parameters。
func (m appModel) configGeneralRawValue(field configGeneralField, cfg settings.Config) string {
	switch field.key {
	case configGeneralYoloKey:
		if m.configCenterController == nil {
			return "false"
		}
		return boolGeneralLabel(m.configCenterController.Snapshot().Document.Yolo)
	case configGeneralThinkingKey, configGeneralReasoningEffortKey:
		if m.configCenterController == nil {
			return "不可用"
		}
		snapshot := m.configCenterController.Snapshot()
		activeModel, ok := snapshot.Document.Models[snapshot.ActiveModelID]
		if !ok {
			return "不可用"
		}
		if field.key == configGeneralThinkingKey {
			return thinkingLabel(activeModel.Parameters)
		}
		return reasoningEffortLabel(activeModel.Parameters)
	default:
		if field.get == nil {
			return ""
		}
		return field.get(cfg)
	}
}

// configGeneralDisplayedFields 返回按搜索过滤后的 General 字段。中文名称、
// 中文说明、显示值和原始 key/value 都可搜索，便于熟悉旧配置名的用户定位。
func (m appModel) configGeneralDisplayedFields() []configGeneralField {
	all := configGeneralFields()
	q := strings.ToLower(strings.TrimSpace(m.configCenter.search))
	if q == "" {
		return all
	}
	cfg := m.currentSettings()
	out := make([]configGeneralField, 0, len(all))
	for _, f := range all {
		rawValue := m.configGeneralRawValue(f, cfg)
		searchText := strings.Join([]string{
			configGeneralLabel(f.key),
			configGeneralDescription(f.key),
			configGeneralDisplayValue(f.key, rawValue),
			f.key,
			rawValue,
		}, " ")
		if strings.Contains(strings.ToLower(searchText), q) {
			out = append(out, f)
		}
	}
	return out
}

// configCenterValueColumn 返回值列相对页面左缘的固定起点。宽屏上最多 52 列，
// 常见 100 列终端约为页面宽的 1/3；这会在值列右侧自然留下大块空白，而不是
// 像旧实现那样把每个值推到搜索框最右端。
func configCenterValueColumn(contentWidth int) int {
	if contentWidth <= 24 {
		return maxInt(8, contentWidth/2)
	}
	column := clampInt(contentWidth/3, 28, 52)
	if column > contentWidth-12 {
		column = maxInt(8, contentWidth-12)
	}
	return column
}

// formatGeneralRow 把名称和值放进固定双列。返回值只延伸到 value 末尾，选中
// 背景因此不会横贯整块页面；右侧留白仍保持终端背景色。
func formatGeneralRow(name, value string, contentWidth int) string {
	contentWidth = maxInt(1, contentWidth)
	valueColumn := configCenterValueColumn(contentWidth)
	nameBudget := maxInt(1, valueColumn-2)
	name = truncateStyledCellLine(name, nameBudget)
	value = truncateStyledCellLine(value, maxInt(1, contentWidth-valueColumn))
	gap := maxInt(2, valueColumn-terminalCellWidth(name))
	return name + strings.Repeat(" ", gap) + value
}

type configGeneralColumnLayout struct {
	valueStart       int
	descriptionStart int
	showDescription  bool
}

// configCenterGeneralColumns 为通用页计算固定三列起点。描述列优先获得剩余
// 空间；当终端窄到不足以容纳 16 个描述单元格时，降级为名称+值两列，避免
// 中文被挤成不可读的单字碎片。
func configCenterGeneralColumns(contentWidth int) configGeneralColumnLayout {
	contentWidth = maxInt(1, contentWidth)
	valueStart := configCenterValueColumn(contentWidth)
	descriptionStart := valueStart + clampInt(contentWidth/6, 14, 28)
	showDescription := contentWidth-descriptionStart >= 16
	return configGeneralColumnLayout{
		valueStart:       valueStart,
		descriptionStart: descriptionStart,
		showDescription:  showDescription,
	}
}

// formatConfigGeneralRow 把设置项、当前值和作用说明放进固定三列。所有宽度
// 计算都按终端 cell 而不是 UTF-8 字节，确保中文列起点真正对齐。
func formatConfigGeneralRow(name, value, description string, contentWidth int) string {
	contentWidth = maxInt(1, contentWidth)
	columns := configCenterGeneralColumns(contentWidth)
	name = truncateStyledCellLine(name, maxInt(1, columns.valueStart-2))
	nameGap := maxInt(2, columns.valueStart-terminalCellWidth(name))
	if !columns.showDescription {
		value = truncateStyledCellLine(value, maxInt(1, contentWidth-columns.valueStart))
		return name + strings.Repeat(" ", nameGap) + value
	}
	value = truncateStyledCellLine(value, maxInt(1, columns.descriptionStart-columns.valueStart-2))
	valueGap := maxInt(2, columns.descriptionStart-columns.valueStart-terminalCellWidth(value))
	description = truncateStyledCellLine(description, maxInt(1, contentWidth-columns.descriptionStart))
	return name + strings.Repeat(" ", nameGap) + value + strings.Repeat(" ", valueGap) + description
}

// advanceGeneralEdit 处理 General 扁平列表上 Enter：bool/enum 循环到下一个值
// 并立即保存+热应用；int/float 打开内联编辑（Enter 确认 Esc 取消）。
func (m *appModel) advanceGeneralEdit() {
	state := m.configCenter
	if state == nil {
		return
	}
	displayed := m.configGeneralDisplayedFields()
	if len(displayed) == 0 {
		return
	}
	selected := state.selected
	if selected < 0 || selected >= len(displayed) {
		selected = 0
		state.selected = selected
	}
	field := displayed[selected]
	if field.kind == configGeneralModelParameter {
		m.advanceGeneralModelParameter(field.key)
		return
	}
	if field.kind == configGeneralDocumentBoolean {
		current := m.configGeneralRawValue(field, m.currentSettings())
		next := current != "true"
		m.applyConfigOperations(configv2.SetYolo(next))
		if state.err != "" {
			return
		}
		if setter, ok := m.runner.(interface {
			SetYoloMode(bool) (bool, error)
		}); ok {
			if _, err := setter.SetYoloMode(next); err != nil {
				state.err = err.Error()
			}
		}
		return
	}
	cfg := m.currentSettings()
	switch field.kind {
	case configGeneralEnum:
		current := field.get(cfg)
		opts := field.options
		idx := 0
		for i, opt := range opts {
			if opt == current {
				idx = i
				break
			}
		}
		if len(opts) == 0 {
			return
		}
		next := opts[(idx+1)%len(opts)]
		field.set(&cfg, next)
		m.saveAndApplyGeneral(cfg)
	case configGeneralInt:
		state.targetID = field.key
		m.openConfigEdit(configEditGeneralInt, field.get(cfg), field.key)
	case configGeneralFloat:
		state.targetID = field.key
		m.openConfigEdit(configEditGeneralFloat, field.get(cfg), field.key)
	}
}

func (m *appModel) advanceGeneralModelParameter(key string) {
	state := m.configCenter
	if state == nil || m.configCenterController == nil {
		return
	}
	snapshot := m.configCenterController.Snapshot()
	activeModel, ok := snapshot.Document.Models[snapshot.ActiveModelID]
	if !ok {
		state.err = "当前模型不存在，无法修改推理设置"
		return
	}
	if activeModel.Parameters == nil {
		activeModel.Parameters = map[string]any{}
	}
	switch key {
	case configGeneralThinkingKey:
		activeModel.Parameters["thinking"] = nextThinkingConfig(activeModel.Parameters)
	case configGeneralReasoningEffortKey:
		activeModel.Parameters["reasoning_effort"] = nextReasoningEffort(activeModel.Parameters)
	default:
		return
	}
	m.applyConfigOperations(configv2.UpsertModel(snapshot.ActiveModelID, activeModel))
}

// saveAndApplyGeneral 持久化 General 字段改动并热应用到 runner。保存失败置
// state.err；成功清错并通过 syncRunnerSettings 按字段调对应 setter。
func (m *appModel) saveAndApplyGeneral(cfg settings.Config) {
	state := m.configCenter
	if state == nil {
		return
	}
	if m.settingsConfig != nil {
		if err := m.settingsConfig.SaveSettings(cfg); err != nil {
			state.err = err.Error()
			return
		}
	}
	state.err = ""
	m.syncRunnerSettings(cfg)
}

// renderConfigCenterGeneral 渲染“设置项 / 当前值 / 作用说明”三列列表。
// maxRows 用于小终端的选择跟随滚动，确保底部 hint 始终留在屏幕上。
func (m appModel) renderConfigCenterGeneral(contentWidth, maxRows int) []string {
	state := m.configCenter
	cfg := m.currentSettings()
	displayed := m.configGeneralDisplayedFields()
	selected := state.selected
	if selected < 0 || selected >= len(displayed) {
		selected = 0
	}
	if len(displayed) == 0 {
		return []string{unselectedProviderStyle.Render("（没有匹配的设置）")}
	}
	showHeader := maxRows >= 2
	dataRows := maxRows
	if showHeader {
		dataRows--
	}
	dataRows = clampInt(dataRows, 1, len(displayed))
	start := maxInt(0, selected-dataRows+1)
	end := minInt(len(displayed), start+dataRows)
	rows := make([]string, 0, end-start+1)
	if showHeader {
		header := formatConfigGeneralRow("设置项", "当前值", "作用说明", contentWidth)
		rows = append(rows, m.styles.StatusMuted.Copy().Bold(true).Render(header))
	}
	for i := start; i < end; i++ {
		f := displayed[i]
		rawValue := m.configGeneralRawValue(f, cfg)
		row := formatConfigGeneralRow(
			configGeneralLabel(f.key),
			configGeneralDisplayValue(f.key, rawValue),
			configGeneralDescription(f.key),
			contentWidth,
		)
		if i == selected {
			row = m.styles.SelectionSelected.Render(row)
		} else {
			row = m.styles.Body.Copy().Bold(true).Render(row)
		}
		rows = append(rows, row)
	}
	return rows
}
