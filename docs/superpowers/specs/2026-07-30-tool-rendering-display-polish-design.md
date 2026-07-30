# Paw TUI 工具轨道信息减法与状态色块设计

**日期：** 2026-07-30
**状态：** 已完成设计，待实现计划
**范围：** `internal/ui/bubble`，以及为显示工作区根目录所需的最小 Runner 能力

## 背景

当前工具轨道直接把原始工具名、目标和状态拼到一行。MCP 工具使用 `server__tool` 命名空间，例如 `codegraph__...`，导致主行过长且信息层级混乱。文件工具的参数也曾出现 `file_path` 这类字段名，而不是用户真正需要定位的文件路径。

本设计的目标是让工具主行只表达三件事：工具是谁、正在处理什么、执行状态如何。原始工具名仍保留在底层，用于调用、tool-use ID 匹配、历史恢复和展开详情。

## 已确认的视觉结果

主行采用“工具名: 目标”结构：

```text
◌ CodeGraph: 读取页面  运行中 · 12s
✓ LS: internal/ui/bubble  完成 · 338ms
× Edit: internal/ui/bubble/transcript.go  出错 · 1.1s
```

状态与耗时合并为一个终端色块：

- `完成 · 耗时`：绿色背景；
- `运行中 · 耗时`：橙色背景；
- `出错 · 耗时`：红色背景。

色块只覆盖状态段，不覆盖整行；状态文字在 `NO_COLOR=1` 时仍保留。工具仍为单行紧凑轨道，不新增 Web 卡片、浮层、tooltip、渐变、阴影或额外时间线。

## 设计

### 1. 原始身份与显示身份分离

不修改工具调用协议，也不把显示名称写回 `ToolCallEvent.Name`。新增 Bubble Tea 渲染层的显示目录，输入原始工具名和 JSON 输入，输出短显示名称、动作描述和目标文本。

显示目录的规则按规范化后的原始工具名匹配：

```text
codegraph__read_url → CodeGraph: 读取页面
codegraph__search   → CodeGraph: 搜索
codegraph__unknown  → CodeGraph: unknown
LS                  → LS: <目标>
Edit                → Edit: <目标>
```

常用工具使用内置中文别名字典。未收录的 MCP 工具使用服务器短名加工具短名，例如 `CodeGraph: explore`；不得把完整 `codegraph__...` 命名空间泄漏到主行。原始名称仍存放在现有 `toolName` 字段中，工具结果继续优先按 `toolUseID` 匹配，并保留现有名称回退匹配。

显示目录应是纯函数式、无 I/O 的渲染辅助单元，建议放在独立的 `internal/ui/bubble/tool_display.go`，避免把 alias、路径提取和状态样式继续堆进 `utils.go` 或 `transcript.go`。

### 2. 工具目标与真实路径

目标提取优先读取工具输入中的关键值：

- 文件工具优先读取 `file_path`，其次读取 `path`；
- `Bash` 使用 `command`；
- Web 工具使用 `url`；
- MCP 工具使用内置动作描述，必要时使用服务器提供的相对目标；
- 未知工具使用第一个安全的非敏感目标值。

文件路径必须显示真实值，不能显示字段名，也不能只显示 basename：

```text
Edit: internal/ui/bubble/transcript.go
LS: internal/ui/bubble
```

渲染上下文使用与 Runner 相同的 `workRoot`。在工作区内的绝对路径转换为相对工作区根目录的路径；路径统一使用 `/`。工作区外的原生文件目标按照既有允许根目录语义保留可定位的绝对路径；解析失败时至少保留输入中的原始相对路径，不退化为 `file_path`。

为避免扩大现有 `Runner` 接口，Bubble UI 通过可选的只读 `WorkspaceRootProvider` 获取工作区根目录；真实 `loop.Runner` 实现该能力，测试 fake 未实现时使用空根目录并保留原始输入。MCP 自己的仓库相对路径不拼接 Paw 工作区根目录。

### 3. 主行渲染与宽度优先级

`renderCompactToolSummary` 改为消费显示目录产生的 `displayName`、`target` 和 `status`。渲染顺序为：

```text
焦点标记 → 状态图标 → 工具名 → : → 目标 → 状态色块
```

宽度不足时按以下顺序压缩：

1. 保留状态词和状态图标；
2. 保留工具名和冒号；
3. 截断目标，优先保留相对路径的末端可辨识部分；
4. 最后省略耗时，但不省略 `完成`、`运行中` 或 `出错`。

所有截断以终端 cell 宽度计算，不能使用字节长度。中文、日文、emoji、ANSI 样式和嵌套背景都必须保持单行不溢出。running 的耗时继续复用现有按秒失效机制，完成后的耗时冻结。

展开、折叠、hover、工具检查模式、拖拽选择和固定布局行为保持不变。展开结果不需要把完整原始名称放回主行；完整结果仍在现有详情区域中查看。

### 4. 状态样式

新增语义化状态色块样式，使用当前主题的颜色管理器：

- success：默认主题绿色；
- running：默认主题橙色；
- error：默认主题红色。

状态样式必须为终端可实现的前景色、背景色、粗体和水平 cell padding，不使用圆角或依赖浏览器的视觉效果。聚焦工具行时保留选择态可读性；若选择背景与状态背景冲突，状态色块仍保留语义色，并使用主题前景色保证对比度。

## 错误处理

- 输入 JSON 非法：保留短工具名，并跳过无法解析的目标。
- 缺少 `file_path` / `path`：使用动作描述或短工具名。
- 路径无法解析：保留原始相对路径；不显示字段名。
- 显示目录没有映射：使用服务器短名加规范化工具短名。
- 未知状态：使用低对比度文本，不误显示为成功。
- `NO_COLOR=1`：移除背景和前景装饰，保留纯文本状态。
- 显示函数不执行外部命令、不读取 Git、不修改输入 JSON。

## 测试与验收

新增或更新 Bubble 测试覆盖：

- 内置 MCP 别名：`CodeGraph: 读取页面`、`CodeGraph: 搜索`；
- 未知 MCP fallback：`CodeGraph: explore`；
- 原生工具显示：`LS: <相对路径>`、`Edit: <相对路径>`；
- `file_path` 真实值、工作区绝对路径转相对路径、空路径和非法路径回退；
- 目标中包含中文、日文、emoji 和长相对路径时的 cell 宽度截断；
- 完成、运行中、出错色块，以及 `NO_COLOR=1`；
- 宽度 `120 / 80 / 40 / 20` 下单行不溢出；
- tool-use ID 匹配、名称回退匹配、展开/折叠、hover 和工具检查模式不回归；
- running 耗时按秒刷新，完成后冻结。

验证命令：

```sh
go test ./internal/ui/bubble -count=1
go test ./... -count=1
go vet ./...
git diff --check
NO_COLOR=1 go test ./internal/ui/bubble -count=1
```

## 范围外

- 不改变工具调用协议、MCP namespace、工具注册和结果匹配语义；
- 不新增工具取消、重试、后台任务或新的交互模式；
- 不改变 Markdown、状态栏、Token Ripple、输入框和工作树 chip；
- 不把完整 MCP 原始名称放回主行；
- 不引入外部字体、Web UI 或终端无法实现的装饰。
