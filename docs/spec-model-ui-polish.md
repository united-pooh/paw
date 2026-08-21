# Spec：模型向导确认面板与切换输出美化（主角式面板 + 圆角状态卡）

| 项 | 值 |
|---|---|
| 状态 | Approved（2026-08-21 头脑风暴定稿，浏览器原型 A 方案 + 圆角边框卡方案胜出） |
| 日期 | 2026-08-21 |
| 范围 | internal/ui/bubble（model_wizard.go、command_helpers.go、transcript.go） |
| 关联 | docs/spec-actor-runtime-refactor.md（分层不变，仅 UI 表现层） |

---

## 1. 问题陈述

模型选择链路的三类文本输出停留在原始 key=value 平铺：

1. **Confirm model 面板**（`renderModelConfirmStep`）：字段全部平铺、空字段挂着空冒号
   （`path:`、`key env:`）、`models:` 一行逗号平铺全部可选模型导致超宽溢出、快捷键提示
   为英文整句且与数据混排；
2. **切换成功后的 transcript 条目**（向导应用 ×2、`/model <id>` 直切 ×2 共 4 处调用点）：
   输出 `id=… provider=… base=… path=… model=… context=… retries=… key=…` 单行键值串，
   空值挂空等号、内部标识 `id=` 对用户无意义、换行折断后不可扫读；
3. 与任务完成卡（`renderTaskCompletionCard`）相比缺乏统一的「成功反馈」视觉语言。

## 2. 目标 / 非目标

**目标**
1. G1 确认面板改为「主角式」布局：选中模型大字加粗当标题，provider/base 退为副标题，
   仅列非空关键参数；
2. G2 四处模型切换成功路径统一输出结构化 `<model …>` 块，transcript 渲染为圆角绿框
   状态卡（复用任务完成卡的视觉语言与宽度算法）;
3. G3 空字段一律隐藏；`models:` 平铺删除，仅在还有其他可选模型时显示计数提示。

**非目标**
1. N1 `/model status` 的诊断输出（catalog/discovery 明细）本轮不动——它是调试视图且被
   config_center 测试断言依赖（列为后续项）；
2. N2 失败路径维持现有 `entryError` 红色条目，不做 ✗ 卡片变体；
3. N3 不改颜色主题、不改向导交互流程（按键语义不变：enter/b/esc）。

## 3. 设计

### 3.1 Confirm 面板（`renderModelConfirmStep`，model_wizard.go）

目标布局（40 列窄面板下同样成立，长 URL 由 `renderModalPanel` 的 fit 兜底截断）：

```
Confirm model · openrouter          ← wizardTitleStyle + StatusMuted provider 后缀

stealth/ox-alpha                    ← 加粗主角行（selectedModelNameOr(cfg.Model)）
https://openrouter.ai/api/v1/responses   ← StatusMuted 副标题（base+path 拼接）

context    131072 tokens            ← 标签列对齐（StatusMuted），值默认色
retries    3
key env    OPENROUTER_API_KEY

另有 12 个可选模型                   ← StatusMuted，仅当 AvailableModels 数 > 1
enter 应用 · b 返回 · esc 取消       ← 键名绿色（colorWorktreeClean），说明 StatusMuted

✗ 错误信息…                          ← wizard.err 非空时保留现有 labelErrorStyle 行
```

规则：
- 标题行 provider 后缀仅在 `cfg.Provider != ""` 时拼接；
- 副标题 = `cfg.APIBaseURL + cfg.APIPath`（path 非空才拼）；base 为空则整行省略；
- 明细行集合：`context`（`model.EffectiveContextLimitTokens(cfg) > 0` 时，值 `<n> tokens`）、
  `retries`（恒显，值 `cfg.RetryCount`）、`key env`（`cfg.APIKeyEnvName != ""` 时）；
  标签列宽 = 最长标签长度 + 2；
- `models:` 平铺删除；`len(model.AvailableModels(cfg)) > 1` 时显示
  `另有 <N-1> 个可选模型`；
- 快捷键提示改为 `enter 应用 · b 返回 · esc 取消`（键名绿色、说明中文灰色），
  按键处理逻辑不变。

### 3.2 切换成功块与状态卡

**块格式**（机器可读，仿 `<task>` 完成块模式；4 处调用点统一经新 helper
`formatModelSwitchBlock(cfg model.Config) string` 生成）：

```
<model provider="openrouter" model="stealth/ox-alpha" base="https://openrouter.ai/api/v1" path="/responses" context="131072" retries="3" key_env="OPENROUTER_API_KEY">
</model>
```

- 属性固定顺序：provider、model、base、path、context、retries、key_env；
- **空值属性整个省略**（不再出现 `key=""`）；`context <= 0` 省略；`retries` 恒输出；
- 属性值做 XML 实体转义（新增 escape helper，与现有 `unescapeTaskBlockAttr` 对偶）；
- 内部标识 `id=` 不再输出。

