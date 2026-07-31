# Edit 工具 Claude Code 行为对齐设计

日期：2026-07-31

## 1. 背景与目标

Paw 已有原生 `Edit` 工具，当前支持：

- 对工作区内已有文件进行精确字符串替换。
- 默认要求 `old_string` 在文件中唯一出现。
- `replace_all=true` 时替换全部匹配。
- 多行字符串替换。
- 工作区路径边界检查。
- 原子写入。
- 当存在 Read 基线时检测文件是否已被外部修改。
- TUI 中展示 Edit/Write 的 diff 预览及 `+N -M` 摘要。

当前实现与目标行为仍存在三个关键差距：

1. `ReadStateStore.Verify` 在目标文件从未被 Read 时会放行，因此 Edit 并不真正要求“先 Read、后 Edit”。
2. `atomicWriteFile` 固定将文件权限设为 `0644`，编辑可执行脚本等文件时会破坏原权限。
3. TUI 主要使用工具输入中的局部 `old_string` / `new_string` 推导 diff；`replace_all` 或多处变化时，这不一定等于文件的真实前后差异。

本次采用“分层对齐”方案，在不改变通用 `tool.Tool` 接口、不重构整个文件工具体系的前提下，对齐 Edit 的核心执行契约、模型使用约束、结果反馈、TUI 展示和回归测试。

目标是：

- Edit 必须在当前 Read 状态中存在目标文件基线后才能执行。
- Read 后文件被外部修改时，Edit 必须拒绝 stale write，并要求重新 Read。
- 保持精确、唯一匹配与 `replace_all` 语义。
- 编辑已有文件时保留其权限位。
- TUI 使用真实的完整文件前后内容生成 diff。
- 模型可见工具结果继续保持简洁，不携带完整文件内容。
- 继续支持工作区相对路径和工作区内绝对路径。

## 2. 范围

### 2.1 包含

- `ReadStateStore` 的严格 Read 前置验证能力。
- `EditTool` 的验证顺序、错误信息和文件权限保留。
- Runner 对内置 Edit/Write 文件变更前后内容的捕获与 UI 传递。
- TUI 对真实文件快照的优先使用及旧数据回退。
- Edit 工具描述中明确“编辑前必须 Read”的模型使用约束。
- 文件工具、Runner、TUI diff 和回归测试。

### 2.2 不包含

- 不要求 `file_path` 必须为绝对路径；Paw 继续兼容工作区相对路径。
- 不增加模糊匹配、自动缩进修正、自动换行符转换或空白容错。
- 不实现跨进程文件锁或平台相关 compare-and-swap 写入。
- 不保留所有权、ACL、扩展属性或文件时间戳；本次只保留文件权限位。
- 不把完整文件前后内容放进模型可见的工具结果。
- 不将 Edit 和 Write 重构为通用文件变更框架。
- 不改变 MCP 中同名 `Edit`/`Write` 工具的行为。

## 3. 总体架构

本次改造分为三个边界清晰的层次：

1. **验证与变更层（`internal/tool/file`）**
   - 验证 Read 前置状态和 stale write。
   - 校验精确匹配、唯一性和 `replace_all`。
   - 计算完整新文件内容。
   - 保留权限并原子写入。

2. **变更快照传递层（Runner）**
   - 仅对 Paw 内置 Edit/Write 识别文件变更调用。
   - 工具执行前捕获完整 `Before`。
   - 工具成功后捕获完整 `After`。
   - 快照只进入 UI 事件，不进入模型可见工具结果。

3. **展示层（Bubble TUI）**
   - 优先使用真实 `Before`/`After` 生成结构化 diff。
   - 由真实 diff 计算 `Added`/`Removed` 数量。
   - 快照缺失时回退到现有局部字段推导逻辑。

推荐数据流：

```text
Read → 记录目标文件内容哈希
  ↓
Edit.Run → 严格验证 → 精确替换 → 保留权限并原子写入
  ↓
Runner → 捕获 Before/After（仅 UI 可见）
  ↓
toolCallMsg / toolResultMsg
  ↓
Bubble TUI → 真实完整文件 diff、行数摘要和展开详情
```

