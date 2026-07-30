# 内置 TUI 主题与全局 Settings 实现计划

> **面向 AI 代理的工作者：** 必需子技能：使用 superpowers:subagent-driven-development（推荐）或 superpowers:executing-plans 逐任务实现此计划。步骤使用复选框（`- [ ]`）语法来跟踪进度。

**目标：** 为 Bubble Tea TUI 增加七个内置 True Color 主题、完整背景渲染和可实时预览/保存/取消的 `/theme` 选择器，同时将整份 settings 迁移到 `~/.paw/settings.json`。

**架构：** 新建独立的 `internal/theme` 包，保存不可变的主题注册表、稳定 ID 和完整语义色板；`settings` 与 Bubble UI 都依赖该包，避免配置层反向依赖 UI。Bubble 的每个 `appModel` 持有自己的 `theme.Theme` 与 `StyleSet`，所有渲染路径通过实例样式取色；`applyTheme` 原子替换样式、重设 textarea/光标、清空 transcript 缓存并刷新 viewport。

**技术栈：** Go、Bubble Tea、Bubbles textarea/viewport、Lip Gloss、标准库 `encoding/json`/`os`/`filepath`、Go table-driven tests。

---

## 文件结构与职责

### 新建文件

- `internal/theme/theme.go`：定义 `ThemeID`、`ThemeMode`、`Palette`、`Theme`，实现主题 ID 规范化、查询、稳定列表和颜色校验。
- `internal/theme/themes.go`：声明七个内置主题的完整 True Color 色板；只允许该文件直接包含主题十六进制常量。
- `internal/theme/theme_test.go`：验证注册表数量、顺序、唯一性、模式、完整性、颜色格式和默认主题基线。
- `internal/ui/bubble/style_set.go`：定义 `StyleSet`，由 `theme.Palette` 一次性构造所有 Bubble UI 样式。
- `internal/ui/bubble/theme_apply.go`：实现 `applyTheme`、textarea 样式同步、光标取色、缓存失效和完整刷新。
- `internal/ui/bubble/theme_picker.go`：实现 `/theme` modal 的状态、渲染、导航、实时预览、保存和取消。
- `internal/ui/bubble/theme_picker_test.go`：隔离测试选择器打开、导航、预览、保存、失败重试、取消恢复和多实例隔离。
- `internal/ui/bubble/theme_render_test.go`：验证完整背景、深浅主题、尺寸不变量、切换后旧 ANSI 色消失以及无 UI 硬编码色。

### 修改文件

- `internal/settings/settings.go`：增加 `ui.theme`，把默认入口改为用户级路径，并提供可注入 home 解析边界。
- `internal/settings/settings_test.go`：覆盖全局路径、缺失文件、旧全局 JSON、主题规范化、损坏 JSON、home 错误和保存失败。
- `cmd/agent/main.go`：使用全局 settings controller；保留 headless/subagent 对同一 controller 的使用。
- `internal/ui/bubble/types.go`：给 `appModel` 增加当前主题、实例样式和选择器状态。
- `internal/ui/bubble/app.go`：启动时从 settings 初始化主题和 textarea 样式；在键盘优先级中加入 theme picker。
- `internal/ui/bubble/color_manager.go`：删除默认可变全局 palette，改为由 `theme.Palette` 构造的实例颜色解析器。
- `internal/ui/bubble/styles.go`：移除包级主题样式变量；保留与颜色无关的尺寸、边框和布局常量，或将构造逻辑移动到 `style_set.go`。
- `internal/ui/bubble/layout.go`：使用 `m.styles` 渲染 frame、transcript、input、overlay 和完整背景。
- `internal/ui/bubble/header.go`：让 header 渲染接收实例样式。
- `internal/ui/bubble/status_line.go`：将状态颜色与 glyph 样式迁入 `StyleSet`。
- `internal/ui/bubble/context_meter.go`：将 meter 的 cache/used/free/thinking/bar 样式改为实例样式。
- `internal/ui/bubble/cursor_animation.go`：使用当前实例 palette 计算光标插值颜色。
- `internal/ui/bubble/input.go`：使用实例输入提示和状态样式。
- `internal/ui/bubble/input_token.go`：使用实例 command/file token 样式。
- `internal/ui/bubble/transcript.go`：让 entry、tool、citation、diff 和 body 渲染使用 `m.styles`，并显式清理缓存。
- `internal/ui/bubble/markdown.go`：把 Markdown 渲染改为 `appModel`/`StyleSet` 驱动，不再读取包级样式。
- `internal/ui/bubble/new_message_notice.go`：将 normal/hover 样式迁入 `StyleSet`。
- `internal/ui/bubble/activity.go`：迁移 activity 状态样式。
- `internal/ui/bubble/right_panel.go`：移除 ANSI 256 色及硬编码 Lip Gloss 颜色，全部映射到语义 palette。
- `internal/ui/bubble/model_wizard.go`：使用实例 modal/selection 样式。
- `internal/ui/bubble/setting_wizard.go`：使用实例 modal/selection 样式。
- `internal/ui/bubble/session_picker.go`：使用实例 modal/selection 样式。
- `internal/ui/bubble/subagent_picker.go`：使用实例 modal/activity 样式。
- `internal/ui/bubble/tool_inspect.go`：使用实例 tool/modal 样式。
- `internal/ui/bubble/completion.go`：使用实例 completion 样式。
- `internal/ui/bubble/command_registry.go`：注册 `/theme`。
- `internal/ui/bubble/command_helpers.go`：在 `/status` 中加入标准主题 ID。
- `internal/ui/bubble/bubble_test.go`：更新现有断言和测试 helper，使其读取 `model.styles` 而非包级变量。
- `internal/ui/bubble/input_token_test.go`、`internal/ui/bubble/new_message_notice_test.go`、`internal/ui/bubble/status_line_test.go`、`internal/ui/bubble/fixed_layout_test.go`：更新受实例样式影响的测试。
- `README.md`：记录全局 settings 路径、`ui.theme`、内置主题和 `/theme` 操作。

### 删除或禁止继续使用

- 删除 `internal/ui/bubble/color_manager.go` 中的包级 `var colorManager`。
- 删除 `internal/ui/bubble/styles.go`、`right_panel.go`、`new_message_notice.go` 等文件中的包级主题样式变量。
- 除 `internal/theme/themes.go`、测试期望和用户动态颜色 `transcriptEntry.color` 外，Bubble UI 生产代码不得直接出现 `lipgloss.Color("...")` 或 `#RRGGBB` 颜色字面量。

---

### 任务 1：建立主题领域模型和内置注册表

**文件：**
- 创建：`internal/theme/theme.go`
- 创建：`internal/theme/themes.go`
- 创建：`internal/theme/theme_test.go`

- [ ] **步骤 1：编写主题注册表失败测试**

在 `internal/theme/theme_test.go` 写入表驱动测试，锁定七个主题和顺序：

