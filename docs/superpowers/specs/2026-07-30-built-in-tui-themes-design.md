# 内置 TUI 主题与全局 Settings 设计

日期：2026-07-30

## 1. 背景与目标

当前 Bubble Tea TUI 已经通过 `ColorManager` 和语义颜色角色集中管理大部分颜色，但许多 Lip Gloss 样式仍以包级变量初始化，`right_panel.go` 等位置还存在硬编码颜色。现有 settings 默认从当前项目的 `.paw/settings.json` 读取。

本功能的目标是：

1. 提供一组内置主题，包括 Tokyo Night 系列和常用配色；
2. 通过 `/theme` 在 TUI 内实时预览并保存主题；
3. 让主题覆盖整个固定尺寸 TUI，包括所有空白 cell 和浅色背景；
4. 将整份 settings 改为用户级全局配置；
5. 在不引入可变全局主题状态的前提下，为每个 `appModel` 构建独立样式集。

## 2. 范围

### 2.1 首版包含

首版内置以下稳定主题 ID：

- `default`
- `tokyo-night`
- `tokyo-night-storm`
- `tokyo-night-light`
- `catppuccin-mocha`
- `dracula`
- `gruvbox-dark`

首版提供：

- 全局 settings：`~/.paw/settings.json`；
- `ui.theme` 配置字段；
- `/theme` modal；
- 选择时实时预览；
- `Enter` 保存；
- `Esc` 恢复打开选择器前的主题；
- True Color（24-bit）配色；
- 深色与浅色主题的完整背景渲染；
- `/status` 展示当前主题 ID。

### 2.2 首版不包含

- 自定义主题文件；
- 用户颜色覆盖；
- CLI `--theme` 参数；
- 256 色降级；
- `NO_COLOR` 支持；
- 自动检测终端深浅背景；
- 项目级主题覆盖；
- 全局与项目配置合并；
- 外部配置热加载；
- 旧项目 settings 自动迁移或兼容回退。

## 3. 全局 Settings

### 3.1 默认路径

settings 默认路径从：

```text
<project>/.paw/settings.json
```

改为：

```text
~/.paw/settings.json
```

默认路径必须使用 `os.UserHomeDir()` 解析，不依赖当前工作目录。为保证测试隔离，用户主目录解析应通过可注入函数或等价边界实现，测试不得访问真实 home。

`NewController(明确路径)` 继续允许调用方传入特定路径，便于测试或显式用法；默认构造入口负责使用全局路径。

### 3.2 不迁移旧配置

程序不再读取项目级 `.paw/settings.json`，也不复制、合并、删除或提示迁移旧文件。

如果 `~/.paw/settings.json` 不存在，程序直接使用默认配置，不主动创建文件。只有发生成功的 settings 保存操作时才创建 `~/.paw/` 和配置文件。

### 3.3 配置格式

`UIConfig` 增加主题字段。完整配置示例：

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

主题配置值保存为注册表中的标准 ID。

### 3.4 规范化规则

- 缺少 `ui.theme`：使用 `default`；
- 空字符串：使用 `default`；
- 输入先去除首尾空白并转为小写；
- 已知主题：规范化为对应标准 ID；
- 未知主题：回退为 `default`；
- 主题值无效时，其他有效 settings 字段仍正常保留。

### 3.5 配置错误

- 无配置文件：返回默认配置；
- JSON 损坏：启动失败，错误包含具体文件路径；
- 无法解析用户主目录：启动失败并给出明确错误；
- 无法创建目录或写入配置：保存失败，不修改 controller 的内存配置；
- 保存文件继续使用 `0600` 权限；用户配置目录使用现有安全策略创建。

## 4. 主题模型与注册表

### 4.1 核心类型

主题系统由三个边界清晰的类型组成：

```go
type ThemeID string

type ThemeMode string

const (
    ThemeModeDark  ThemeMode = "dark"
    ThemeModeLight ThemeMode = "light"
)

type Theme struct {
    ID     ThemeID
    Name   string
    Mode   ThemeMode
    Colors Palette
}

type Palette struct {
    Background string
    Surface    string
    Foreground string
    Muted      string
    Primary    string
    Secondary  string
    Success    string
    Warning    string
    Error      string
    // 以及覆盖现有全部 colorRole 所需的具名语义字段
}

type StyleSet struct {
    // 由 Palette 一次性构造的全部 Lip Gloss 样式
}
```

职责划分：

- `Theme` 保存稳定 ID、展示名、深浅模式和完整色板；
- `Palette` 使用具名语义字段，不暴露任意字符串 map；
- `StyleSet` 是将色板转换为 Lip Gloss 样式的唯一构造边界；
- `ColorManager` 演进为接收 `Palette` 的轻量颜色解析器，不再依赖运行时可变的包级调色板。

### 4.2 注册表

内置主题注册表初始化后不可变，并提供：

- 按 ID 查询；
- 以固定顺序列出；
- ID 规范化；
- 主题存在性验证。