通用 `tool.Tool` 接口保持不变：

```go
type Tool interface {
    Name() string
    Description() string
    Run(ctx context.Context, input json.RawMessage) (string, error)
    InputSchema() json.RawMessage
}
```

## 4. Read 状态与严格验证

### 4.1 保留宽松验证

现有 `ReadStateStore.Verify(path, current)` 的语义保持不变：

- 有基线且内容不同：返回 stale-write 错误。
- 有基线且内容相同：通过。
- 没有基线：通过。

保留该方法是为了避免隐式改变 Write 等现有调用者的行为。

### 4.2 新增严格验证

新增独立方法：

```go
func (s *ReadStateStore) VerifyRequired(path string, current []byte) error
```

其行为为：

| 状态 | 结果 |
|---|---|
| 没有目标路径的 Read 基线 | 失败，要求先 Read |
| 有基线且当前内容一致 | 通过 |
| 有基线但当前内容不同 | 失败，要求重新 Read |

`EditTool` 必须使用 `VerifyRequired`，而不是宽松 `Verify`。

如果 `EditTool.ReadState` 未配置，也视为没有可验证的 Read 基线，Edit 应失败，而不是绕过严格契约。生产注册流程必须继续为内置 Edit 注入共享的 `ReadStateStore`。

### 4.3 基线更新

- `Read` 成功读取完整文件后记录基线。
- Edit 成功写入后通过 `RecordAfterWrite` 将基线更新为新内容。
- 因此同一会话中，成功 Edit 后可继续对最新内容执行下一次 Edit，不需要机械地再 Read 一次。
- 任一失败分支不得更新基线。

### 4.4 错误信息

未 Read 时：

```text
file must be read before editing: core/utils.py; use Read first
```

Read 后文件被修改时：

```text
file has been modified since last read: core/utils.py; read it again before editing
```

用户可见错误路径统一使用工作区相对、斜杠规范化的展示路径；工作区外路径仍由路径安全校验提前拒绝。

## 5. Edit 执行契约

### 5.1 输入与路径

输入结构保持不变：

```go
type editInput struct {
    FilePath   string `json:"file_path"`
    OldString  string `json:"old_string"`
    NewString  string `json:"new_string"`
    ReplaceAll bool   `json:"replace_all,omitempty"`
}
```

路径规则：

- 接受工作区相对路径。
- 接受工作区内绝对路径。
- 拒绝逃逸工作区的路径。
- 目标必须是已有文件；Edit 不负责创建新文件。

### 5.2 执行顺序

`EditTool.Run` 必须按以下顺序执行：

1. 检查 context 是否已取消。
2. 解码 JSON。
3. 校验 `file_path` 非空。
4. 校验 `old_string` 非空。
5. 校验 `old_string != new_string`。
6. 将路径解析到工作区内。
7. 读取目标文件内容和文件元数据。
8. 使用 `VerifyRequired` 检查 Read 基线。
9. 统计 `old_string` 的精确匹配次数。
10. 校验零匹配与非唯一匹配。
11. 计算完整更新后内容。
12. 使用原权限位原子写入。
13. 更新 Read 基线。
14. 返回简洁成功结果。

在步骤 12 完成之前发生的任何错误，都不得改变目标文件。

### 5.3 精确匹配

匹配继续使用完整字符串的字节级精确语义：

- 空格、Tab、缩进、换行和 CRLF/LF 差异均有意义。
- 不进行大小写折叠。
- 不自动补齐周边上下文。
- 不做正则匹配。
- 多行 `old_string` 可以跨行匹配。

零匹配错误：

```text
old_string not found in core/utils.py; it must match the file contents exactly
```

多处匹配且未设置 `replace_all`：

```text
old_string matches 3 locations in core/utils.py; set replace_all=true or include more surrounding context to make it unique
```

### 5.4 唯一匹配与 replace_all

- `replace_all=false` 或省略时，匹配数必须恰好为 1。
- 匹配数大于 1 时必须失败，不允许默认替换第一处。
- `replace_all=true` 时替换所有非重叠匹配。
- 成功结果中的替换次数必须等于实际替换数量。

成功结果继续使用简洁文本，例如：