```go
package theme

import (
    "reflect"
    "regexp"
    "testing"
)

func TestListReturnsStableBuiltInOrder(t *testing.T) {
    gotThemes := List()
    got := make([]ThemeID, 0, len(gotThemes))
    for _, item := range gotThemes {
        got = append(got, item.ID)
    }
    want := []ThemeID{
        Default,
        TokyoNight,
        TokyoNightStorm,
        TokyoNightLight,
        CatppuccinMocha,
        Dracula,
        GruvboxDark,
    }
    if !reflect.DeepEqual(got, want) {
        t.Fatalf("theme order = %#v, want %#v", got, want)
    }
}

func TestBuiltInThemesHaveCompleteTrueColorPalettes(t *testing.T) {
    hex := regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
    seen := map[ThemeID]bool{}
    for _, item := range List() {
        if seen[item.ID] {
            t.Fatalf("duplicate theme id %q", item.ID)
        }
        seen[item.ID] = true
        if item.Name == "" {
            t.Fatalf("theme %q has empty name", item.ID)
        }
        for role, color := range item.Colors.Values() {
            if !hex.MatchString(color) {
                t.Fatalf("theme %q role %q color = %q, want #RRGGBB", item.ID, role, color)
            }
        }
    }
    if len(seen) != 7 {
        t.Fatalf("theme count = %d, want 7", len(seen))
    }
}

func TestNormalizeID(t *testing.T) {
    tests := map[string]ThemeID{
        " TOKYO-NIGHT ": TokyoNight,
        "DrAcUlA":       Dracula,
        "":              Default,
        "unknown":       Default,
    }
    for input, want := range tests {
        if got := NormalizeID(input); got != want {
            t.Fatalf("NormalizeID(%q) = %q, want %q", input, got, want)
        }
    }
}

func TestTokyoNightLightIsOnlyLightTheme(t *testing.T) {
    for _, item := range List() {
        want := ModeDark
        if item.ID == TokyoNightLight {
            want = ModeLight
        }
        if item.Mode != want {
            t.Fatalf("theme %q mode = %q, want %q", item.ID, item.Mode, want)
        }
    }
}
```

同时增加 `TestDefaultPaletteMatchesLegacyBaseline`，逐项断言当前 `NewColorManager()` 中已有颜色转换成 `#RRGGBB` 后的默认值；原先的 ANSI 256 色（如 `"229"`、`"240"`）在测试期望中明确替换为对应的 RGB 值，不允许新主题继续保存 ANSI 索引。

- [ ] **步骤 2：运行测试并确认因主题 API 不存在而失败**

运行：

```bash
go test ./internal/theme -run 'Test(List|BuiltIn|Normalize|Tokyo|Default)' -count=1
```

预期：FAIL，编译错误包含 `undefined: ThemeID`、`undefined: List` 或包目录尚不存在。

- [ ] **步骤 3：实现主题类型、完整 Palette 和不可变查询 API**

在 `internal/theme/theme.go` 定义稳定接口：

```go
package theme

import "strings"

type ThemeID string
type ThemeMode string

const (
    ModeDark  ThemeMode = "dark"
    ModeLight ThemeMode = "light"

    Default          ThemeID = "default"
    TokyoNight       ThemeID = "tokyo-night"
    TokyoNightStorm  ThemeID = "tokyo-night-storm"
    TokyoNightLight  ThemeID = "tokyo-night-light"
    CatppuccinMocha  ThemeID = "catppuccin-mocha"
    Dracula          ThemeID = "dracula"
    GruvboxDark      ThemeID = "gruvbox-dark"
)

type Palette struct {
    TerminalBackground     string
    HeaderBackground       string
    HeaderForeground       string
    LabelUser              string
    LabelAssistant         string
    LabelTool              string
    LabelResult            string
    LabelSystem            string
    LabelError             string
    Body                    string
    ToolDetailBackground    string
    MarkdownHeading         string
    MarkdownRule            string
    MarkdownBullet          string
    MarkdownCodeForeground  string
    MarkdownCodeBackground  string
    MarkdownCodeBorder      string
    MarkdownLink            string
    MarkdownQuote           string
    MarkdownQuoteBorder     string
    PanelBorder             string
    InputFocusedBorder      string
    InputWaitingBorder      string
    InputMultilineBorder    string
    InputTerminal           string
    InputTokenCommand       string
    InputTokenFile          string
    SelectedProviderBG      string
    SelectedProviderFG      string
    UnselectedProvider      string
    WizardTitle             string
    WizardBorder            string
    SelectionBackground     string
    SelectionForeground     string
    ContextCache            string
    ContextUsed             string
    ContextFree             string
    Signal                  string
    WorktreeBackground      string
    WorktreeBorder          string
    WorktreeClean           string
    WorktreeDirty           string
    WorktreeConflict        string
    CursorNormalBright      string
    CursorTerminalBright    string
}

type Theme struct {
    ID     ThemeID
    Name   string
    Mode   ThemeMode
    Colors Palette
}

func NormalizeID(value string) ThemeID {
    id := ThemeID(strings.ToLower(strings.TrimSpace(value)))
    if _, ok := ByID(id); ok {
        return id
    }
    return Default
}

func ByID(id ThemeID) (Theme, bool) {
    for _, item := range builtIns {
        if item.ID == id {
            return item, true
        }
    }
    return Theme{}, false
}

func List() []Theme {
    return append([]Theme(nil), builtIns...)
}
```

给 `Palette` 增加 `Values() map[string]string`，必须列出每一个字段，以便完整性测试能发现遗漏。不要返回内部可修改 map；注册表只由值类型和复制 slice 暴露。

- [ ] **步骤 4：填写七个主题的完整 True Color 色板**

在 `internal/theme/themes.go` 定义固定顺序的 `builtIns`。`default` 使用任务 1 测试锁定的 legacy RGB 基线；其余六个主题使用已确认的视觉基线。每个 `Palette.Values()` 返回的键都必须在每个主题中设置为非空 `#RRGGBB`。

```go
package theme

var builtIns = []Theme{
    {
        ID: Default, Name: "Default", Mode: ModeDark,
        Colors: Palette{
            TerminalBackground: "#292c33",
            HeaderBackground:   "#242830",
            HeaderForeground:   "#f0e6d5",
            Body:                 "#c9c2b7",
            Signal:               "#76d5e8",
            PanelBorder:          "#3b434c",
            InputFocusedBorder:   "#76d5e8",
            ContextUsed:          "#76d5e8",
            ContextFree:          "#687581",
            CursorNormalBright:   "#9fffd3",
            CursorTerminalBright: "#ff8ddd",
        },
    },
    {
        ID: TokyoNight, Name: "Tokyo Night", Mode: ModeDark,
        Colors: Palette{
            TerminalBackground: "#1a1b26",
            HeaderBackground:   "#24283b",
            HeaderForeground:   "#c0caf5",
            Signal:             "#7aa2f7",
            Body:                 "#c0caf5",
            PanelBorder:          "#3b4261",
            ContextUsed:          "#7dcfff",
            ContextFree:          "#565f89",
            CursorNormalBright:   "#9ece6a",
            CursorTerminalBright: "#bb9af7",
        },
    },
    // Tokyo Night Storm、Tokyo Night Light、Catppuccin Mocha、Dracula、Gruvbox Dark。
}
```

主题色板是本任务唯一允许集中维护颜色常量的位置。不要为不存在的功能增加可配置字体、间距或图标字段。

- [ ] **步骤 5：运行主题包测试**

运行：

```bash
go test ./internal/theme -count=1
```

预期：PASS。

- [ ] **步骤 6：提交主题注册表**

```bash
git add internal/theme/theme.go internal/theme/themes.go internal/theme/theme_test.go
git commit -m "feat: add built-in TUI theme registry"
```

---

### 任务 2：将 settings 改为全局路径并持久化主题 ID

**文件：**
- 修改：`internal/settings/settings.go`
- 修改：`internal/settings/settings_test.go`
- 修改：`cmd/agent/main.go:445-474`

- [ ] **步骤 1：编写全局路径与主题规范化失败测试**

在 `internal/settings/settings_test.go` 增加：

