# Bash 工具运行中断与实时输出设计

- 日期：2026-08-07
- 状态：已批准设计，待实现计划

## 背景

当前工具运行期间，`Ctrl+C` 无法可靠地先停止当前工具；运行中的 Bash 也无法在 transcript 中查看实时 stdout/stderr。目标是在不改造所有工具执行接口的前提下，增加 Bash 专用的工具级取消和实时输出能力。

## 目标与非目标

### 目标

1. 第一次 `Ctrl+C` 只取消当前运行中的 Bash。
2. 第二次 `Ctrl+C` 取消当前整轮对话。
3. Bash 运行期间在 transcript 中实时展示 stdout 与 stderr。
4. Bash 被中断后保留已收到的部分输出，并明确标记为“已中断”。
5. 终止 Bash 进程组及其子进程，避免后台遗留进程。
6. 保持其他工具的现有一次性结果行为不变。

### 非目标

- 本次不把所有工具统一重构为流式工具。
- 本次不为 MCP 或子智能体增加实时 stdout 面板。
- 实时输出仅用于 UI，不逐片写入模型历史。

## 推荐架构

采用 Runner 中心化的工具控制器：

- 每轮对话保留 turn context。
- 当前 Bash 执行创建独立 tool context，并由 Runner 保存当前工具 ID、名称和取消函数。
- UI 第一次收到 `Ctrl+C` 时调用 `Runner.CancelCurrentTool()`；没有运行中的 Bash时直接取消 turn。
- 第二次 `Ctrl+C` 取消 turn context，终止模型流、工具调用链和后续 continuation。
- 工具控制器在工具完成、失败或中断后清理当前句柄；清理操作必须幂等。

取消通过 `context.Context` 传播。Bash 执行器需要使用可终止进程组的方式启动命令，取消时同时停止命令及其子进程。

## Bash 事件与数据流

Bash 专用执行路径产生以下事件：

```text
tool.started
  -> tool.output(stream=stdout|stderr, chunk)
  -> tool.output(...)
  -> tool.completed | tool.failed | tool.interrupted
```

事件至少包含工具调用 ID、流类型和文本片段。stdout 与 stderr 保持来源区分，由 UI 使用标签或颜色区分 stderr。Runner 将事件转发为 UI 工具事件；工具结束或中断时仍向模型提交一次最终工具结果，并明确携带中断语义，不能被解释为成功。

非 Bash 工具继续使用现有 `Run(ctx, input)` 一次性接口。

## Transcript 展示与交互

- Bash 启动即创建运行中的工具块。
- 运行期间默认展开，随 stdout/stderr 增量刷新。
- 用户位于该工具块底部时自动跟随新输出；用户已滚动离开底部时保持其位置。
- 用户可以手动折叠运行中的工具块，输出仍继续接收；再次展开显示最新内容。
- stderr 显示来源标识并使用错误色，避免与 stdout 混淆。
- 增加输出上限，超限时保留尾部并显示“前面的输出已截断”。
- 工具完成、失败或中断后保留输出并恢复既有工具块折叠规则。
- 中断后的工具块保留部分输出，状态明确显示“已中断”。

实时输出不逐片写入模型历史；最终结果仍遵循现有工具结果和历史持久化机制。

## Ctrl+C 状态机

1. 当前 Bash running：第一次 `Ctrl+C` 取消 tool context，工具进入 interrupted，turn 仍可继续。
2. 工具已被取消但 turn 尚未结束：第二次 `Ctrl+C` 取消 turn context。
3. 当前没有工具：`Ctrl+C` 直接取消 turn。
4. 工具已完成、失败或中断后：再次按键不得重复取消或改变终态。

若工具调用链收到 turn cancellation，应停止启动新的工具，并保留已产生的 transcript。

## 错误处理与并发

- stdout/stderr 读取必须支持并发，不得因一侧阻塞而丢失另一侧输出。
- 输出通道关闭后等待进程退出，再发送唯一最终状态事件。
- 启动失败、非零退出和信号终止分别映射到现有错误显示；主动取消映射到 interrupted。
- 重复完成/中断事件必须幂等。
- tool context 和 turn context 的取消函数必须在所有退出路径释放。
- 输出更新与 transcript 渲染状态之间必须避免数据竞争。

## 测试与验收标准

### 取消

- 第一次 `Ctrl+C` 只停止 Bash，turn context 仍有效。
- 第二次 `Ctrl+C` 停止整轮。
- 无运行工具时 `Ctrl+C` 停止整轮。
- 取消后进程及子进程均退出，部分 stdout/stderr 保留，状态为 interrupted。
- 工具完成后按 `Ctrl+C` 不改变结果。

### 输出

- stdout 和 stderr 的多次增量按各自到达顺序显示。
- 并发输出无丢失、死锁或数据竞争。
- 运行中的 transcript 立即刷新。
- 离开底部后不被新输出强制拉回。
- 超过上限显示截断提示且 UI 仍响应。
- 启动失败、非零退出、信号终止和主动中断均显示正确状态。

### 回归

- 非 Bash 工具继续走现有接口。
- 历史恢复、工具折叠、transcript 渲染和模型工具结果测试继续通过。
- 不留下后台 Bash 或子进程。
