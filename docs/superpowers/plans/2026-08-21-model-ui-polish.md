# 模型向导确认面板与切换输出美化 实现计划

> **面向 AI 代理的工作者：** 必需子技能：使用 superpowers:subagent-driven-development（推荐）或 superpowers:executing-plans 逐任务实现此计划。步骤使用复选框（`- [ ]`）语法来跟踪进度。

**目标：** 按 docs/spec-model-ui-polish.md 实现「主角式」Confirm model 面板与 `<model>` 结构化块的圆角绿框状态卡渲染。

**架构：** 新建 `internal/ui/bubble/model_card.go` 承载块格式（formatModelSwitchBlock）、解析（parseModelCardBlock）与卡片渲染（renderModelSwitchCard），仿照既有 `<task>` 完成块模式；`renderEntryAt` 加一个检测分支；四处切换成功调用点统一改用块格式；`renderModelConfirmStep` 重写为主角式布局。

**技术栈：** Go + charmbracelet/lipgloss + bubbletea（仓库现有），测试用标准库 testing + `github.com/charmbracelet/x/ansi`。

**规格：** docs/spec-model-ui-polish.md（Approved，commit b809877）

---

## 文件结构

| 文件 | 动作 | 职责 |
|---|---|---|
| `internal/ui/bubble/model_card.go` | 创建 | `<model>` 块的转义/生成/检测/解析 + 圆角状态卡渲染 |
| `internal/ui/bubble/model_card_test.go` | 创建 | 块格式、解析、卡片渲染、transcript 集成的单测 |
| `internal/ui/bubble/model_wizard_confirm_test.go` | 创建 | 确认面板布局单测 |
| `internal/ui/bubble/model_wizard.go` | 修改 | 重写 `renderModelConfirmStep`（约 :373-395）；两处 apply body 改块格式（:244-248、:274-278）；import 加 `strconv` |
| `internal/ui/bubble/command_helpers.go` | 修改 | 两处切换成功 body 改块格式（:56-59、:100-104） |
| `internal/ui/bubble/transcript.go` | 修改 | `renderEntryAt` 在 `<task>` 卡片分支后加 `<model>` 卡片分支（约 :2316 之后） |
| `internal/ui/bubble/bubble_test.go` | 修改 | `TestModelCommandVariantsKeepWizardAndShortcuts` 追加切换卡断言 |

不动：`/model status`（command_helpers.go:43，规格 N1）；entryError 失败路径；颜色主题。

---

### 任务 1：`<model>` 块格式与解析

**文件：**
- 创建：`internal/ui/bubble/model_card.go`
- 创建：`internal/ui/bubble/model_card_test.go`

- [ ] **步骤 1：编写失败的测试**

写入 `internal/ui/bubble/model_card_test.go`：

```go
package bubble

import (
	"strings"
	"testing"

	"paw/internal/model"
)

func TestFormatModelSwitchBlockOmitsEmptyAttrsAndKeepsOrder(t *testing.T) {
	cfg := model.Config{
		Provider:            "openrouter",
		Model:               "stealth/ox-alpha",
		APIBaseURL:          "https://openrouter.ai/api/v1",
		ContextLimitTokens:  131072,
		RetryCount:          3,
		APIKeyEnvName:       "OPENROUTER_API_KEY",
	}
	body := formatModelSwitchBlock(cfg)
	if !isModelCardBlock(body) {
		t.Fatalf("generated body not detected as model card block:\n%s", body)
	}
	wantOrder := []string{
		`provider="openrouter"`,
		`model="stealth/ox-alpha"`,
		`base="https://openrouter.ai/api/v1"`,
		`context="131072"`,
		`retries="3"`,
		`key_env="OPENROUTER_API_KEY"`,
	}
	position := 0
	for _, want := range wantOrder {
		at := strings.Index(body[position:], want)
		if at < 0 {
			t.Fatalf("attr %s missing or out of order in:\n%s", want, body)
		}
		position += at + len(want)
	}
	if strings.Contains(body, `path=`) {
		t.Fatalf("empty path attr should be omitted:\n%s", body)
	}
}

func TestFormatModelSwitchBlockAlwaysWritesRetries(t *testing.T) {
	body := formatModelSwitchBlock(model.Config{Provider: "p1", Model: "m1"})
	if !strings.Contains(body, `retries="0"`) {
		t.Fatalf("retries=0 should still be written:\n%s", body)
	}
}

func TestModelCardBlockEscapeRoundTrip(t *testing.T) {
	cfg := model.Config{Provider: `a&b"c<d>`, Model: "m&1"}
	body := formatModelSwitchBlock(cfg)
	info, ok := parseModelCardBlock(body)
	if !ok {
		t.Fatalf("parse failed:\n%s", body)
	}
	if info.Provider != `a&b"c<d>` || info.Model != "m&1" {
		t.Fatalf("round trip mismatch: %#v", info)
	}
}

