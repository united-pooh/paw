package bubble

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"paw/internal/settings"
)

// openGeneralCenter 打开配置中心并落到 General 扁平列表（首页第 0 项）。
func openGeneralCenter(t *testing.T) (appModel, *fakeSettingsController, *fakeRunner) {
	t.Helper()
	controller, _ := newConfigCenterHarness(t)
	settingsController := &fakeSettingsController{current: settings.DefaultConfig()}
	runner := &fakeRunner{}
	model := newModel(context.Background(), runner, "session", controller, settingsController, nil, nil, newTerminalCursorAnchor())
	model.configCenterController = controller
	model.ready = true
	model.width = 101
	model.height = 36
	model.relayout()
	model.openConfigCenter()
	model.configCenter.page = configCenterGeneral
	model.configCenter.selected = 0
	return model, settingsController, runner
}

// press 把一个按键送进配置中心并返回更新后的 appModel（丢弃 cmd；General
// 编辑路径的 cmd 仅是 Saved notice 到期，断言里不依赖）。
func press(m appModel, msg tea.KeyMsg) appModel {
	next, _ := m.handleConfigCenterKey(msg)
	return next.(appModel)
}

func generalFieldIndex(t *testing.T, model appModel, key string) int {
	t.Helper()
	for index, field := range model.configGeneralDisplayedFields() {
		if field.key == key {
			return index
		}
	}
	t.Fatalf("General field %q not found", key)
	return -1
}

func TestConfigCenterGeneralFlatListShowsAllFields(t *testing.T) {
	model, _, _ := openGeneralCenter(t)
	rendered := ansi.Strip(model.renderConfigCenterBox())
	for _, label := range []string{
		"YOLO 模式", "推理开关", "推理强度",
		"压缩模式", "保留最近对话轮数", "状态压缩触发比例",
		"worker上下文模式", "worker运行模式", "worker等待超时",
		"界面主题", "上下文 Token 上限", "上下文用量显示位置", "助手输出节奏", "双击翻译",
		"温和压缩触发比例", "工具结果裁剪比例", "常规压缩触发比例", "强制压缩触发比例",
		"压缩目标比例", "尾部保留 Token", "工具结果最小保留字节", "保留错误信息",
		"保留用户标记内容", "启用压缩归档",
	} {
		if !strings.Contains(rendered, label) {
			t.Fatalf("General list missing %q:\n%s", label, rendered)
		}
	}
	if strings.Contains(rendered, "context_maintenance.") {
		t.Fatalf("internal dotted keys leaked into the visual list:\n%s", rendered)
	}
	for _, description := range []string{
		"选择状态快照压缩或 LLM 摘要压缩",
		"worker同步执行，或在后台运行",
		"清理工具结果前将原始内容写入归档",
	} {
		if !strings.Contains(rendered, description) {
			t.Fatalf("General list missing description %q:\n%s", description, rendered)
		}
	}
	if !strings.Contains(rendered, "状态压缩") {
		t.Fatal("localized default compression mode value missing")
	}
}