```go
func TestDefaultSettingsPathUsesHomeDirectory(t *testing.T) {
    got, err := DefaultPath(func() (string, error) { return "/tmp/paw-home", nil })
    if err != nil {
        t.Fatal(err)
    }
    want := filepath.Join("/tmp/paw-home", ".paw", "settings.json")
    if got != want {
        t.Fatalf("DefaultPath() = %q, want %q", got, want)
    }
}

func TestDefaultSettingsPathPropagatesHomeError(t *testing.T) {
    wantErr := errors.New("no home")
    _, err := DefaultPath(func() (string, error) { return "", wantErr })
    if !errors.Is(err, wantErr) {
        t.Fatalf("error = %v, want wrapped %v", err, wantErr)
    }
}

func TestNormalizeTheme(t *testing.T) {
    cfg := DefaultConfig()
    cfg.UI.Theme = " TOKYO-NIGHT "
    if got := Normalize(cfg).UI.Theme; got != theme.TokyoNight {
        t.Fatalf("theme = %q, want %q", got, theme.TokyoNight)
    }
    cfg.UI.Theme = "not-installed"
    if got := Normalize(cfg).UI.Theme; got != theme.Default {
        t.Fatalf("invalid theme = %q, want %q", got, theme.Default)
    }
}

func TestNewDefaultControllerIgnoresProjectSettings(t *testing.T) {
    home := t.TempDir()
    project := t.TempDir()
    t.Chdir(project)
    if err := os.MkdirAll(filepath.Join(project, ".paw"), 0o755); err != nil { t.Fatal(err) }
    if err := os.WriteFile(filepath.Join(project, ".paw", "settings.json"), []byte(`{"ui":{"theme":"dracula"}}`), 0o600); err != nil { t.Fatal(err) }

    controller, err := NewDefaultController(func() (string, error) { return home, nil })
    if err != nil { t.Fatal(err) }
    if got := controller.CurrentSettings().UI.Theme; got != theme.Default {
        t.Fatalf("theme = %q, want default; project settings must be ignored", got)
    }
}
```

再补充：

- 缺少 `ui.theme` 的旧全局 JSON 返回 `theme.Default`；
- 损坏 JSON 的错误文本包含完整路径；
- `SaveSettings` 失败后 `CurrentSettings()` 不变；
- 成功保存创建 `~/.paw/settings.json` 且文件 mode 为 `0600`（Windows 上跳过 mode 断言）。

- [ ] **步骤 2：运行 settings 测试并确认失败**

运行：

```bash
go test ./internal/settings -count=1
```

预期：FAIL，错误包含缺少 `DefaultPath`、`NewDefaultController` 或 `UIConfig.Theme`。

- [ ] **步骤 3：实现全局默认入口和主题字段**

在 `internal/settings/settings.go`：

```go
const defaultSettingsRelativePath = ".paw/settings.json"

type HomeDirFunc func() (string, error)

type UIConfig struct {
    Theme                theme.ThemeID `json:"theme"`
    ContextLimitTokens   int           `json:"context_limit_tokens"`
    ContextMeterLocation MeterLocation `json:"context_meter_location"`
}

func DefaultPath(homeDir HomeDirFunc) (string, error) {
    if homeDir == nil {
        homeDir = os.UserHomeDir
    }
    home, err := homeDir()
    if err != nil {
        return "", fmt.Errorf("resolve settings home directory: %w", err)
    }
    if strings.TrimSpace(home) == "" {
        return "", fmt.Errorf("resolve settings home directory: empty path")
    }
    return filepath.Join(home, ".paw", "settings.json"), nil
}

func NewDefaultController(homeDir HomeDirFunc) (*Controller, error) {
    path, err := DefaultPath(homeDir)
    if err != nil {
        return nil, err
    }
    return NewController(path)
}
```

`DefaultConfig()` 设置 `UI.Theme: theme.Default`；`Normalize()` 使用 `theme.NormalizeID(string(cfg.UI.Theme))`。保持 `NewController(path)` 只接受显式非空路径；空路径返回 `settings path is empty`。删除 `NewControllerInCwd`，所有生产调用统一改用 `NewDefaultController(nil)`。

错误包装必须带路径：

```go
return Config{}, fmt.Errorf("parse settings %s: %w", path, err)
```

保存目录建议使用 `0700`；配置文件继续使用 `0600`。

- [ ] **步骤 4：更新 runner 构建入口使用全局 controller**

在 `cmd/agent/main.go` 的 `buildRunnerWithSubagentContext` 中替换：

```go
settingsController, err := settings.NewDefaultController(nil)
```

不要从 `root` 或 cwd 拼接 settings 路径。headless、interactive 和 subagent worker 继续共享这一构建路径，因此会自然读取同一全局配置。

- [ ] **步骤 5：运行 settings 和 agent 包测试**

运行：

```bash
go test ./internal/settings ./cmd/agent -count=1
```

预期：PASS。

- [ ] **步骤 6：提交全局 settings**

```bash
git add internal/settings/settings.go internal/settings/settings_test.go cmd/agent/main.go
git commit -m "feat: load settings from user home"
```

---

### 任务 3：构建实例级 ColorManager 和 StyleSet

**文件：**
- 创建：`internal/ui/bubble/style_set.go`
- 创建：`internal/ui/bubble/style_set_test.go`
- 修改：`internal/ui/bubble/color_manager.go`
- 修改：`internal/ui/bubble/styles.go`
- 修改：`internal/ui/bubble/types.go:315-394`

- [ ] **步骤 1：编写 StyleSet 映射和实例隔离失败测试**

创建 `internal/ui/bubble/style_set_test.go`：

```go
func TestNewStyleSetUsesThemePalette(t *testing.T) {
    item, ok := theme.ByID(theme.TokyoNight)
    if !ok { t.Fatal("Tokyo Night missing") }
    styles := NewStyleSet(item.Colors)

    if got := fmt.Sprint(styles.Body.GetForeground()); got != strings.ToLower(item.Colors.Body) {
        t.Fatalf("body foreground = %q, want %q", got, item.Colors.Body)
    }
    if got := fmt.Sprint(styles.Frame.GetBackground()); got != strings.ToLower(item.Colors.TerminalBackground) {
        t.Fatalf("frame background = %q, want %q", got, item.Colors.TerminalBackground)
    }
    if got := fmt.Sprint(styles.ToolDetail.GetBackground()); got != strings.ToLower(item.Colors.ToolDetailBackground) {
        t.Fatalf("tool background = %q, want %q", got, item.Colors.ToolDetailBackground)
    }
}

func TestStyleSetsDoNotShareMutableState(t *testing.T) {
    tokyo, _ := theme.ByID(theme.TokyoNight)
    light, _ := theme.ByID(theme.TokyoNightLight)
    a := NewStyleSet(tokyo.Colors)
    b := NewStyleSet(light.Colors)
    if a.Body.GetForeground() == b.Body.GetForeground() {
        t.Fatal("independent themes unexpectedly share body color")
    }
}
```

增加 `TestColorManagerCursorColorUsesProvidedPalette`，确保暗色和浅色主题使用各自 background 插值，不读取包级状态。

- [ ] **步骤 2：运行测试并确认失败**

运行：

```bash
go test ./internal/ui/bubble -run 'Test(NewStyleSet|StyleSets|ColorManagerCursor)' -count=1
```

预期：FAIL，错误包含 `undefined: NewStyleSet`。

- [ ] **步骤 3：改造 ColorManager 为 palette 值对象**

保留现有 `colorRole` 常量以降低迁移风险，但删除 `var colorManager` 和无参 `NewColorManager()`。实现：

```go
type ColorManager struct {
    palette theme.Palette
}

func NewColorManager(palette theme.Palette) ColorManager {
    return ColorManager{palette: palette}
}

func (c ColorManager) Hex(role colorRole) string {
    return strings.ToLower(c.roleValue(role))
}
```