**检测与解析**（transcript.go，紧邻现有 task block 代码）：
- `isModelCardBlock(body)`：TrimSpace 后 `HasPrefix "<model "` 且 `HasSuffix "</model>"`；
- `parseModelCardBlock(body)`：复用 `taskBlockAttrPattern` + `unescapeTaskBlockAttr` 提取字段。

**卡片渲染**（`renderModelSwitchCard(body string, width int) string`）：

```
╭──────────────────────────────────────╮
│ ✓ 模型已生效                          │
│                                      │
│ stealth/ox-alpha                     │
│ openrouter · 131072 ctx · retry ×3   │
│                                      │
│ base     https://openrouter.ai/api/v1│
│ path     /responses                  │
│ key env  OPENROUTER_API_KEY          │
╰──────────────────────────────────────╯
```

- 边框/宽度算法照抄 `renderTaskCompletionCard`：RoundedBorder +
  `colorWorktreeClean` 绿框、Padding(0,1)、styleWidth/bodyWidth 推导、
  `truncateStyledCellLine` + `fitStyledCellLine` 逐行适配；
- 内容：绿色加粗标题 `✓ 模型已生效`；空行；加粗模型名；StatusMuted meta 行
  `provider · <ctx> ctx · retry ×<n>`（各段非空才拼，整行空则省略）；空行（仅有明细行时）；
  明细行 base/path/key_env（非空才显示，标签列对齐同 3.1）。

### 3.3 渲染集成点

`renderEntryAt`（transcript.go）在现有 `<task>` 卡片分支之后新增分支：

```go
if entry.kind == entrySystem && isModelCardBlock(entry.body) {
    card := renderModelSwitchCard(entry.body, bodyWidth)
    if card == "" {
        return ""
    }
    return indentLines(card, transcriptEntryGutter)
}
```

与任务卡一致：整块替换普通渲染，不显示 `model` 标签（卡片标题自带 ✓ 状态标记）。

### 3.4 调用点改造（4 处）

| 位置 | 现状 | 改造 |
|---|---|---|
| model_wizard.go:246（catalog 向导应用） | `id=… provider=… …` 键值串 | `formatModelSwitchBlock(cfg)` |
| model_wizard.go:276（profile 向导应用） | 同上 | 同上 |
| command_helpers.go:58（`/model <id>` catalog 直切） | `id=… provider=… model=…` | 同上 |
| command_helpers.go:103（profile 命令切换） | `provider=… models=… …` | 同上 |

`/model status`（command_helpers.go:43）保持现状（N1）。

## 4. 决策记录

| # | 决策 | 要点 |
|---|---|---|
| ADR-1 | 结构化块而非直接存渲染文本 | body 保持机器可读，`/export`、测试、未来重渲染都吃同一格式；与 `<task>` 块先例一致 |
| ADR-2 | 检测走 body 前后缀而非新 entry kind | 复用 isTaskCompletionBlock 先例，不动 transcriptEntry 结构与持久化协议 |
| ADR-3 | 只做成功卡，不做失败卡 | 失败路径已有 entryError 红色条目，双通道语义清晰 |
| ADR-4 | models 平铺改计数提示 | 确认时刻用户只关心将生效的配置；完整列表在模型选择步可见 |
| ADR-5 | `/model status` 缓改 | 诊断视图 + config_center 测试依赖，收益低风险高，列为后续项 |

## 5. 影响面与测试计划

**行为影响**
- `/export` 导出的模型切换条目原文从键值串变为 `<model …>` 块（可接受，见 ADR-1）；
- transcript 中模型切换条目不再显示 `model` 标签行（卡片标题替代）。

**测试**
1. 更新 bubble_test.go:645 断言：`provider=gateway` → 新块格式
   （如 Contains `provider="gateway"` 或 `isModelCardBlock(...) == true`）；
2. 新增 model_card 单测：
   - `isModelCardBlock` 真/假边界（前缀匹配、非块文本、前后空白）；
   - `parseModelCardBlock` 字段提取 + 实体反转义；
   - `formatModelSwitchBlock` 空值属性省略、转义、属性顺序；
   - `renderModelSwitchCard` 含 `✓ 模型已生效` 与模型名、空字段行不出现、
     输出宽度不超过给定 width；
3. 新增 confirm 面板单测：空 path/key env 行不出现；`models:` 平铺不出现；
   多模型时出现 `另有 N 个可选模型`；hint 行出现；
4. 回归：`go test ./...` 全绿。

## 6. 验收标准

1. 40 列窄终端下打开向导确认步骤：无内容溢出、无空字段行、提示为中文；
2. 向导应用、`/model <id>` 直切两条路径在 transcript 中均渲染为绿框 ✓ 卡片，
   视觉与任务完成卡一致；
3. 配置缺省（无 path/key env/context）时不出现空行占位与空等号；
4. `go test ./...` 全绿，`go vet ./...` 无新增告警。

## 7. 后续项（deferred）

- `/model status` 诊断输出改版（需同步调整 config_center 相关断言）；
- 确认面板内嵌预览卡方案（原型 C，窄终端适配验证后再议）。