func TestIsModelCardBlockBoundaries(t *testing.T) {
	block := "<model provider=\"p\">\n</model>"
	cases := map[string]bool{
		block:                 true,
		"  \n" + block + "  ": true,
		"<modelx provider=\"p\">": false,
		"plain key=value":         false,
		"<model provider=\"p\">":  false,
	}
	for input, want := range cases {
		if got := isModelCardBlock(input); got != want {
			t.Fatalf("isModelCardBlock(%q) = %v, want %v", input, got, want)
		}
	}
}
```

- [ ] **步骤 2：运行测试验证失败**

运行：`go test ./internal/ui/bubble -run 'TestFormatModelSwitchBlock|TestModelCardBlock|TestIsModelCardBlock' -v`
预期：编译失败，报 `undefined: formatModelSwitchBlock` 等。

- [ ] **步骤 3：编写实现**

创建 `internal/ui/bubble/model_card.go`：

```go
package bubble

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"paw/internal/model"
)

// escapeTaskBlockAttrValue 转义结构化块属性值，与 unescapeTaskBlockAttr 对偶。
func escapeTaskBlockAttrValue(value string) string {
	return strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
	).Replace(value)
}

// formatModelSwitchBlock 生成模型切换成功条目的结构化块。
// 属性固定顺序，空值整体省略；retries 恒输出（0 也是有效配置）。
func formatModelSwitchBlock(cfg model.Config) string {
	type attr struct{ key, value string }
	pairs := []attr{
		{"provider", strings.TrimSpace(cfg.Provider)},
		{"model", strings.TrimSpace(cfg.Model)},
		{"base", strings.TrimSpace(cfg.APIBaseURL)},
		{"path", strings.TrimSpace(cfg.APIPath)},
	}
	if limit := model.EffectiveContextLimitTokens(cfg); limit > 0 {
		pairs = append(pairs, attr{"context", strconv.Itoa(limit)})
	}
	pairs = append(pairs, attr{"retries", strconv.Itoa(cfg.RetryCount)})
	if env := strings.TrimSpace(cfg.APIKeyEnvName); env != "" {
		pairs = append(pairs, attr{"key_env", env})
	}
	rendered := make([]string, 0, len(pairs))
	for _, pair := range pairs {
		if pair.value == "" {
			continue
		}
		rendered = append(rendered, fmt.Sprintf(`%s="%s"`, pair.key, escapeTaskBlockAttrValue(pair.value)))
	}
	return "<model " + strings.Join(rendered, " ") + ">\n</model>"
}

type modelCardInfo struct {
	Provider string
	Model    string
	Base     string
	Path     string
	Context  int
	Retries  int
	KeyEnv   string
}

// isModelCardBlock 检测 <model> 切换块，模式与 isTaskCompletionBlock 一致。
func isModelCardBlock(body string) bool {
	trimmed := strings.TrimSpace(body)
	return strings.HasPrefix(trimmed, "<model ") && strings.HasSuffix(trimmed, "</model>")
}