注册表顺序即 `/theme` 选择器顺序，必须稳定。主题 ID 是配置兼容接口，不应因展示名调整而改变。

每个主题必须提供完整语义色板。`default` 必须尽量逐项复现当前 UI 配色，避免升级后默认外观意外变化。

## 5. 样式架构

### 5.1 每实例主题状态

`appModel` 持有独立主题和样式集：

```go
type appModel struct {
    theme       Theme
    styles      StyleSet
    themePicker *themePickerState

    // 现有状态
}
```

渲染代码通过 `m.styles` 获取样式，例如：

```go
m.styles.Body.Render(text)
m.styles.ToolDetail.Render(text)
```

主题切换不得通过修改包级全局变量实现。多个 `appModel` 必须可以同时使用不同主题，且测试之间不互相污染。

### 5.2 首版迁移范围

首版必须将所有当前可见 TUI 区域纳入 `StyleSet`：

- 整体背景和外框；
- header；
- transcript；
- input dock；
- 状态栏；
- Markdown 正文、粗体、标题、规则、列表、链接、引用、行内代码和代码块；
- 工具调用、工具结果、citation 和错误状态；
- context meter；
- completion；
- modal；
- new-message notice；
- right panel；
- textarea 正文、placeholder 和光标；
- worktree chip、输入 token 和其他当前可见辅助状态。

`right_panel.go` 等文件中的硬编码 Lip Gloss 颜色必须改为语义色。主题化 UI 路径中不保留绕开主题注册表的硬编码颜色。

### 5.3 完整背景渲染

主题控制整个 TUI 背景，而不仅是文本和局部面板。

`View()` 完成尺寸裁剪后，最外层使用当前主题背景样式渲染严格等于终端宽高的固定帧。内部面板按语义使用 `Background` 或 `Surface`。

必须保证：

- 空白 cell 具有主题背景；
- frame 的横线及其余补齐区域不露出终端原背景；
- overlay、modal、completion 和 padding 不产生背景断层；
- `tokyo-night-light` 在深色终端中仍呈现完整浅色画布；
- 切换主题后的下一帧覆盖整个窗口。

退出 TUI 时继续依赖 Bubble Tea 恢复终端状态，不永久修改终端自身配色。

## 6. 统一主题应用入口

主题变化统一经过：

```go
func (m *appModel) applyTheme(id ThemeID) error
```

该方法负责：

1. 从注册表查询主题；
2. 为该主题构建新的 `StyleSet`；
3. 更新 `m.theme` 和 `m.styles`；
4. 重新应用 textarea 正文、placeholder、focused/blurred 和光标样式；
5. 清空 transcript render cache；
6. 使 Markdown 及其他依赖颜色的派生渲染失效；
7. 重新生成 viewport 内容；
8. 请求完整帧重绘。

预览、保存后的最终应用和取消恢复均调用此入口。调用方不得分别修改零散样式。

主题注册表中的 ID 均应能成功构建样式；如果内部构建仍返回错误，调用方保持旧主题和旧样式不变，并显示错误，避免半应用状态。

## 7. `/theme` 选择器

### 7.1 命令入口

在现有命令注册表中增加 `/theme`。执行后打开居中的主题选择 modal，不增加搜索、导入或编辑能力。

每一项显示：

- 展示名称；
- 稳定 ID；
- `dark` 或 `light`；
- 一组关键语义色样本；
- 当前已保存主题的标记。

示意：

```text
Theme
────────────────────────────────────────
  Default              default              dark
› Tokyo Night          tokyo-night          dark
  Tokyo Night Storm    tokyo-night-storm    dark
  Tokyo Night Light    tokyo-night-light    light
  Catppuccin Mocha     catppuccin-mocha     dark
  Dracula              dracula              dark
  Gruvbox Dark         gruvbox-dark         dark

↑/↓ preview    enter save    esc cancel
```

### 7.2 状态

```go
type themePickerState struct {
    original ThemeID
    selected ThemeID
    saveError string
}
```

打开选择器时：

1. 将当前已应用主题记录为 `original`；
2. 将 `selected` 定位到当前主题；
3. 清空旧保存错误；
4. 暂停普通输入和其他 modal 交互；
5. 显示选择器，不写配置。

### 7.3 导航和实时预览

支持：

- `↑` / `k`：上一项；
- `↓` / `j`：下一项；
- `home`：第一项；
- `end`：最后一项。

选中项改变时调用 `applyTheme(selected)` 并立即完整重绘。移动只改变当前 `appModel` 的内存状态，不调用 `SaveSettings`。

若预览应用失败，保留上一项及上一主题，并在 modal 中显示错误。

### 7.4 保存

按 `Enter` 时：

1. 复制 controller 当前 settings；
2. 将 `UI.Theme` 设置为选中主题的标准 ID；
3. 调用 `SaveSettings` 写入 `~/.paw/settings.json`；
4. 保存成功后关闭 modal；
5. 保留当前预览主题；
6. 不向 transcript 追加系统消息。