`roleValue` 用 `switch` 把每个现有 `colorRole` 映射到具名 `theme.Palette` 字段。未知角色返回 `palette.Body`，测试中直接覆盖全部已声明角色，防止漏映射。`CursorColor` 保留插值逻辑，但只读实例 palette。

- [ ] **步骤 4：定义完整 StyleSet 并集中构造样式**

在 `style_set.go` 定义覆盖现有 `styles.go`、notice、wizard、completion、right panel 和动态状态所需字段。字段使用导出式命名以便测试，但类型仍留在 `bubble` 包：

```go
type StyleSet struct {
    Colors ColorManager

    Frame              lipgloss.Style
    Header             lipgloss.Style
    Body               lipgloss.Style
    ThinkingBody       lipgloss.Style
    LabelUser          lipgloss.Style
    LabelAssistant     lipgloss.Style
    LabelThinking      lipgloss.Style
    LabelTool          lipgloss.Style
    LabelSystem        lipgloss.Style
    LabelError         lipgloss.Style
    ToolHeader         lipgloss.Style
    ToolDetail         lipgloss.Style
    ToolCitation       lipgloss.Style
    ToolCitationKey    lipgloss.Style
    ToolCitationOK     lipgloss.Style
    ToolCitationError  lipgloss.Style
    MarkdownBold       lipgloss.Style
    MarkdownHeading    lipgloss.Style
    MarkdownRule       lipgloss.Style
    MarkdownBullet     lipgloss.Style
    MarkdownCode       lipgloss.Style
    MarkdownLink       lipgloss.Style
    MarkdownCodeBlock  lipgloss.Style
    MarkdownQuote      lipgloss.Style
    TranscriptContent  lipgloss.Style
    InputDock          lipgloss.Style
    InputDockMultiline lipgloss.Style
    InputDockTerminal  lipgloss.Style
    InputHint          lipgloss.Style
    InputPrompt        lipgloss.Style
    InputTokenCommand  lipgloss.Style
    InputTokenFile     lipgloss.Style
    ContextCache       lipgloss.Style
    ContextUsed        lipgloss.Style
    ContextFree        lipgloss.Style
    Modal              lipgloss.Style
    ModalTitle         lipgloss.Style
    Selected           lipgloss.Style
    Unselected         lipgloss.Style
    Notice             lipgloss.Style
    NoticeHover        lipgloss.Style
    DiffAddedForeground   lipgloss.Style
    DiffAddedBackground   lipgloss.Style
    DiffDeletedForeground lipgloss.Style
    DiffDeletedBackground lipgloss.Style
    StatusRunning         lipgloss.Style
    StatusSuccess         lipgloss.Style
    StatusWarning         lipgloss.Style
    StatusError           lipgloss.Style
    StatusMuted           lipgloss.Style
}

func NewStyleSet(p theme.Palette) StyleSet {
    colors := NewColorManager(p)
    return StyleSet{
        Colors: colors,
        Frame: lipgloss.NewStyle().
            Foreground(colors.LipglossColor(colorPanelBorder)).
            Background(colors.LipglossColor(colorTerminalBackground)),
        Body: lipgloss.NewStyle().
            Foreground(colors.LipglossColor(colorBody)).
            Background(colors.LipglossColor(colorTerminalBackground)),
        ToolDetail: lipgloss.NewStyle().
            Foreground(colors.LipglossColor(colorBody)).
            Background(colors.LipglossColor(colorToolDetailBackground)),
        // 完整复制现有结构属性，并把颜色来源替换为 colors。
    }
}
```

不要在构造器中读取 settings、全局变量或终端环境。`StyleSet` 构造必须是纯函数。

- [ ] **步骤 5：给 appModel 增加实例主题字段**

在 `types.go` 增加：

```go
theme       theme.Theme
styles      StyleSet
themePicker *themePickerState
```

此时先允许旧渲染路径继续使用尚未迁移的样式；本任务只建立新实例状态，不做行为切换。

- [ ] **步骤 6：运行 StyleSet 测试和全包编译**

运行：

```bash
go test ./internal/ui/bubble -run 'Test(NewStyleSet|StyleSets|ColorManagerCursor)' -count=1
go test ./internal/ui/bubble -run '^$'
```

预期：PASS。

- [ ] **步骤 7：提交样式基础设施**

```bash
git add internal/ui/bubble/color_manager.go internal/ui/bubble/styles.go internal/ui/bubble/style_set.go internal/ui/bubble/style_set_test.go internal/ui/bubble/types.go
git commit -m "refactor: add instance-scoped TUI style sets"
```

---

### 任务 4：启动时应用主题并实现原子 `applyTheme`

**文件：**
- 创建：`internal/ui/bubble/theme_apply.go`
- 创建：`internal/ui/bubble/theme_apply_test.go`
- 修改：`internal/ui/bubble/app.go:34-99`
- 修改：`internal/ui/bubble/cursor_animation.go`
- 修改：`internal/ui/bubble/transcript.go:416-447`

- [ ] **步骤 1：编写启动、缓存失效和多实例失败测试**

在 `theme_apply_test.go` 增加 helper：

```go
func newThemedTestModel(t *testing.T, id theme.ThemeID) appModel {
    t.Helper()
    controller := &fakeSettingsController{current: settings.DefaultConfig()}
    controller.current.UI.Theme = id
    return newModel(context.Background(), &fakeRunner{}, "session-1", &fakeModelConfigController{}, controller, nil, nil, newTerminalCursorAnchor())
}
```

测试：

```go
func TestNewModelUsesConfiguredTheme(t *testing.T) {
    model := newThemedTestModel(t, theme.Dracula)
    if model.theme.ID != theme.Dracula {
        t.Fatalf("theme = %q, want %q", model.theme.ID, theme.Dracula)
    }
}

func TestApplyThemeClearsRenderCacheAndRefreshesViewport(t *testing.T) {
    model := newThemedTestModel(t, theme.Default)
    model.addEntry(transcriptEntry{kind: entryAssistant, body: "hello"})
    _ = model.renderTranscriptContent()
    if len(model.transcriptRenderCache) == 0 { t.Fatal("expected populated cache") }

    if err := model.applyTheme(theme.TokyoNight); err != nil { t.Fatal(err) }
    if model.theme.ID != theme.TokyoNight { t.Fatalf("theme = %q", model.theme.ID) }
    if len(model.transcriptRenderCache) != 0 { t.Fatalf("cache length = %d, want 0", len(model.transcriptRenderCache)) }
    if !strings.Contains(model.viewport.View(), "hello") { t.Fatal("viewport was not refreshed") }
}

func TestTwoModelsCanUseDifferentThemes(t *testing.T) {
    dark := newThemedTestModel(t, theme.TokyoNight)
    light := newThemedTestModel(t, theme.TokyoNightLight)
    if dark.styles.Frame.GetBackground() == light.styles.Frame.GetBackground() {
        t.Fatal("models share theme styles")
    }
}
```

- [ ] **步骤 2：运行测试并确认失败**

运行：

```bash
go test ./internal/ui/bubble -run 'Test(NewModelUsesConfiguredTheme|ApplyTheme|TwoModels)' -count=1
```

预期：FAIL，`newModel` 尚未初始化 `model.theme` 或缺少 `applyTheme`。

- [ ] **步骤 3：实现纯准备 + 原子提交的 applyTheme**

在 `theme_apply.go`：

```go
func (m *appModel) applyTheme(id theme.ThemeID) error {
    nextTheme, ok := theme.ByID(theme.NormalizeID(string(id)))
    if !ok {
        return fmt.Errorf("theme %q is not registered", id)
    }
    nextStyles := NewStyleSet(nextTheme.Colors)

    m.theme = nextTheme
    m.styles = nextStyles
    m.applyTextareaTheme()
    m.transcriptRenderCache = nil
    m.transcriptRefreshPending = false
    m.refreshViewportPreservingOffset()
    m.applyCursorAnimation()
    return nil
}
```