// parseModelCardBlock 解析 <model> 块头部属性，复用 task 块的属性正则与反转义。
func parseModelCardBlock(body string) (modelCardInfo, bool) {
	trimmed := strings.TrimSpace(body)
	if !isModelCardBlock(trimmed) {
		return modelCardInfo{}, false
	}
	headerEnd := strings.IndexByte(trimmed, '>')
	if headerEnd < 0 {
		return modelCardInfo{}, false
	}
	info := modelCardInfo{}
	for _, match := range taskBlockAttrPattern.FindAllStringSubmatch(trimmed[:headerEnd], -1) {
		value := unescapeTaskBlockAttr(match[2])
		switch match[1] {
		case "provider":
			info.Provider = value
		case "model":
			info.Model = value
		case "base":
			info.Base = value
		case "path":
			info.Path = value
		case "context":
			info.Context, _ = strconv.Atoi(value)
		case "retries":
			info.Retries, _ = strconv.Atoi(value)
		case "key_env":
			info.KeyEnv = value
		}
	}
	return info, true
}
```

- [ ] **步骤 4：运行测试验证通过**

运行：`go test ./internal/ui/bubble -run 'TestFormatModelSwitchBlock|TestModelCardBlock|TestIsModelCardBlock' -v`
预期：全部 PASS。

- [ ] **步骤 5：Commit**

```bash
git add -f internal/ui/bubble/model_card.go internal/ui/bubble/model_card_test.go
git commit -m "feat(ui): <model> 切换块格式与解析（空值属性省略）"
```

---

### 任务 2：renderModelSwitchCard 圆角状态卡

**文件：**
- 修改：`internal/ui/bubble/model_card.go`（追加）
- 修改：`internal/ui/bubble/model_card_test.go`（追加）

- [ ] **步骤 1：编写失败的测试**

追加到 `internal/ui/bubble/model_card_test.go`（import 区补 `"github.com/charmbracelet/lipgloss"` 与 `"github.com/charmbracelet/x/ansi"`）：

```go
func TestRenderModelSwitchCardLayout(t *testing.T) {
	cfg := model.Config{
		Provider:           "openrouter",
		Model:              "stealth/ox-alpha",
		APIBaseURL:         "https://openrouter.ai/api/v1",
		ContextLimitTokens: 131072,
		RetryCount:         3,
		APIKeyEnvName:      "OPENROUTER_API_KEY",
	}
	rendered := renderModelSwitchCard(formatModelSwitchBlock(cfg), 60)
	plain := ansi.Strip(rendered)
	for _, want := range []string{
		"✓ 模型已生效",
		"stealth/ox-alpha",
		"openrouter · 131072 ctx · retry ×3",
		"base",
		"https://openrouter.ai/api/v1",
		"key env",
		"OPENROUTER_API_KEY",
	} {
		if !strings.Contains(plain, want) {
			t.Fatalf("card missing %q:\n%s", want, plain)
		}
	}
	for _, line := range strings.Split(rendered, "\n") {
		if lipgloss.Width(line) > 60 {
			t.Fatalf("card line exceeds width 60: %q", line)
		}
	}
}

func TestRenderModelSwitchCardHidesEmptyDetails(t *testing.T) {
	body := formatModelSwitchBlock(model.Config{Provider: "p1", Model: "m1"})
	plain := ansi.Strip(renderModelSwitchCard(body, 40))
	for _, banned := range []string{"base", "path", "key env"} {
		if strings.Contains(plain, banned) {
			t.Fatalf("empty detail %q leaked:\n%s", banned, plain)
		}
	}
	if !strings.Contains(plain, "✓ 模型已生效") || !strings.Contains(plain, "m1") {
		t.Fatalf("card missing essentials:\n%s", plain)
	}
}

