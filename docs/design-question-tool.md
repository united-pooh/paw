# question 工具设计记录（原 Select 改名 + 批量提问）

日期：2026-08-15

## 背景

原阻塞式 `Select` 工具一次调用只能问一个问题。实测中 agent 常有多个问题
需要连续澄清，逐次调用既慢又让模型多轮往返；同时工具名 `Select` 与其
「向用户提问」的语义不够直白。本次将工具改名为 `question`，并支持一次
调用携带多个问题（批量提问）。

## 决策（经 /grilling 确认）

1. **改名**：`Select` → `question`（小写）。旧会话 transcript 中已存的
   `Select` 调用不做别名兼容，渲染退化为普通工具行。
2. **纯 questions 数组**：输入唯一入口是 `questions: [...]`（breaking），
   单题也必须写成数组；结果按输入顺序返回数组，位置对齐，不引入题 id。
3. **整批原子取消**：任一题被用户取消，整批返回全部 `cancelled: true`，
   已作答的题目一并作废，不返回部分结果。
4. **行为模式**写入全局 `~/.paw/agent.md`：需要提问时用 question 工具，
   多个问题一次传。

## 协议

输入：

```json
{
  "questions": [
    {
      "prompt": "…",
      "mode": "single" | "multiple",
      "options": [{"id": "…", "label": "…", "description": "…"}],
      "initial_selected_id": "…",       // 可选
      "initial_selected_ids": ["…"],    // 可选
      "min_select": 0,                  // 可选
      "max_select": 1                   // 可选
    }
  ]
}
```

输出（与输入顺序对齐；任一题取消则全部为 cancelled）：

```json
{
  "results": [
    {"cancelled": false, "selected_options": [{"id": "…", "label": "…"}]}
  ]
}
```

## 实现要点

- `internal/tool/select/`：`decodeInput` 解码 `questions` 数组并逐题
  复用原有校验（错误带 `questions[i]:` 前缀）；`Tool.Run` 将整批问题
  组装为一次 `Broker.Ask`，UI 在同一个 dock 内逐页展示，确认页一次提交
  完整结果；任一题取消即整批置 cancelled；返回 `BatchResult`
  （`{"results":[...]}`）。
- `Request` 携带完整 `Questions`；页面索引与每题选择状态由 dock 内部维护，
  不再依赖 `BatchIndex`/`BatchSize` 作为页面元数据。
- UI dock：问题页之间用左右方向键切换，边界停留；第 n 个问题右键进入
  第 n+1 个确认页。问题页上下遍历选项、Custom option、Chat about this；
  空格只选中，单题兼容模式下 Enter 仍可直接提交，多题 Enter 只选中。
  确认页只读展示 Q1/Q2/… 与答案，上下切换默认聚焦的 Submit/Cancel，
  空格或 Enter 执行当前项，左键返回最后一个问题页。
- UI 渲染：transcript 摘要由「selected N options」改为「answered N
  questions」/「cancelled」；展开详情按 `Q1/Q2/…` 分块展示每题 prompt 与
  所选答案。`formatToolCallBody` 的 question 分支只显示题目数量，隐藏
  选项 payload。
- 选中态统一使用 `SelectionNormal`、`SelectionSelected`、
  `SelectionFocused`、`SelectionFocusedSelected` 四个主题令牌；问题页、
  Custom、Review 的 Submit/Cancel 共用同一状态语义。

## 不做的事

- 不给每题引入 id（顺序对齐已够用）。
- 不兼容旧 `Select` 名称与旧单题输入形状。
- 不限制批量题目数量上限（dock 有进度指示，esc 可整批取消）。