注意：`theme.NormalizeID` 会把未知值回退到 default，因此若需要对内部调用严格报错，先用原始标准化字符串调用 `ByID`；配置加载阶段已经负责无效值回退。`applyTheme` 对选择器传入的注册 ID 应永不失败，但仍保留错误返回以防注册表/构造器不一致。

实现 `applyTextareaTheme()`，覆盖 focused/blurred 的 `Base`、`CursorLine`、`Text`、`Placeholder`，所有背景使用当前主题背景，不再透明。terminal textarea 的临时副本也必须从 `m.styles` 获取颜色。

- [ ] **步骤 4：在 newModel 中先构造主题再初始化 textarea**

读取规范化 settings：

```go
cfg := settings.DefaultConfig()
if settingsController != nil {
    cfg = settings.Normalize(settingsController.CurrentSettings())
}
selected, _ := theme.ByID(cfg.UI.Theme)
styles := NewStyleSet(selected.Colors)
```

把 `theme: selected`、`styles: styles` 放入 model literal；model 建立后调用 `model.applyTextareaTheme()`，不要继续调用依赖包级 `colorManager` 的 `applyTextareaPlainBackground`。初始 viewport refresh 必须发生在样式就绪之后。

- [ ] **步骤 5：让光标动画读取实例 ColorManager**

把：

```go
cursorColor(intensity, terminal)
```

替换为：

```go
m.styles.Colors.CursorColor(intensity, terminal)
```

删除包级 `cursorColor` wrapper。更新现有 cursor 测试，使其在指定主题 model 上断言插值结果。

- [ ] **步骤 6：运行主题应用测试**

运行：

```bash
go test ./internal/ui/bubble -run 'Test(NewModelUsesConfiguredTheme|ApplyTheme|TwoModels|Cursor)' -count=1
```

预期：PASS。

- [ ] **步骤 7：提交主题应用生命周期**

```bash
git add internal/ui/bubble/theme_apply.go internal/ui/bubble/theme_apply_test.go internal/ui/bubble/app.go internal/ui/bubble/cursor_animation.go internal/ui/bubble/transcript.go
git commit -m "feat: apply themes per TUI model"
```

---

### 任务 5：迁移核心 frame、header、status、input 和 context meter

**文件：**
- 修改：`internal/ui/bubble/layout.go`
- 修改：`internal/ui/bubble/header.go`
- 修改：`internal/ui/bubble/status_line.go`
- 修改：`internal/ui/bubble/context_meter.go`
- 修改：`internal/ui/bubble/input.go`
- 修改：`internal/ui/bubble/input_token.go`
- 修改：`internal/ui/bubble/worktree.go`
- 修改：`internal/ui/bubble/new_message_notice.go`
- 修改：`internal/ui/bubble/fixed_layout_test.go`
- 修改：`internal/ui/bubble/status_line_test.go`
- 修改：`internal/ui/bubble/input_token_test.go`
- 修改：`internal/ui/bubble/new_message_notice_test.go`

- [ ] **步骤 1：编写完整背景和核心区域失败测试**

在 `theme_render_test.go` 添加 ANSI 背景断言 helper：

```go
func requireThemeBackgroundOnEveryLine(t *testing.T, view, want string) {
    t.Helper()
    sequence := lipgloss.NewStyle().Background(lipgloss.Color(want)).Render(" ")
    prefix := strings.TrimSuffix(sequence, " \x1b[0m")
    for i, line := range strings.Split(view, "\n") {
        if !strings.Contains(line, prefix) {
            t.Fatalf("line %d does not contain background %q: %q", i, want, line)
        }
    }
}
```

如果 Lip Gloss 合并 ANSI 序列导致该 helper 不稳定，改为使用 `github.com/charmbracelet/x/ansi` 解析 SGR 状态，逐 cell 验证背景；不要退化成只检查首行。

新增：

```go
func TestViewPaintsWholeTokyoNightLightBackground(t *testing.T) {
    model := newThemedTestModel(t, theme.TokyoNightLight)
    model.ready, model.width, model.height = true, 80, 24
    model.relayout()
    view := model.View()
    if got := lipgloss.Width(view); got != 80 { t.Fatalf("width = %d", got) }
    if got := lipgloss.Height(view); got != 24 { t.Fatalf("height = %d", got) }
    requireThemeBackgroundOnEveryLine(t, view, model.theme.Colors.TerminalBackground)
}
```

再测试 header、input dock、context meter、notice hover 的前景/背景等于当前 `model.styles` 对应语义色。

- [ ] **步骤 2：运行核心渲染测试并确认失败**

运行：

```bash
go test ./internal/ui/bubble -run 'TestViewPaintsWhole|TestThemeCore' -count=1
```

预期：FAIL，空白区域仍透明或核心函数仍引用包级样式。

- [ ] **步骤 3：让 View 和固定 panel 使用实例样式**

将 `layout.go` 中的包级样式引用逐一替换：

```go
base := renderFixedStyledPanel(
    m.styles.TranscriptContent,
    layout.contentWidth,
    layout.transcriptHeight,
    content,
)
```

`renderHairlineFrame` 改为接收样式：

```go
func renderHairlineFrame(inner string, width, height int, style lipgloss.Style) string
```

先生成严格尺寸的 frame，再由当前背景样式渲染每行。不要对包含换行的整个字符串只设置一次宽度后假设空白已着色；使用 `fitStyledRect` 后逐行 pad 并通过 `m.styles.Frame.Width(width).Render(line)` 保证每个 cell 有背景。

`inputDockContentWidth` 改为接收 `m.styles.InputDock` 或直接成为 model method，避免继续读取包级 `inputDockStyle`。

- [ ] **步骤 4：迁移 header、status、input、worktree 和 notice**

把渲染 helper 改成以下两种形式之一：

```go
func (m appModel) renderDockStatusLine(width int) string
func renderHeader(s headerSnapshot, width int, styles StyleSet) string
```

所有调用点显式传递 `m.styles`。动态 glyph 不再创建硬编码 style：

```go
return m.styles.StatusSuccess.Render(glyph)
```

`applyTextareaTerminalStyle` 改为 model method或接收 `StyleSet`：

```go
func applyTextareaTerminalStyle(input *textarea.Model, styles StyleSet)
```

输入 token 使用 `m.styles.InputTokenCommand` 和 `m.styles.InputTokenFile`。notice normal/hover 使用 `m.styles.Notice`/`NoticeHover`，删除文件级 style 变量。

- [ ] **步骤 5：迁移 context meter 的所有样式参数**

把纯 helper 明确接收所需样式，或提升为 model method。例如：

```go
func (m appModel) renderContextMeterLine(...) string
func renderContextBar(..., styles contextMeterStyles) string
```

其中 `contextMeterStyles` 从 `m.styles` 构造，包含 cache、used、free、thinking、bar used/cache/free。不要让纯函数反向读取 appModel 或全局变量。

- [ ] **步骤 6：更新相关现有测试**

将类似：

```go
contextCacheStyle.GetForeground()
```

改为：

```go
model.styles.ContextCache.GetForeground()
```

每个测试自行创建 model，禁止重新引入测试专用全局样式。保持原有尺寸、prompt、token 无背景等行为断言；完整背景要求下，原“textarea text 无背景”测试应改成“textarea text 背景等于主题 terminal background”。

- [ ] **步骤 7：运行核心区域测试**

运行：