func TestConfigCenterGeneralFlatListUsesFixedThreeColumns(t *testing.T) {
	model, _, _ := openGeneralCenter(t)
	model.configCenter.selected = 0
	rendered := ansi.Strip(model.renderConfigCenterBox())
	columns := configCenterGeneralColumns(model.fullscreenContentWidth())
	wantValueColumn := model.fullscreenHorizontalMargin() + columns.valueStart
	wantDescriptionColumn := model.fullscreenHorizontalMargin() + columns.descriptionStart
	if !columns.showDescription {
		t.Fatal("101-column terminal unexpectedly hid the description column")
	}
	for _, item := range []struct {
		label       string
		value       string
		description string
	}{
		{label: "压缩模式", value: "状态压缩", description: "选择状态快照压缩或 LLM 摘要压缩"},
		{label: "保留最近对话轮数", value: "3", description: "压缩后保留的最近完整对话轮数"},
		{label: "界面主题", value: "default", description: "设置终端界面的配色主题"},
	} {
		var row string
		for _, line := range strings.Split(rendered, "\n") {
			if strings.Contains(line, item.label) {
				row = line
				break
			}
		}
		if row == "" {
			t.Fatalf("row %q not found:\n%s", item.label, rendered)
		}
		valueByte := strings.Index(row, item.value)
		if valueByte < 0 {
			t.Fatalf("%q value %q not found: %q", item.label, item.value, row)
		}
		if got := terminalCellWidth(row[:valueByte]); got != wantValueColumn {
			t.Fatalf("%q value column = %d, want %d: %q", item.label, got, wantValueColumn, row)
		}
		descriptionByte := strings.Index(row, item.description)
		if descriptionByte < 0 {
			t.Fatalf("%q description %q not found: %q", item.label, item.description, row)
		}
		if got := terminalCellWidth(row[:descriptionByte]); got != wantDescriptionColumn {
			t.Fatalf("%q description column = %d, want %d: %q", item.label, got, wantDescriptionColumn, row)
		}
	}
}

func TestConfigCenterGeneralColumnLayoutStaysWithinTerminalWidth(t *testing.T) {
	for _, width := range []int{42, 60, 80, 120, 180} {
		row := formatConfigGeneralRow(
			"工具结果最小保留字节",
			"1024",
			"小于该字节数的工具结果不参与裁剪",
			width,
		)
		if got := terminalCellWidth(row); got > width {
			t.Fatalf("width=%d rendered row width=%d: %q", width, got, row)
		}
	}
	if configCenterGeneralColumns(42).showDescription {
		t.Fatal("42-column layout should fall back to two readable columns")
	}
	if !configCenterGeneralColumns(80).showDescription {
		t.Fatal("80-column layout should retain all three columns")
	}
}

func TestConfigCenterGeneralSearchFiltersAndEscClears(t *testing.T) {
	model, _, _ := openGeneralCenter(t)
	model.configCenter.search = "强制"
	rendered := ansi.Strip(model.renderConfigCenterBox())
	if !strings.Contains(rendered, "强制压缩触发比例") {
		t.Fatalf("filtered list missing Chinese match:\n%s", rendered)
	}
	for _, label := range []string{"压缩模式", "界面主题", "worker等待超时"} {
		if strings.Contains(rendered, label) {
			t.Fatalf("filtered list should not contain %q:\n%s", label, rendered)
		}
	}
	model = press(model, tea.KeyMsg{Type: tea.KeyEsc})
	if model.configCenter.search != "" {
		t.Fatalf("Esc did not clear search: %q", model.configCenter.search)
	}
	rendered = ansi.Strip(model.renderConfigCenterBox())
	if !strings.Contains(rendered, "压缩模式") {
		t.Fatalf("search cleared but list not restored:\n%s", rendered)
	}
}

func TestConfigCenterNonGeneralSearchPreservesActionIndex(t *testing.T) {
	model, _, _ := openGeneralCenter(t)
	model.configCenter.page = configCenterProviders
	model.configCenter.selected = 0
	model = press(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("添加服务商")})

	// Providers 有一个已配置 provider，Add provider 是原始下标 1。过滤后仍
	// 必须保存这个原始下标，Enter 才不会误打开第一个 provider。
	if got := model.configCenter.selected; got != 1 {
		t.Fatalf("filtered selection index = %d, want original index 1", got)
	}
	rendered := ansi.Strip(model.renderConfigCenterBox())
	if !strings.Contains(rendered, "添加服务商") || strings.Contains(rendered, "openai-compatible") {
		t.Fatalf("provider search did not filter options:\n%s", rendered)
	}
	model = press(model, tea.KeyMsg{Type: tea.KeyEnter})
	if model.configCenter.page != configCenterAddProvider {
		t.Fatalf("filtered Enter opened page %v, want Add provider", model.configCenter.page)
	}
}