如果选中主题等于 controller 中已保存的主题，可以跳过磁盘写入并直接关闭 modal。

### 7.5 取消

按 `Esc` 时：

1. 调用 `applyTheme(original)`；
2. 成功恢复后关闭 modal；
3. 不修改配置文件。

如果恢复主题发生内部错误，选择器保持打开并显示错误，避免界面状态与选择器状态不一致。

### 7.6 保存失败

保存失败时：

- modal 保持打开；
- 当前预览主题保持不变；
- 显示简短错误，如 `Unable to save theme: permission denied`；
- controller 内存配置保持不变；
- `Enter` 可重试；
- `Esc` 可恢复 `original` 并退出。

## 8. 启动与数据流

交互模式启动流程：

1. 解析用户主目录；
2. 从 `~/.paw/settings.json` 加载并规范化 settings；
3. 将 settings controller 注入 Bubble UI；
4. `newModel` 读取 `UI.Theme`；
5. 从主题注册表解析主题；
6. 构建该 `appModel` 的 `StyleSet`；
7. 用主题样式初始化 textarea、viewport 和首次完整帧。

单轮 headless 模式与 subagent worker 仍加载同一份全局 settings，以保持 subagent 默认行为、context 设置等一致，但不需要构建 TUI 主题样式。

`/status` 输出增加当前标准主题 ID，以便排查配置和显示问题。

## 9. 缓存一致性

当前 transcript 缓存键只包含部分颜色相关信息。主题会同时影响 Markdown、工具详情、引用、代码块、状态和其他派生样式，因此不能依赖缓存键逐字段自然失效。

`applyTheme` 必须显式：

- 清空 transcript render cache；
- 清空或重建 Markdown 派生渲染状态；
- 重新渲染 viewport；
- 重新应用 textarea 子样式；
- 触发完整帧绘制。

切换后生成的新帧不得混用旧主题 ANSI 样式。

## 10. 测试策略

### 10.1 Settings 单元测试

验证：

- 默认路径是用户主目录下的 `.paw/settings.json`；
- 主目录解析可注入且不访问真实 home；
- 文件缺失时返回默认配置和 `default` 主题；
- 缺少 `theme` 字段时向后兼容；
- 合法主题 ID 可加载和保存；
- 大小写和空白被规范化；
- 非法主题 ID 回退为 `default`；
- JSON 损坏错误包含路径；
- home 解析失败；
- 目录创建和写入失败；
- 保存失败不更新 controller 内存状态；
- 项目级 `.paw/settings.json` 不会被默认入口读取。

### 10.2 Theme 与 StyleSet 单元测试

验证：

- 注册表包含且只包含七个首版主题；
- 主题 ID 唯一；
- 列表顺序稳定；
- 每个主题所有语义颜色非空；
- 每个颜色是有效的 `#RRGGBB` True Color 值；
- light/dark 元数据正确；
- `default` 与当前颜色基线一致；
- `StyleSet` 关键样式使用正确语义色；
- 主题化 UI 路径中不残留未经允许的硬编码 Lip Gloss 颜色。

硬编码颜色检查可以通过针对性静态测试或代码审查约束实现；主题定义文件本身的十六进制颜色不属于违规硬编码。

### 10.3 `/theme` 交互测试

验证：

- 打开时定位当前主题；
- 上下、home、end 导航正确；
- 导航立即改变当前主题；
- 导航只预览、不保存；
- `Esc` 恢复原主题；
- `Enter` 成功保存并关闭；
- 选择已保存主题时可跳过写入；
- 保存失败时保持 modal 和预览状态；
- 保存失败后 `Enter` 可重试；
- 保存失败后 `Esc` 可恢复；
- 多个 `appModel` 的主题与样式互不污染。

### 10.4 渲染回归测试

至少使用一个深色主题和 `tokyo-night-light` 验证：

- `View()` 每行宽度与总高度保持不变；
- 整帧空白区域使用主题背景；
- transcript、input、modal、completion 和 right panel 使用当前主题；
- 切换后新帧不再包含旧主题关键 ANSI 颜色；
- 浅色主题的正文、状态、错误、链接和代码具有可读对比度；
- overlay 与 padding 不出现背景断层。

不维护大型整屏 ANSI golden 文件。优先断言语义颜色、关键片段和尺寸不变量，降低无关文案或间距调整造成的测试噪音。

## 11. 成功标准

功能完成时必须满足：

1. 用户可在任意项目运行程序并共享同一份 `~/.paw/settings.json`；
2. `/theme` 能在七个主题之间即时完整预览；
3. `Enter` 持久化，重启后恢复保存主题；
4. `Esc` 无磁盘副作用并恢复原主题；
5. 深色与浅色主题均覆盖整个终端画布；
6. 所有当前可见 TUI 颜色均来自当前实例的语义主题；
7. 多实例和并行测试不受可变全局主题状态影响；
8. 旧项目 settings 不会被读取或迁移；
9. settings、主题注册表、选择器和关键渲染路径均有自动化测试。