func TestRenderModelSwitchCardFallsBackOnPlainBody(t *testing.T) {
	rendered := renderModelSwitchCard("plain text", 40)
	if !strings.Contains(ansi.Strip(rendered), "plain text") {
		t.Fatalf("fallback lost body: %q", rendered)
	}
}
```

- [ ] **步骤 2：运行测试验证失败**

运行：`go test ./internal/ui/bubble -run TestRenderModelSwitchCard -v`
预期：编译失败，报 `undefined: renderModelSwitchCard`。

- [ ] **步骤 3：编写实现**

追加到 `internal/ui/bubble/model_card.go`：

```go
// renderModelSwitchCard 把 <model> 切换块渲染为绿框状态卡，
// 边框与宽度算法对齐 renderTaskCompletionCard。
func renderModelSwitchCard(body string, width int) string {
	info, ok := parseModelCardBlock(body)
	if !ok {
		return bodyStyle.Width(width).Render(sanitizeTerminalText(body))
	}
	borderColor := colorManager.LipglossColor(colorWorktreeClean)
	titleStyle := lipgloss.NewStyle().Foreground(borderColor).Bold(true)
	nameStyle := lipgloss.NewStyle().Bold(true)
	mutedStyle := lipgloss.NewStyle().Foreground(colorManager.LipglossColor(colorContextFree))

	lines := []string{titleStyle.Render("✓ 模型已生效"), ""}
	name := strings.TrimSpace(info.Model)
	if name == "" {
		name = "unknown"
	}
	lines = append(lines, nameStyle.Render(name))

	metaParts := make([]string, 0, 3)
	if info.Provider != "" {
		metaParts = append(metaParts, info.Provider)
	}
	if info.Context > 0 {
		metaParts = append(metaParts, fmt.Sprintf("%d ctx", info.Context))
	}
	if info.Retries > 0 {
		metaParts = append(metaParts, fmt.Sprintf("retry ×%d", info.Retries))
	}
	if len(metaParts) > 0 {
		lines = append(lines, mutedStyle.Render(strings.Join(metaParts, " · ")))
	}

	type detailRow struct{ label, value string }
	details := make([]detailRow, 0, 3)
	if info.Base != "" {
		details = append(details, detailRow{"base", info.Base})
	}
	if info.Path != "" {
		details = append(details, detailRow{"path", info.Path})
	}
	if info.KeyEnv != "" {
		details = append(details, detailRow{"key env", info.KeyEnv})
	}
	if len(details) > 0 {
		lines = append(lines, "")
		labelWidth := 0
		for _, r := range details {
			labelWidth = maxInt(labelWidth, len(r.label))
		}
		labelWidth += 2
		for _, r := range details {
			lines = append(lines, mutedStyle.Render(fmt.Sprintf("%-*s", labelWidth, r.label))+r.value)
		}
	}

	cardStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor).
		Padding(0, 1)
	horizontalBorder := cardStyle.GetHorizontalBorderSize()
	styleWidth := maxInt(1, width-horizontalBorder)
	bodyWidth := maxInt(1, styleWidth-cardStyle.GetHorizontalPadding())
	content := make([]string, 0, len(lines))
	for _, line := range lines {
		content = append(content, fitStyledCellLine(truncateStyledCellLine(line, bodyWidth), bodyWidth))
	}
	return cardStyle.Width(styleWidth).Render(strings.Join(content, "\n"))
}
```

- [ ] **步骤 4：运行测试验证通过**

运行：`go test ./internal/ui/bubble -run TestRenderModelSwitchCard -v`
预期：全部 PASS。

- [ ] **步骤 5：Commit**

```bash
git add -f internal/ui/bubble/model_card.go internal/ui/bubble/model_card_test.go
git commit -m "feat(ui): 模型切换圆角状态卡渲染（对齐任务完成卡视觉）"
```

---

### 任务 3：transcript 集成分支

**文件：**
- 修改：`internal/ui/bubble/transcript.go`（`renderEntryAt` 内，`<task>` 卡片分支之后）
- 修改：`internal/ui/bubble/model_card_test.go`（追加集成测试）

- [ ] **步骤 1：编写失败的测试**

追加到 `internal/ui/bubble/model_card_test.go`：

```go
func TestRenderEntryRendersModelCardWithoutLabel(t *testing.T) {
	body := formatModelSwitchBlock(model.Config{Provider: "p1", Model: "m1"})
	entry := transcriptEntry{kind: entrySystem, title: "model", body: body}
	rendered := ansi.Strip(renderEntry(entry, 80))
	trimmed := strings.TrimLeft(rendered, " ")
	if !strings.HasPrefix(trimmed, "╭") {
		t.Fatalf("model card entry should start with rounded border:\n%s", rendered)
	}
	if !strings.Contains(rendered, "✓ 模型已生效") {
		t.Fatalf("missing card title:\n%s", rendered)
	}
	if strings.Contains(rendered, "\nmodel\n") {
		t.Fatalf("legacy model label should be replaced by card title:\n%s", rendered)
	}
}
```

- [ ] **步骤 2：运行测试验证失败**

运行：`go test ./internal/ui/bubble -run TestRenderEntryRendersModelCard -v`
预期：FAIL——当前走普通 system 渲染，输出以 `model` 标签开头而非 `╭`。

- [ ] **步骤 3：编写实现**

在 `internal/ui/bubble/transcript.go` 的 `renderEntryAt` 中，紧接现有 `<task>` 分支之后插入：

```go
	// 结构化 <model> 切换块：整块渲染为绿框状态卡，不显示 "model" 标签
	// （卡片标题自带 ✓ 状态标记），与 <task> 完成卡同一套视觉语言。
	if entry.kind == entrySystem && isModelCardBlock(entry.body) {
		card := renderModelSwitchCard(entry.body, bodyWidth)
		if card == "" {
			return ""
		}
		return indentLines(card, transcriptEntryGutter)
	}