```bash
go test ./internal/ui/bubble -run 'Test(ViewPaintsWhole|Relayout|RenderInput|Textarea|Token|Context|Status|Notice|Worktree|Header)' -count=1
```

预期：PASS。

- [ ] **步骤 8：提交核心渲染迁移**

```bash
git add internal/ui/bubble/layout.go internal/ui/bubble/header.go internal/ui/bubble/status_line.go internal/ui/bubble/context_meter.go internal/ui/bubble/input.go internal/ui/bubble/input_token.go internal/ui/bubble/worktree.go internal/ui/bubble/new_message_notice.go internal/ui/bubble/theme_render_test.go internal/ui/bubble/fixed_layout_test.go internal/ui/bubble/status_line_test.go internal/ui/bubble/input_token_test.go internal/ui/bubble/new_message_notice_test.go internal/ui/bubble/bubble_test.go
git commit -m "refactor: theme core TUI regions"
```

---

### 任务 6：迁移 transcript、Markdown、工具详情和动态用户颜色

**文件：**
- 修改：`internal/ui/bubble/transcript.go`
- 修改：`internal/ui/bubble/markdown.go`
- 修改：`internal/ui/bubble/tool_inspect.go`
- 修改：`internal/ui/bubble/tool_track_test.go`
- 修改：`internal/ui/bubble/bubble_test.go`

- [ ] **步骤 1：编写 Markdown 和 transcript 主题切换失败测试**

在 `theme_render_test.go` 增加：

```go
func TestThemeSwitchRemovesOldMarkdownAndToolColors(t *testing.T) {
    model := newThemedTestModel(t, theme.TokyoNight)
    model.addEntry(transcriptEntry{kind: entryAssistant, body: "# Heading\n[link](https://example.com)\n`code`"})
    model.addEntry(transcriptEntry{kind: entryTool, title: "Bash", body: "ok", toolStatus: "ok"})
    before := model.renderTranscriptContent()
    oldColor := strings.ToLower(model.theme.Colors.MarkdownHeading)
    if !strings.Contains(strings.ToLower(before), ansiColorFragment(oldColor)) {
        t.Fatalf("before frame missing old heading color %q", oldColor)
    }

    if err := model.applyTheme(theme.TokyoNightLight); err != nil { t.Fatal(err) }
    after := model.renderTranscriptContent()
    if strings.Contains(strings.ToLower(after), ansiColorFragment(oldColor)) {
        t.Fatalf("after frame still contains old color %q", oldColor)
    }
}
```

`ansiColorFragment` 将 `#RRGGBB` 转成 `38;2;r;g;b`，避免依赖 Lip Gloss 输出大小写。

另加：动态 `transcriptEntry.color = "#669988"` 仍优先于主题 label 色，但 body、背景和工具面板继续使用主题样式。

- [ ] **步骤 2：运行测试并确认失败**

运行：

```bash
go test ./internal/ui/bubble -run 'TestThemeSwitchRemovesOldMarkdown|TestDynamicTranscriptColor' -count=1
```

预期：FAIL，Markdown 或 transcript 仍读取包级样式。

- [ ] **步骤 3：把 transcript 渲染链改为 model 驱动**

把：

```go
func renderEntry(entry transcriptEntry, width int) string
func renderEntryAt(entry transcriptEntry, width int, at time.Time) string
```

改为：

```go
func (m appModel) renderEntry(entry transcriptEntry, width int) string
func (m appModel) renderEntryAt(entry transcriptEntry, width int, at time.Time) string
```

继续向下迁移 `renderEntryBodyAt`、`renderTool...`、citation、diff 和 selection 所需样式。缓存 key 增加 `themeID theme.ThemeID` 作为防御性约束：

```go
themeID theme.ThemeID
```

即使 `applyTheme` 已清缓存，key 仍防止未来遗漏失效。

动态 agent color 只用于 label：

```go
labelStyle := m.labelStyle(entry.kind)
if color != "" {
    labelStyle = labelStyle.Foreground(lipgloss.Color(color))
}
```

动态颜色必须通过现有 sanitize/合法性边界；不要将它写入注册表。

- [ ] **步骤 4：把 Markdown 渲染改为 StyleSet 参数**

首选纯 renderer：

```go
type markdownRenderer struct { styles StyleSet }

func (r markdownRenderer) Render(markdown string, width int) string
func (r markdownRenderer) renderInline(text string) string
func (r markdownRenderer) renderCodeBlock(lang, code string, width int) string
```

`appModel` 调用：

```go
renderer := markdownRenderer{styles: m.styles}
body := renderer.Render(entry.body, width)
```

所有 Markdown helper 都通过 receiver 取样式，不保留 `markdownHeadingStyle` 等全局。代码高亮若存在第三方 ANSI 输出，外围 panel 背景仍使用主题 code background；首版不重新设计语法高亮主题。

- [ ] **步骤 5：迁移工具详情、citation、diff 和 tool inspect**

把现有 `toolHeaderStyle`、`toolDetailStyle`、citation rail、OK/error、diff added/deleted 等映射到 `m.styles`。当前测试中硬编码的 diff ANSI 色（如 `"224"`、`"52"`）改为当前主题语义 `DiffAddedForeground/Background`、`DiffDeletedForeground/Background`；如 `Palette` 尚无这些字段，在任务 1 的 Palette 和七个主题中补齐，并更新完整性测试。

- [ ] **步骤 6：运行 transcript 与 Markdown 测试**

运行：

```bash
go test ./internal/ui/bubble -run 'Test(ThemeSwitchRemovesOldMarkdown|DynamicTranscriptColor|Markdown|Tool|Citation|Diff)' -count=1
```

预期：PASS。

- [ ] **步骤 7：提交 transcript 主题迁移**

```bash
git add internal/theme/theme.go internal/theme/themes.go internal/theme/theme_test.go internal/ui/bubble/transcript.go internal/ui/bubble/markdown.go internal/ui/bubble/tool_inspect.go internal/ui/bubble/theme_render_test.go internal/ui/bubble/tool_track_test.go internal/ui/bubble/bubble_test.go
git commit -m "refactor: theme transcript and markdown rendering"
```

---

### 任务 7：迁移 modal、completion、activity 和 right panel，清除硬编码 UI 色

**文件：**
- 修改：`internal/ui/bubble/model_wizard.go`
- 修改：`internal/ui/bubble/setting_wizard.go`
- 修改：`internal/ui/bubble/session_picker.go`
- 修改：`internal/ui/bubble/subagent_picker.go`
- 修改：`internal/ui/bubble/completion.go`
- 修改：`internal/ui/bubble/activity.go`
- 修改：`internal/ui/bubble/right_panel.go`
- 修改：`internal/ui/bubble/layout.go:166-181`
- 修改：`internal/ui/bubble/model_wizard_test.go`
- 修改：`internal/ui/bubble/bubble_test.go`

- [ ] **步骤 1：编写 modal/right-panel 主题和静态硬编码失败测试**

在 `theme_render_test.go` 增加渲染测试，打开 model wizard、setting wizard、completion 和 activity，确认输出包含当前主题 modal/surface/selection 色。

增加针对生产文件的静态测试：

