# 状态压缩切片 1：恢复路径实施计划

**日期：** 2026-08-13
**对应：** `docs/superpowers/specs/2026-08-13-state-compression-design.md` §11 切片 1（D3/D7/D9/D10/D11/D12）
**验证前置：** 切片 0 实验完成（`docs/superpowers/research/2026-08-13-state-compression-validation.md`）——状态+清洗3轮 = 全量 10–18% token，方案质量完整

## 目标

模式 B 的**恢复路径**：全局项目布局 + 状态注入器 + 3 轮恢复 + search_transcript + settings 接入。不包含运行时 90% 压缩（切片 3）。

## 任务清单

### T1 存储层：全局项目布局 + 迁移（D7）✅
- `internal/session/jsonl_store.go`：baseDir 改为全局项目目录 `~/.paw/projects/<项目名>/sessions/`
  - 项目名 = cwd basename（含哈希后缀防重名）；新增 `NewJSONLStoreGlobal(cwd)`，保留 `NewJSONLStoreInCwd` 兼容（或内部切换）
  - 读取 fallback：`sessionDir` 探测全局 → 不存在时回退 legacy `<cwd>/.paw/sessions/`（旧会话只读兼容）
  - 写入固定全局（新数据不落旧路径）
- 一次性迁移命令：`/migrate-sessions` 或启动参数——legacy → 全局复制 + 校验（行数/哈希）
- 测试：新会话写全局、旧会话读取 fallback、迁移命令幂等

### T2 buildStateContext 注入器（D9/D12）✅
- `internal/loop/state_context.go`（新）：读取 plan 投影 + todo 快照 + memory.md + ariadne.md → 组装稳定状态块
- 状态块格式：固定前缀 + 各组成部分带 `updated_at`/来源标注（D12）
- 字节稳定：内容变化时整体替换，不局部编辑（D9）
- 测试：状态块组装、时间戳标注、空组件容错

### T3 模式 B 恢复流程（D3 + v2 结论）✅
- `runSingleTurnWithTiming` 冷启动分支（historyIsNil）：模式 B 下
  - 注入 `buildStateContext` 状态块（替代全量历史）
  - 最近 3 轮：按 turn 边界取最后 3 轮，**清洗工具参数（保留工具名+文本）**（v2 结论）
  - Recovery 状态保留
- settings `context.mode=state` 时启用；`summary` 走现状
- 测试：模式 B 恢复注入内容、模式 A 不受影响、清洗正确性

### T4 search_transcript 工具（D11）✅
- `internal/tool/` 新工具：检索当前 session transcript
- 入参 `query`/`turn_range`/`limit`；返回 `matched` + `searched`（可检索范围）+ 匹配记录（role/时间/片段/turn_id）
- 0 命中返回「未找到 + 可检索范围」（D11）
- 注册到主 agent 工具集；测试：关键字命中、范围限定、0 命中提示

### T5 settings 接入 ✅（TUI 状态栏展示待切片 3 一并做）
- `internal/settings`：`context.mode`（summary|state，默认 state）、`resumeRecentTurns`（3）、`stateCompactionRatio`（0.9，切片 3 用）
- TUI：状态栏显示当前模式；`/config` 可见
- 测试：settings 解析、默认值、非法值拒绝

## 验证（每任务后）

- 对应包 focused tests + `go build ./...`；T1 后全仓库 `go test ./...`（存储迁移风险最高）

## 交付顺序

T1（存储）→ T2（注入器）→ T3（恢复流程）→ T4（取回工具）→ T5（settings/TUI）。每任务独立可验证。
