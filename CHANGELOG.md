# Changelog

## 2026-06-16

- 修改默认启动行为：`go run ./cmd/agent` 现在每次都启动一个全新的空会话；若需要恢复指定会话，请使用 `-s <session-id>` 参数。
- 新增 `/sessions` 命令：可浏览所有历史会话，列表展示 ID 前缀、创建日期、文件大小与第一条消息摘要，选中后可直接恢复该会话继续对话。
- 新增输入补全弹窗：在输入框中输入 `/` 触发斜杠命令补全，输入 `@` 触发文件路径补全；使用 ↑↓ 键导航候选项，Tab 或 Enter 确认选择，Esc 关闭弹窗。

## 2026-06-12

- Added Bubble slash commands for `/export`, `/setting`, `/subagent`, and `/tasks`, and updated `/help` to show parameter hints for commands that accept arguments.
- Added persisted runtime state under `.ccagent/`, including `.ccagent/settings.json` defaults, transcript exports in `.ccagent/exports/`, and subagent transcripts under `.ccagent/sessions/<sessionID>/`.
- Changed `/model` so it supports `status`, `custom`, and `deepseek` shortcuts in addition to the interactive provider wizard.
- Changed the interactive UI so the input area shows a context-usage meter by default instead of the old `Input`/`Waiting`/`Terminal` title labels.
- Fixed turn-scoped cache-hit display so turns without usage data fall back to `0.0%`, prevented background system notifications from splitting an active assistant response, and tightened exported transcript permissions to `0600`.