func TestConfigCenterHintBarRenderedPerTab(t *testing.T) {
	model, _, _ := openGeneralCenter(t)
	general := ansi.Strip(model.renderConfigCenterBox())
	if !strings.Contains(general, "输入筛选 · Enter 编辑 · ↑/↓ 选择 · Tab/←/→ 切换 · Esc 清除/关闭") {
		t.Fatalf("General hint missing:\n%s", general)
	}
	// 非 General tab 用 Enter 选择变体。
	model.configCenter.page = configCenterProviders
	providers := ansi.Strip(model.renderConfigCenterBox())
	if !strings.Contains(providers, "Enter 选择") {
		t.Fatalf("non-General hint missing Enter 选择:\n%s", providers)
	}
}

func TestConfigCenterTabSwitchCyclesTabs(t *testing.T) {
	model, _, _ := openGeneralCenter(t)
	if got := model.configCenter.page; got != configCenterGeneral {
		t.Fatalf("initial page = %v, want General", got)
	}
	model = press(model, tea.KeyMsg{Type: tea.KeyTab})
	if model.configCenter.page != configCenterProviders {
		t.Fatalf("after Tab page = %v, want Providers", model.configCenter.page)
	}
	model = press(model, tea.KeyMsg{Type: tea.KeyRight})
	if model.configCenter.page != configCenterModels {
		t.Fatalf("after Right page = %v, want Models", model.configCenter.page)
	}
	model = press(model, tea.KeyMsg{Type: tea.KeyRight})
	if model.configCenter.page != configCenterCredentials {
		t.Fatalf("after Models page = %v, want Credentials", model.configCenter.page)
	}
	model = press(model, tea.KeyMsg{Type: tea.KeyLeft})
	if model.configCenter.page != configCenterModels {
		t.Fatalf("after Left page = %v, want Models", model.configCenter.page)
	}
}

func TestConfigCenterGeneralYoloPersistsToJSONCAndHotApplies(t *testing.T) {
	model, settingsController, runner := openGeneralCenter(t)
	model.configCenter.selected = generalFieldIndex(t, model, configGeneralYoloKey)
	model = press(model, tea.KeyMsg{Type: tea.KeyEnter})

	snapshot := model.configCenterController.Snapshot()
	if !snapshot.Document.Yolo || !strings.Contains(string(snapshot.Raw), `"yolo": true`) {
		t.Fatalf("YOLO was not persisted to config.jsonc: document=%#v raw=%s", snapshot.Document, snapshot.Raw)
	}
	if len(runner.yoloModes) != 1 || !runner.yoloModes[0] {
		t.Fatalf("runner YOLO calls = %#v, want [true]", runner.yoloModes)
	}
	if len(settingsController.saved) != 0 {
		t.Fatalf("YOLO was incorrectly persisted to settings.json: %#v", settingsController.saved)
	}

	model = press(model, tea.KeyMsg{Type: tea.KeyEnter})
	if model.configCenterController.Snapshot().Document.Yolo {
		t.Fatal("second activation did not disable YOLO")
	}
	if len(runner.yoloModes) != 2 || runner.yoloModes[1] {
		t.Fatalf("runner YOLO calls = %#v, want [true false]", runner.yoloModes)
	}
}

func TestConfigCenterGeneralReasoningControlsUpdateCurrentModel(t *testing.T) {
	model, settingsController, _ := openGeneralCenter(t)

	model.configCenter.selected = generalFieldIndex(t, model, configGeneralThinkingKey)
	model = press(model, tea.KeyMsg{Type: tea.KeyEnter})
	configured := model.configCenterController.Snapshot().Document.Models["local/one"]
	if got := thinkingLabel(configured.Parameters); got != "关闭" {
		t.Fatalf("thinking=%q parameters=%#v", got, configured.Parameters)
	}

	model.configCenter.selected = generalFieldIndex(t, model, configGeneralReasoningEffortKey)
	for _, want := range []string{"low", "medium", "high", "xhigh", "max", "low"} {
		model = press(model, tea.KeyMsg{Type: tea.KeyEnter})
		configured = model.configCenterController.Snapshot().Document.Models["local/one"]
		if got := reasoningEffortLabel(configured.Parameters); got != want {
			t.Fatalf("reasoning effort=%q, want %q parameters=%#v", got, want, configured.Parameters)
		}
	}
	if model.configCenter.page != configCenterGeneral {
		t.Fatalf("reasoning control left General tab: %#v", model.configCenter)
	}
	if len(settingsController.saved) != 0 {
		t.Fatalf("model parameters were incorrectly persisted to settings.json: %#v", settingsController.saved)
	}
}