```

- [ ] **步骤 4：运行测试验证通过**

运行：`go test ./internal/ui/bubble -run TestRenderEntryRendersModelCard -v`
预期：PASS。

- [ ] **步骤 5：Commit**

```bash
git add -f internal/ui/bubble/transcript.go internal/ui/bubble/model_card_test.go
git commit -m "feat(ui): transcript 识别 <model> 块并渲染为状态卡"
```

---

### 任务 4：四处调用点统一切换为块格式

**文件：**
- 修改：`internal/ui/bubble/model_wizard.go:244-248`（catalog 向导应用）
- 修改：`internal/ui/bubble/model_wizard.go:274-278`（profile 向导应用）
- 修改：`internal/ui/bubble/command_helpers.go:56-59`（catalog 直切）
- 修改：`internal/ui/bubble/command_helpers.go:100-104`（profile 命令切换）
- 修改：`internal/ui/bubble/bubble_test.go`（`TestModelCommandVariantsKeepWizardAndShortcuts` 追加断言）
- 创建：`internal/ui/bubble/model_wizard_apply_test.go`

- [ ] **步骤 1：编写失败的测试**

在 `bubble_test.go` 的 `TestModelCommandVariantsKeepWizardAndShortcuts` 中，`/model backup` 断言块之后追加：

```go
	switchEntry := model.transcript[len(model.transcript)-1]
	if !isModelCardBlock(switchEntry.body) {
		t.Fatalf("switch entry body not a model card block: %q", switchEntry.body)
	}
	switchInfo, ok := parseModelCardBlock(switchEntry.body)
	if !ok || switchInfo.Model != "model-b" || switchInfo.Provider != "backup" {
		t.Fatalf("switch card info = %#v, ok=%v", switchInfo, ok)
	}
```

创建 `internal/ui/bubble/model_wizard_apply_test.go`：

```go
package bubble

import (
	"testing"
)

func TestApplyModelWizardSelectionEmitsModelCardBlock(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.modelWizard = &modelWizard{
		step:            modelWizardConfirm,
		providerOptions: []modelProviderOption{{id: "gateway", label: "Gateway"}},
		selectedIndex:   0,
		selectedModel:   0,
		modelOptions:    []string{"model-x"},
	}
	next := model.applyModelWizardSelection()
	if next.modelWizard != nil {
		t.Fatalf("wizard should close after apply")
	}
	last := next.transcript[len(next.transcript)-1]
	if !isModelCardBlock(last.body) {
		t.Fatalf("apply entry body not a model card block: %q", last.body)
	}
	info, ok := parseModelCardBlock(last.body)
	if !ok || info.Model != "model-x" {
		t.Fatalf("apply card info = %#v, ok=%v", info, ok)
	}
}
```

- [ ] **步骤 2：运行测试验证失败**

运行：`go test ./internal/ui/bubble -run 'TestModelCommandVariants|TestApplyModelWizardSelection' -v`
预期：FAIL——body 仍是 `provider=…` 键值串，`isModelCardBlock` 为 false。

- [ ] **步骤 3：修改四处调用点**

`model_wizard.go` catalog 向导应用（原 :244-248）的 `addEntry` body 改为：

```go
			body: formatModelSwitchBlock(cfg),
```

`model_wizard.go` profile 向导应用（原 :274-278）同样改为 `body: formatModelSwitchBlock(cfg),`。

`command_helpers.go` catalog 直切（原 :56-59）改为：

```go
				m.addEntry(transcriptEntry{kind: entrySystem, title: "model", body: formatModelSwitchBlock(cfg)})
```

`command_helpers.go` profile 命令切换（原 :100-104）的 body 改为 `formatModelSwitchBlock(cfg)`。

同时删除各处不再使用的 `fmt.Sprintf("id=… provider=…")` 拼接；若 `command_helpers.go` 的 `strings`/`fmt` import 因此不再被其他代码使用则一并清理（实际仍被 `commandArgs`/`handleSettingCommand` 等使用，预计无需动）。

- [ ] **步骤 4：运行测试验证通过**

运行：`go test ./internal/ui/bubble -run 'TestModelCommandVariants|TestApplyModelWizardSelection' -v`
预期：全部 PASS。

- [ ] **步骤 5：Commit**

```bash
git add -f internal/ui/bubble/model_wizard.go internal/ui/bubble/command_helpers.go internal/ui/bubble/bubble_test.go internal/ui/bubble/model_wizard_apply_test.go
git commit -m "feat(ui): 四处模型切换成功路径统一输出 <model> 块"
```

---

### 任务 5：确认面板主角式重写

**文件：**
- 修改：`internal/ui/bubble/model_wizard.go`（重写 `renderModelConfirmStep`，约 :373-395；import 加 `strconv`）
- 创建：`internal/ui/bubble/model_wizard_confirm_test.go`

- [ ] **步骤 1：编写失败的测试**

创建 `internal/ui/bubble/model_wizard_confirm_test.go`：

```go
package bubble

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"paw/internal/model"
)