```text
edited core/utils.py (1 replacement)
```

或：

```text
edited core/utils.py (3 replacements)
```

完整文件内容不得加入该模型可见结果。

### 5.5 工具描述

Edit 的工具描述必须明确告知模型：

- 目标文件必须先通过 Read 读取。
- `old_string` 必须与文件内容精确匹配。
- 默认必须唯一；`replace_all=true` 才可替换全部匹配。

schema 字段保持不变，避免破坏现有调用格式。

## 6. 原子写入与权限保留

### 6.1 权限来源

Edit 在读取目标文件时同时通过 `os.Stat` 获取权限位：

```go
mode := info.Mode().Perm()
```

写入临时文件时应用该权限，再执行同目录 rename。

例如原文件为 `0755`，Edit 后仍必须为 `0755`；原文件为 `0600`，Edit 后仍必须为 `0600`。

### 6.2 原子写入接口

原子写入辅助函数需要能够接收目标权限。可采用显式 mode 参数或区分“保留已有权限”和“使用默认权限”的等价接口，但行为必须明确：

- Edit 已有文件：使用原文件权限。
- Write 新建文件：继续使用默认 `0644`。
- Write 覆盖已有文件：保持实现前已经由测试锁定的 Write 契约，不因本次 Edit 对齐而被无意改变。

如果修改 `atomicWriteFile` 函数签名，必须同步更新所有调用方和原子写入测试。

### 6.3 非目标属性

本次不承诺保留：

- owner/group。
- ACL。
- extended attributes。
- creation/modification timestamp。
- hard-link 关系。

原子 rename 本身可能改变 inode；这属于当前原子写入策略的既有特征。

## 7. Runner 文件变更快照

### 7.1 快照结构

Runner 到 UI 的事件数据新增可选文件变更快照：

```go
type FileMutationSnapshot struct {
    Before string
    After  string
}
```

字段名称可根据现有事件结构调整，但必须表达完整文件的修改前后内容，而不是局部 replacement。

### 7.2 内置工具识别

快照捕获只应用于 Paw 内置文件工具实例：

- 内置 `*file.EditTool`。
- 内置 `*file.WriteTool`。

不得只根据 `Name() == "Edit"` 或 `Name() == "Write"` 判断，否则 MCP 或其他命名空间中的同名工具会被误识别。

识别方式应基于具体工具类型、显式内部能力接口或注册时元数据；不引入面向模型的新 schema 字段。

### 7.3 捕获规则

- 工具执行前，根据已经安全解析的目标路径读取 `Before`。
- Edit 的目标必须存在，因此正常情况下 `Before` 有值。
- Write 新建文件时，`Before` 表示不存在/空基线；必须与“已有空文件”在内部状态上可区分，以便未来需要时正确表达，但当前 diff 都可按空内容处理。
- 工具成功后读取 `After`。
- 工具失败时不生成成功变更快照，也不展示成功 diff。
- `Before` 或 `After` 捕获失败时，不得把已经成功的工具执行改判为失败；UI 退化为现有推导逻辑或简洁结果。
- 捕获路径必须复用文件工具的工作区边界逻辑，不能读取工作区外文件。

### 7.4 模型与 UI 隔离

`Before`/`After` 只用于本地 UI 事件和 transcript 展示：

- 不拼接到 `Tool.Run` 返回字符串。
- 不加入发回模型的 `tool_result` 内容。
- 不写入额外临时文件。
- 会话持久化若记录 UI tool transaction，可保存可选快照；恢复旧会话时必须兼容字段缺失。

若现有 Runner 事件只能在工具调用时携带 `OldContent`，应将其演进为可同时传递调用前后内容的可选结构，而不是让 UI 重新读取当前磁盘并猜测历史状态。

## 8. TUI diff 与展示

### 8.1 数据优先级

Bubble TUI 生成文件变更 diff 时按以下优先级取数据：

1. Runner 提供的完整 `Before`/`After` 快照。
2. 旧会话或快照缺失时，使用现有 `old_content` / `new_content` 字段。
3. Edit 继续回退到 `old_string` / `new_string`。
4. Write 继续回退到原有 content 和 oldContent 逻辑。
5. 无法得到有效前后内容时，只展示简洁工具结果，不伪造 diff。