```go
func TestBubbleProductionCodeHasNoDirectColorLiterals(t *testing.T) {
    allowed := map[string]bool{
        "theme_render_test.go": true,
        "style_set_test.go":   true,
    }
    entries, err := os.ReadDir(".")
    if err != nil { t.Fatal(err) }
    forbidden := regexp.MustCompile(`lipgloss\.Color\(("#[0-9a-fA-F]{6}"|"[0-9]{1,3}")\)|#[0-9a-fA-F]{6}`)
    for _, entry := range entries {
        if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") || allowed[entry.Name()] {
            continue
        }
        data, err := os.ReadFile(entry.Name())
        if err != nil { t.Fatal(err) }
        if loc := forbidden.Find(data); loc != nil {
            t.Fatalf("%s contains direct UI color literal %q", entry.Name(), loc)
        }
    }
}
```

该测试允许 `lipgloss.Color(color)` 这类运行时值；颜色常量只应存在 `internal/theme/themes.go`。

- [ ] **步骤 2：运行测试并确认 right_panel 等文件失败**

运行：

```bash
go test ./internal/ui/bubble -run 'Test(Modal|Completion|Activity|RightPanel|BubbleProductionCodeHasNoDirectColorLiterals)' -count=1
```

预期：FAIL，报告 `right_panel.go`、notice 或其他文件的硬编码颜色。

- [ ] **步骤 3：迁移所有 wizard、picker 和 completion 样式**

将选择项渲染统一为实例 helper：

```go
func (m appModel) renderPickerOption(selected bool, label, description string, width int) string {
    style := m.styles.Unselected
    if selected {
        style = m.styles.Selected
    }
    return style.Width(width).Render(...)
}
```

保留各 wizard 的状态机和文案，不借主题改造重写交互逻辑。`renderActiveModalBox` 后续会加入 theme picker，但本任务先确保现有 modal 使用 `m.styles.Modal`、`ModalTitle`、`Selected`、`Unselected` 和 `WizardBorder`。

- [ ] **步骤 4：迁移 activity 和 right panel 的状态语义**

把硬编码索引映射为 StyleSet 字段：

- running → `StatusRunning` / `ContextUsed`
- done → `StatusSuccess` / `WorktreeClean`
- failed → `StatusError` / `LabelError`
- stopped/pending → `StatusMuted` / `ContextFree`
- retry/warning → `StatusWarning` / `WorktreeDirty`
- panel background → `Surface` / `ToolDetailBackground`
- selected rail → `Signal`

不要机械保留 `"84"`、`"203"` 等值；使用语义字段确保每个主题可调整。

- [ ] **步骤 5：移除遗留包级主题样式**

删除 `styles.go` 中已无调用的主题变量，只保留：

- 与颜色无关的 border shape；
- padding、margin、width 等常量；
- 仅包含 border shape、padding、margin、width 的无颜色基础 style；任何调用 `Foreground`、`Background` 或 `BorderForeground` 的 style 都必须进入 `StyleSet`。

运行 grep：

```bash
rg 'lipgloss\.Color\("|#[0-9A-Fa-f]{6}|Foreground\(colorManager|Background\(colorManager|BorderForeground\(colorManager' internal/ui/bubble --glob '*.go' --glob '!**/*_test.go'
```

预期：无输出。动态 `lipgloss.Color(color)` 不会匹配第一项。

- [ ] **步骤 6：运行 modal、panel 和全 Bubble 测试**

运行：

```bash
go test ./internal/ui/bubble -count=1
```

预期：PASS。

- [ ] **步骤 7：提交剩余 UI 样式迁移**

```bash
git add internal/ui/bubble/model_wizard.go internal/ui/bubble/setting_wizard.go internal/ui/bubble/session_picker.go internal/ui/bubble/subagent_picker.go internal/ui/bubble/completion.go internal/ui/bubble/activity.go internal/ui/bubble/right_panel.go internal/ui/bubble/layout.go internal/ui/bubble/styles.go internal/ui/bubble/theme_render_test.go internal/ui/bubble/model_wizard_test.go internal/ui/bubble/bubble_test.go
git commit -m "refactor: theme all TUI overlays and panels"
```

---

### 任务 8：实现 `/theme` 实时预览选择器

**文件：**
- 创建：`internal/ui/bubble/theme_picker.go`
- 创建：`internal/ui/bubble/theme_picker_test.go`
- 修改：`internal/ui/bubble/types.go`
- 修改：`internal/ui/bubble/app.go:349-369`
- 修改：`internal/ui/bubble/layout.go:166-181`
- 修改：`internal/ui/bubble/command_registry.go:30-70`

- [ ] **步骤 1：编写命令注册和打开状态失败测试**

在 `theme_picker_test.go`：

```go
func TestThemeCommandIsRegistered(t *testing.T) {
    registry := NewCommandRegistry()
    command, ok := registry.Resolve("/theme")
    if !ok { t.Fatal("/theme is not registered") }
    if command.AllowWhileRunning { t.Fatal("/theme must not open while a turn is running") }
}

func TestOpenThemePickerSelectsCurrentTheme(t *testing.T) {
    model := newThemedTestModel(t, theme.Dracula)
    model.openThemePicker()
    if model.themePicker == nil { t.Fatal("theme picker not opened") }
    if model.themePicker.original != theme.Dracula || model.themePicker.selected != theme.Dracula {
        t.Fatalf("picker = %#v", model.themePicker)
    }
}
```

- [ ] **步骤 2：编写实时预览、取消、保存和失败测试**

使用 `tea.KeyMsg` 驱动真实 handler：

```go
func TestThemePickerNavigationPreviewsWithoutSaving(t *testing.T) {
    controller := &fakeSettingsController{current: settings.DefaultConfig()}
    model := newModel(..., controller, ...)
    model.openThemePicker()
    nextModel, _ := model.handleThemePickerKey(tea.KeyMsg{Type: tea.KeyDown})
    got := nextModel.(appModel)
    if got.theme.ID == theme.Default { t.Fatal("down did not preview next theme") }
    if len(controller.saved) != 0 { t.Fatalf("preview saved %d configs", len(controller.saved)) }
}

func TestThemePickerEscapeRestoresOriginal(t *testing.T) { /* preview down, press esc, expect default and nil picker */ }
func TestThemePickerEnterSavesAndCloses(t *testing.T) { /* preview, enter, expect one save and nil picker */ }
func TestThemePickerSaveFailureKeepsPreviewOpen(t *testing.T) { /* controller.err, enter, expect same preview, picker.err non-empty */ }
func TestThemePickerSaveFailureThenEscapeRestores(t *testing.T) { /* failed save then esc */ }
func TestThemePickerHomeAndEnd(t *testing.T) { /* end selects gruvbox, home selects default */ }
```

- [ ] **步骤 3：运行 picker 测试并确认失败**

运行：

```bash
go test ./internal/ui/bubble -run 'TestTheme(Command|Open|Picker)' -count=1
```

预期：FAIL，缺少 picker API 和 `/theme`。

- [ ] **步骤 4：实现 themePickerState 和打开逻辑**

```go
type themePickerState struct {
    original    theme.ThemeID
    selected    theme.ThemeID
    selectedIdx int
    saveError   string
}

func (m *appModel) openThemePicker() {
    items := theme.List()
    idx := 0
    for i, item := range items {
        if item.ID == m.theme.ID { idx = i; break }
    }
    m.themePicker = &themePickerState{
        original: m.theme.ID,
        selected: m.theme.ID,
        selectedIdx: idx,
    }
    m.pending = nil
    m.clearCompletionAndRelayout()
}
```

注册命令：

```go
registry.Register(Command{
    Name: "/theme",
    Description: "preview and select a color theme",
    AllowWhileRunning: false,
    Handler: func(m *appModel, _ string) tea.Cmd {
        m.openThemePicker()
        return nil
    },
})
```

- [ ] **步骤 5：实现键盘导航、实时预览和事务式保存**

`handleThemePickerKey` 优先处理 `up/k`、`down/j`、`home`、`end`、`enter`、`esc`。导航 helper：

```go
func (m *appModel) previewThemeIndex(index int) error {
    items := theme.List()
    index = clampInt(index, 0, len(items)-1)
    candidate := items[index]
    if err := m.applyTheme(candidate.ID); err != nil { return err }
    m.themePicker.selectedIdx = index
    m.themePicker.selected = candidate.ID
    m.themePicker.saveError = ""
    return nil
}
```

`Enter`：复制 `m.currentSettings()`，设置 `cfg.UI.Theme`。若 controller 当前已保存 ID 等于 selected，直接关闭；否则调用 `SaveSettings`。失败时只设置 `saveError`，不得关闭或恢复预览。

`Esc`：先 `applyTheme(original)`；成功后才令 `themePicker = nil`。恢复失败则保留 modal 并显示错误。

- [ ] **步骤 6：实现 modal 渲染**

在 `renderActiveModalBox` 中把 theme picker 放在其他 modal 之前或与打开互斥：

```go
case m.themePicker != nil:
    return m.renderThemePickerBox()