func confirmTestWizard(profile model.Profile, models []string) *modelWizard {
	return &modelWizard{
		step:            modelWizardConfirm,
		providerOptions: []modelProviderOption{{id: profile.ID, label: profile.Name, profile: profile}},
		selectedIndex:   0,
		selectedModel:   0,
		modelOptions:    models,
	}
}

func TestRenderModelConfirmStepHeroLayoutHidesEmptyFields(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.modelWizard = confirmTestWizard(model.Profile{
		ID:                 "gateway",
		Name:               "Gateway",
		Provider:           "gateway",
		APIBaseURL:         "https://openrouter.ai/api/v1",
		Model:              "stealth/ox-alpha",
		ContextLimitTokens: 131072,
		RetryCount:         3,
	}, []string{"stealth/ox-alpha"})
	rendered := ansi.Strip(model.renderModelConfirmStep())
	for _, want := range []string{
		"Confirm model · gateway",
		"stealth/ox-alpha",
		"https://openrouter.ai/api/v1",
		"context",
		"131072 tokens",
		"retries",
		"enter 应用 · b 返回 · esc 取消",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("confirm panel missing %q:\n%s", want, rendered)
		}
	}
	for _, banned := range []string{"path", "key env", "models:", "Press enter to apply", "可选模型"} {
		if strings.Contains(rendered, banned) {
			t.Fatalf("confirm panel contains unexpected %q:\n%s", banned, rendered)
		}
	}
}