### 8.2 真实 diff

已有 `structuredDiff`、`diffCounts` 和 `renderDiffPreview` 继续作为展示核心。输入改为完整文件行序列后：

- 单处替换统计真实的新增/删除行。
- `replace_all` 跨多处替换时统计所有变化。
- 多行添加、删除和替换的行号基于完整文件。
- 展开详情保留现有上下文窗口、折叠标记和预览上限。
- 完全相同的内容不显示 `+0 -0`。

### 8.3 折叠摘要

沿用现有文件工具轨迹样式，展示目标路径和真实变更数量。例如：

```text
Update(core/utils.py)
└─ Added 3 lines, removed 1 line
```

具体大小写和单复数应遵循现有 UI 风格；关键要求是计数来源于真实完整 diff，而不是 replacement 输入片段。

### 8.4 错误展示

Edit 失败时：

- tool transaction 标记为 error。
- 自动展开或按现有错误工具规则展示错误详情。
- 不展示成功的 Added/Removed 摘要。
- 不展示推测性 diff。
- 错误信息应明确指出需要先 Read、重新 Read、精确匹配或增加周边上下文。

## 9. 兼容性与迁移

### 9.1 路径兼容性

虽然 Claude Code 的公开契约倾向使用绝对路径，Paw 本次明确保留：

- 工作区相对路径为正常支持路径。
- 工作区内绝对路径同样支持。
- 工作区边界检查不放宽。

这是有意保留的 Paw 兼容差异，不视为未完成项。

### 9.2 结果兼容性

模型可见成功结果继续为简洁字符串，不迁移为携带完整内容的 JSON。现有模型循环和工具结果解析不需要理解新协议。

UI 事件新增的快照字段必须为可选字段：

- 新会话优先使用快照。
- 旧会话无快照时安全回退。
- 非文件工具不受影响。
- 同名 MCP 工具不进入内置文件快照逻辑。

### 9.3 Write 兼容性

本次允许 Runner 同时完善 Write 的真实 diff 快照，但不改变 Write 的模型使用契约、输入 schema、Read 前置规则或文件创建语义。

任何原子写入辅助函数调整都必须由 Write 现有测试锁定行为，避免为修复 Edit 权限而改变 Write。

## 10. 并发与竞态边界

严格 Read 验证发生在 Edit 读取当前内容后、实际原子 rename 前。两者之间仍存在短暂的 TOCTOU 窗口：其他进程可能在验证后、rename 前修改文件。

本次保证：

- 对 Edit 本次读取到的内容执行 Read 基线比较。
- 写入采用同目录临时文件和原子 rename，避免部分文件。
- 成功后 Read 基线更新为新内容。

本次不保证：

- 跨进程锁定目标文件。
- inode 或版本号 compare-and-swap。
- 网络文件系统上的强一致原子语义。

这些能力需要平台相关实现，超出本次范围。

## 11. 错误处理

需要覆盖并保持文件不变的错误包括：

- context 已取消。
- JSON 非法。
- `file_path` 为空。
- `old_string` 为空。
- `old_string == new_string`。
- 路径逃逸工作区。
- 目标文件不存在或不可读。
- 未先 Read。
- Read 后内容被外部修改。
- `old_string` 精确匹配数为 0。
- 匹配数大于 1 且未设置 `replace_all`。
- 获取文件元数据失败。
- 创建、写入、chmod、关闭或 rename 临时文件失败。

失败时必须满足：

- 不更新 Read 基线。
- 不生成成功文件变更快照。
- 不展示成功 diff 或 Added/Removed 摘要。
- 临时文件尽力清理。

## 12. 测试策略

### 12.1 ReadStateStore 测试

新增：

- `VerifyRequired` 在无基线时失败。
- Read 后内容一致时通过。
- Read 后内容变化时失败。
- `RecordAfterWrite` 后以新内容为基线。
- 不同路径状态相互隔离。
- 并发 Record/Verify/VerifyRequired 在 race detector 下安全。

保留：

- 宽松 `Verify` 在无基线时继续通过。
- 现有 Write 等调用者不因新增严格方法而改变。