func TestConfigCenterGeneralEditCompressionHotToggle(t *testing.T) {
	model, settingsController, runner := openGeneralCenter(t)
	model.configCenter.selected = generalFieldIndex(t, model, "compression.mode")
	model = press(model, tea.KeyMsg{Type: tea.KeyEnter})
	if len(settingsController.saved) != 1 {
		t.Fatalf("saved settings = %#v", settingsController.saved)
	}
	if got := settingsController.saved[0].ContextCompression.Mode; got != settings.CompressionModeSummary {
		t.Fatalf("saved compression mode = %v, want summary", got)
	}
	if len(runner.contextModes) != 1 || runner.contextModes[0] != "summary" {
		t.Fatalf("runner.SetContextMode calls = %#v, want [summary]", runner.contextModes)
	}
}

func TestConfigCenterGeneralEditBoolToggle(t *testing.T) {
	model, settingsController, _ := openGeneralCenter(t)
	model.configCenter.selected = generalFieldIndex(t, model, "ui.translate_on_double_click")
	model = press(model, tea.KeyMsg{Type: tea.KeyEnter})
	if len(settingsController.saved) != 1 {
		t.Fatalf("saved settings = %#v", settingsController.saved)
	}
	if !settingsController.saved[0].UI.TranslateOnDoubleClick {
		t.Fatalf("translate did not toggle to true: %#v", settingsController.saved[0].UI)
	}
}

func TestConfigCenterGeneralEditIntInline(t *testing.T) {
	model, settingsController, _ := openGeneralCenter(t)
	model.configCenter.selected = generalFieldIndex(t, model, "compression.resume_recent_turns")
	model = press(model, tea.KeyMsg{Type: tea.KeyEnter})
	if model.configCenter.page != configCenterEdit || model.configCenter.editKind != configEditGeneralInt {
		t.Fatalf("int edit did not open: %#v", model.configCenter)
	}
	if got := model.configCenter.editValue; got != "3" {
		t.Fatalf("edit initial value = %q, want 3", got)
	}
	model.configCenter.editValue = ""
	model = press(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("5")})
	model = press(model, tea.KeyMsg{Type: tea.KeyEnter})
	if len(settingsController.saved) != 1 {
		t.Fatalf("saved settings = %#v", settingsController.saved)
	}
	if got := settingsController.saved[0].ContextCompression.ResumeRecentTurns; got != 5 {
		t.Fatalf("resume_recent_turns = %d, want 5", got)
	}
	if model.configCenter.page != configCenterGeneral {
		t.Fatalf("after int edit confirm page = %v, want General", model.configCenter.page)
	}
}

func TestConfigCenterGeneralEditFloatInline(t *testing.T) {
	model, settingsController, _ := openGeneralCenter(t)
	model.configCenter.selected = generalFieldIndex(t, model, "compression.state_compaction_ratio")
	model = press(model, tea.KeyMsg{Type: tea.KeyEnter})
	if model.configCenter.editKind != configEditGeneralFloat {
		t.Fatalf("float edit did not open: %#v", model.configCenter)
	}
	model.configCenter.editValue = ""
	model = press(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("0.85")})
	model = press(model, tea.KeyMsg{Type: tea.KeyEnter})
	if len(settingsController.saved) != 1 {
		t.Fatalf("saved settings = %#v", settingsController.saved)
	}
	if got := settingsController.saved[0].ContextCompression.StateCompactionRatio; got != 0.85 {
		t.Fatalf("state_compaction_ratio = %v, want 0.85", got)
	}
}