func TestRenderModelConfirmStepShowsModelCountHintAndError(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.modelWizard = confirmTestWizard(model.Profile{
		ID:                 "gateway",
		Name:               "Gateway",
		Provider:           "gateway",
		APIBaseURL:         "https://api.example/v1",
		APIPath:            "/responses",
		APIKeyEnvName:      "TEST_API_KEY",
		Model:              "alpha",
		Models:             []string{"alpha", "beta"},
		ContextLimitTokens: 4096,
		RetryCount:         2,
	}, []string{"alpha"})
	model.modelWizard.err = "selected model is unavailable"
	rendered := ansi.Strip(model.renderModelConfirmStep())
	for _, want := range []string{
		"另有 1 个可选模型",
		"key env",
		"TEST_API_KEY",
		"https://api.example/v1/responses",
		"selected model is unavailable",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("confirm panel missing %q:\n%s", want, rendered)
		}
	}
}
```

- [ ] **步骤 2：运行测试验证失败**

运行：`go test ./internal/ui/bubble -run TestRenderModelConfirmStep -v`
预期：FAIL——现面板仍是 `provider:` 平铺与英文提示，缺 `·` 标题后缀与中文 hint。

- [ ] **步骤 3：重写 renderModelConfirmStep**

用以下实现替换 `model_wizard.go` 中整个 `renderModelConfirmStep` 函数（保留函数上方注释并更新为「渲染模型配置确认步骤（主角式布局）和错误提示。」）：

```go
func (m appModel) renderModelConfirmStep() string {
	option := m.modelWizard.selectedProvider()
	cfg := m.configForProfile(option.profile)
	modelName := m.modelWizard.selectedModelNameOr(cfg.Model)
	mutedStyle := m.styles.StatusMuted
	keyStyle := lipgloss.NewStyle().Foreground(colorManager.LipglossColor(colorWorktreeClean))
	boldStyle := lipgloss.NewStyle().Bold(true)

	title := wizardTitleStyle.Render("Confirm model")
	if strings.TrimSpace(cfg.Provider) != "" {
		title += mutedStyle.Render(" · " + cfg.Provider)
	}
	lines := []string{title, "", boldStyle.Render(modelName)}
	if subtitle := cfg.APIBaseURL + cfg.APIPath; strings.TrimSpace(subtitle) != "" {
		lines = append(lines, mutedStyle.Render(subtitle))
	}

	type confirmRow struct{ label, value string }
	rows := []confirmRow{
		{label: "context", value: fmt.Sprintf("%d tokens", model.EffectiveContextLimitTokens(cfg))},
		{label: "retries", value: strconv.Itoa(cfg.RetryCount)},
	}
	if strings.TrimSpace(cfg.APIKeyEnvName) != "" {
		rows = append(rows, confirmRow{label: "key env", value: cfg.APIKeyEnvName})
	}
	labelWidth := 0
	for _, r := range rows {
		labelWidth = maxInt(labelWidth, len(r.label))
	}
	labelWidth += 2
	for _, r := range rows {
		lines = append(lines, mutedStyle.Render(fmt.Sprintf("%-*s", labelWidth, r.label))+r.value)
	}

	if extra := len(model.AvailableModels(cfg)) - 1; extra > 0 {
		lines = append(lines, "", mutedStyle.Render(fmt.Sprintf("另有 %d 个可选模型", extra)))
	}

	hint := keyStyle.Render("enter") + mutedStyle.Render(" 应用 · ") +
		keyStyle.Render("b") + mutedStyle.Render(" 返回 · ") +
		keyStyle.Render("esc") + mutedStyle.Render(" 取消")
	lines = append(lines, "", hint)
	if m.modelWizard.err != "" {
		lines = append(lines, labelErrorStyle.Render(m.modelWizard.err))
	}
	return strings.Join(lines, "\n")
}
```

`model_wizard.go` import 块加入 `"strconv"`。

- [ ] **步骤 4：运行测试验证通过**

运行：`go test ./internal/ui/bubble -run TestRenderModelConfirmStep -v`
预期：全部 PASS。

- [ ] **步骤 5：全量回归**

运行：`go vet ./... && go test ./...`
预期：vet 无新增告警，全部测试 PASS（重点观察 `TestModelWizardSearch*` 与 config_center 系列）。

- [ ] **步骤 6：Commit**

```bash
git add -f internal/ui/bubble/model_wizard.go internal/ui/bubble/model_wizard_confirm_test.go
git commit -m "feat(ui): Confirm model 面板改为主角式布局（空字段隐藏+中文快捷键提示）"
```

---

## 规格覆盖对照

| 规格条款 | 任务 |
|---|---|
| G1 主角式确认面板（3.1 全部规则） | 任务 5 |
| G2 四处调用点统一 `<model>` 块 + 圆角卡（3.2/3.3/3.4） | 任务 1–4 |
| G3 空字段隐藏 / models 计数提示 | 任务 1（块级省略）、任务 5（面板级行省略 + 另有 N 提示） |
| 测试计划 §5.1–5.4 | 任务 4 步骤 1（§5.1 改为新增断言，见下注）、任务 1–3、5 各测试 |
| 验收 §6.4 go vet + go test 全绿 | 任务 5 步骤 5 |

注：规格 §5.1 原计划「更新 bubble_test.go:645 断言」，实施核查发现该断言属于 `/model status`（N1 保持不动），故改为在 `TestModelCommandVariantsKeepWizardAndShortcuts` 中**新增**切换卡断言，规格意图（覆盖切换路径输出）不受影响。

## 自检记录

1. **规格覆盖度**：G1/G2/G3、§3.1–3.4、§5、§6 均有对应任务；N1/N2/N3 未引入违反任务。遗漏检查通过。
2. **占位符扫描**：无 TODO/待定/"类似任务 N"；所有代码步骤含完整代码。
3. **类型一致性**：`formatModelSwitchBlock(cfg model.Config)`、`parseModelCardBlock` 返回 `modelCardInfo`（字段 Provider/Model/Base/Path/Context/Retries/KeyEnv）在任务 1 定义、任务 2/3/4 使用一致；`escapeTaskBlockAttrValue` 与既有 `unescapeTaskBlockAttr` 对偶；复用的既有符号（`taskBlockAttrPattern`、`bodyStyle`、`sanitizeTerminalText`、`fitStyledCellLine`、`truncateStyledCellLine`、`maxInt`、`indentLines`、`transcriptEntryGutter`、`colorManager`、`m.styles.StatusMuted`、`wizardTitleStyle`、`labelErrorStyle`、`newTestModel`、`fakeRunner`）均已在当前代码库核实存在。