### 12.2 EditTool 测试

覆盖：

- 未先 Read 时拒绝 Edit，文件不变。
- Read 后唯一匹配替换成功。
- 成功 Edit 后可基于更新后的基线继续 Edit。
- 外部修改后 Edit 失败。
- 重新 Read 后可以再次 Edit。
- 零匹配失败并提示精确匹配。
- 多处匹配且未设置 `replace_all` 时失败。
- `replace_all=true` 替换所有匹配并返回正确数量。
- 多行及带缩进字符串精确匹配。
- 空格、Tab、CRLF/LF 差异不会被自动修正。
- `old_string == new_string` 失败。
- 空参数和非法 JSON 失败。
- 缺失文件和工作区外路径失败。
- 相对路径和工作区内绝对路径成功。
- `0755`、`0600` 等原权限在成功 Edit 后保持不变。
- 每个失败分支均不改变文件、不更新基线。
- 成功结果保持简洁且不包含完整文件内容。

现有只测试匹配逻辑的用例，应在调用 Edit 前显式通过 Read 工具或测试辅助方法建立 Read 基线，避免因新前置条件失去原测试焦点。

### 12.3 原子写入与 Write 回归测试

- 临时写入和 rename 成功。
- 指定权限正确应用。
- 错误路径清理临时文件。
- Write 新建文件仍使用既有默认权限。
- Write 覆盖文件的权限行为与实施前测试锁定结果一致。

### 12.4 Runner 快照测试

- 内置 Edit 成功时传递完整 `Before` 和 `After`。
- `replace_all` 时快照包含整份文件的全部变化。
- 内置 Write 成功时传递真实前后内容。
- Edit/Write 失败时不产生成功快照。
- After 捕获失败时工具成功状态不被改写。
- 非文件工具不触发文件快照读取。
- MCP 或其他同名 Edit/Write 工具不被误判为内置工具。
- 路径解析失败时安全退化，不读取工作区外文件。
- 快照不进入模型可见 `tool_result`。

### 12.5 TUI 与 diff 测试

- 单处替换显示正确 `+N -M`。
- `replace_all` 跨多处变化时统计全部新增/删除行。
- 多行添加、删除和替换的行号正确。
- 快照优先于局部 `old_string/new_string`。
- 快照缺失时回退到旧推导逻辑。
- 相同内容不显示 `+0 -0`。
- 展开详情使用上下文窗口和折叠标记。
- Edit 错误自动按现有错误规则展示，且无成功 diff。
- 恢复旧会话时缺少快照字段不会失败。
- 非 Edit/Write 工具的 transcript 渲染不受影响。

### 12.6 回归命令

实现完成后运行：

```bash
go test ./internal/tool/file
go test ./internal/loop
go test ./internal/ui/bubble
go test ./...
go test -race ./internal/tool/file ./internal/loop
```

如全量测试存在与本次修改无关的既有失败，应明确记录，不通过修改无关模块掩盖。

## 13. 验收标准

本设计完成时必须满足：

1. 当前会话未建立目标文件 Read 基线时，内置 Edit 必定失败。
2. Read 后文件被外部修改时，Edit 必定失败并要求重新 Read。
3. 成功 Edit 后 Read 基线更新，允许针对新内容继续 Edit。
4. `old_string` 继续按字节精确匹配，默认必须唯一。
5. `replace_all=true` 替换全部匹配并返回正确替换数量。
6. 编辑已有文件不改变其权限位。
7. TUI diff 优先来源于真实完整文件的 `Before`/`After`。
8. `replace_all` 的 Added/Removed 统计覆盖全部真实变化。
9. 模型可见工具结果保持简洁，不携带完整文件内容。
10. 相对路径、工作区内绝对路径和工作区安全边界保持兼容。
11. MCP 或其他同名工具不会进入内置文件快照逻辑。
12. 旧会话缺少快照字段时安全回退。
13. Write 的既有模型契约和文件行为不被无意改变。
14. 相关文件工具、Runner、TUI 和全量回归测试通过。
15. 实现过程不覆盖工作区中与本设计无关的未提交修改。