```

每项渲染 name、ID、mode、五个色块和保存标记。色块必须用候选主题自身 palette 构造，而不是当前 `m.styles`，否则列表中所有 swatch 会同色：

```go
swatch := lipgloss.NewStyle().Background(lipgloss.Color(candidate.Colors.Signal)).Render("  ")
```

这是动态注册表颜色，不属于硬编码色。modal chrome、文字、选中背景必须使用当前预览主题的 `m.styles`。

窄终端时按 cell width 截断；选择器高度大于 transcript 时只显示可容纳项，并保持 selected 可见。

- [ ] **步骤 7：把 theme picker 放入 KeyMsg 最高 modal 优先级**

在 `app.go` 的 `tea.KeyMsg` 分支中，在 setting/model wizard 前处理：

```go
if m.themePicker != nil {
    return m.handleThemePickerKey(msg)
}
```

picker 打开期间不让按键传给 textarea、viewport 或 completion。

- [ ] **步骤 8：运行 picker 与 modal 测试**

运行：

```bash
go test ./internal/ui/bubble -run 'TestTheme(Command|Open|Picker)' -count=1
```

预期：PASS。

- [ ] **步骤 9：提交主题选择器**

```bash
git add internal/ui/bubble/theme_picker.go internal/ui/bubble/theme_picker_test.go internal/ui/bubble/types.go internal/ui/bubble/app.go internal/ui/bubble/layout.go internal/ui/bubble/command_registry.go
git commit -m "feat: add interactive theme picker"
```

---

### 任务 9：补充 `/status`、配置文档和最终回归测试

**文件：**
- 修改：`internal/ui/bubble/command_helpers.go:269-291`
- 修改：`internal/ui/bubble/bubble_test.go`
- 修改：`README.md`

- [ ] **步骤 1：编写 `/status` 主题字段失败测试**

在 `bubble_test.go` 增加：

```go
func TestStatusTextIncludesCurrentTheme(t *testing.T) {
    model := newThemedTestModel(t, theme.CatppuccinMocha)
    got := model.statusText("session-1")
    if !strings.Contains(got, "theme: catppuccin-mocha") {
        t.Fatalf("status = %q", got)
    }
}
```

- [ ] **步骤 2：运行测试并确认失败**

运行：

```bash
go test ./internal/ui/bubble -run TestStatusTextIncludesCurrentTheme -count=1
```

预期：FAIL，status 尚未包含主题。

- [ ] **步骤 3：在状态文本中加入标准主题 ID**

把 settings 行拆开或追加独立行：

```go
"settings: context=%s run=%s meter=%s limit=%d\ntheme: %s\nqueue: %d..."
```

主题值使用 `m.theme.ID`；若 model 尚未初始化则使用 `theme.Default`，不要直接回显未经规范化的 settings 字符串。

- [ ] **步骤 4：更新 README**

加入准确示例：

```json
{
  "subagent": {
    "default_context_mode": "empty",
    "default_run_mode": "background"
  },
  "ui": {
    "theme": "tokyo-night",
    "context_limit_tokens": 1048576,
    "context_meter_location": "input-above"
  }
}
```

明确：

- 路径是 `~/.paw/settings.json`；
- 项目 `.paw/settings.json` 不再读取或迁移；
- 七个主题 ID；
- `/theme` 中移动键实时预览、`Enter` 保存、`Esc` 取消；
- 首版仅保证 True Color，不支持自定义主题、256 色降级和 `NO_COLOR`。

- [ ] **步骤 5：运行格式化、静态扫描和完整测试**

运行：

```bash
gofmt -w internal/theme/*.go internal/settings/*.go internal/ui/bubble/*.go cmd/agent/main.go
git diff --check
rg 'NewControllerInCwd|var colorManager|lipgloss\.Color\("|#[0-9A-Fa-f]{6}' internal/ui/bubble --glob '*.go' --glob '!**/*_test.go'
go test ./internal/theme ./internal/settings ./internal/ui/bubble ./cmd/agent -count=1
go test ./... -count=1
go vet ./...
```

预期：

- `git diff --check` 无输出；
- `rg` 无输出，动态 `lipgloss.Color(variable)` 不受影响；
- 所有 `go test` PASS；
- `go vet` 无诊断。

若 `go test ./...` 因当前工作区中与本功能无关的既存修改失败，先用 `git diff --name-only` 和失败包确认归属；不得修改或回滚用户已有的 model request-body 工作来掩盖失败。

- [ ] **步骤 6：手工烟雾测试深色、浅色和持久化**

使用临时 home，避免污染真实用户配置：

```bash
TEST_HOME="$(mktemp -d)"
HOME="$TEST_HOME" go run ./cmd/agent
```

在 TUI 中执行：

1. `/theme`；
2. 移到 `Tokyo Night Light`，确认整屏立即变浅且空白区域无深色漏出；
3. `Esc`，确认恢复 default，`$TEST_HOME/.paw/settings.json` 不存在；
4. 再次 `/theme`，选择 `Dracula` 并按 `Enter`；
5. 退出并用同一 `HOME` 重启；
6. 确认 Dracula 自动恢复；
7. 执行 `/status`，确认包含 `theme: dracula`。

预期：预览无写盘，保存后创建全局配置，重启恢复，浅色背景完整覆盖。

- [ ] **步骤 7：提交文档与最终回归修正**

```bash
git add internal/ui/bubble/command_helpers.go internal/ui/bubble/bubble_test.go README.md
git commit -m "docs: document global TUI themes"
```

---

## 最终验收清单

- [ ] `internal/theme` 恰好注册七个稳定主题 ID，所有 palette 字段为 `#RRGGBB`。
- [ ] 默认 settings 从 `~/.paw/settings.json` 加载，不读取、合并或迁移项目 `.paw/settings.json`。
- [ ] 配置缺失或主题非法时使用 `default`；JSON 损坏和 home 解析失败给出明确路径/原因。
- [ ] 每个 `appModel` 持有独立 `Theme` 和 `StyleSet`，不存在可变全局主题状态。
- [ ] `applyTheme` 原子更新 theme/style、textarea、光标、缓存和 viewport。
- [ ] frame、空白 cell、input、transcript、modal、completion、notice 和 right panel 全部具有当前主题背景。
- [ ] `/theme` 导航实时预览但不写盘；`Enter` 保存；`Esc` 恢复；保存失败可重试或取消。
- [ ] `/status` 显示规范化主题 ID。
- [ ] Bubble 生产代码无直接颜色字面量，主题颜色集中在 `internal/theme/themes.go`。
- [ ] 深色与 Tokyo Night Light 渲染测试、尺寸不变量、旧 ANSI 色清理和多实例隔离测试全部通过。
- [ ] `go test ./... -count=1` 和 `go vet ./...` 通过。
