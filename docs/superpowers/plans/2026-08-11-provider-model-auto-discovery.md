# Provider 模型自动发现实现计划（完成记录）

**状态：** 功能实现、逐任务评审、Task 8 验证、四项最终 Important 集成修正和最终 TOCTOU blocker 均已完成。
**实现范围：** `fcbd952c` 之后的 19 个 feature/fix commits，当前实现基线 `56399b59b63379fa7217dd88f0ce236dbe53e1a7`。
**原则：** 本记录描述已经评审通过的代码，不再保留与实现矛盾的预期伪代码或未采用方案。

## 最终架构

- `config.Manager` 是配置、有效模型目录、discovery 状态和 runtime Profiles 的唯一事实来源。
- 启动先独立加载严格 cache，再执行 parse-only discovery selection；watcher 在最终完整 load 前注册。
- live result 先 pending，最终文档确认 Provider/fingerprint/format provenance 后才发布，并延迟写入 cache。
- cache fingerprint 使用 `discoveryURL` 得到的实际请求 URL；cache 读写严格限制 8 MiB。
- HTTP discovery 保留 raw 名称；Manager 在 trim/dedup 前执行 512 UTF-8 bytes、Unicode control 和空白拒绝，并把 raw rejection 计入 `FilteredCount`。
- `DiscoveryConfig.PathSet` 区分 omitted path 与显式空 path，并贯穿 preset、clone、merge、Upsert 和 JSONC round trip。
- discovered-only 激活携带 `CatalogSelection`，要求 prospective active ID 与选择完全一致，通过文件基线校验、runtime preflight、`commitPreview` 和 rollback 事务提交。
- global/workspace bytes 与 existence 在 preview、commit candidate 构建前和 CAS writer 内比较；`0600` advisory lock 从 writer 最终校验保持到 atomic replace，同 baseline 多 Manager 只允许一个提交。
- `/model`、Configuration Center 和 Diagnostics 消费 `EffectiveModels`/`DiscoveryStatus`；所有状态文本经过终端净化和 240-cell 截断。
- subagent discovery 禁用由显式 `workerMode` 决定；worker.start 在 `config.Open` 前校验 `MaxDepth >= 1` 和 `1 <= Depth <= MaxDepth`。

## 实际文件范围

### 新增

- `internal/config/model_catalog.go`
- `internal/config/model_catalog_test.go`
- `internal/config/model_discovery.go`
- `internal/config/model_discovery_test.go`
- `internal/config/model_cache.go`
- `internal/config/model_cache_test.go`
- `internal/config/controller_test.go`
- `internal/config/config_lock_unix.go`
- `internal/config/config_lock_windows.go`
- `internal/config/config_lock_fallback.go`
- `cmd/agent/bootstrap_test.go`
- `cmd/agent/worker_test.go`

### 修改

- `go.mod`
- `internal/config/types.go`
- `internal/config/catalog.go`
- `internal/config/validate.go`
- `internal/config/schema/config-v2.schema.json`
- `internal/config/paths.go`
- `internal/config/paths_test.go`
- `internal/config/atomic.go`
- `internal/config/manager.go`
- `internal/config/migrate.go`
- `internal/config/manager_test.go`
- `internal/config/controller.go`
- `internal/ui/bubble/bubble.go`
- `internal/ui/bubble/command_helpers.go`
- `internal/ui/bubble/config_center.go`
- `internal/ui/bubble/config_center_test.go`
- `internal/ui/bubble/model_wizard.go`
- `internal/ui/bubble/model_wizard_test.go`
- `internal/ui/bubble/types.go`
- `cmd/agent/bootstrap.go`
- `cmd/agent/worker.go`

## 已完成任务与提交

### Task 1 — 配置契约和有效目录

- [x] 增加 Provider discovery schema、preset merge、Snapshot effective catalog 和 clone。
- [x] 实现手工优先、稳定 ID、glob/heuristic 过滤和 Provider 身份规则。
- [x] 加固不可信模型名：trim 前拒绝超过 512 bytes 或包含 `unicode.IsControl` 的名称。
- [x] 保留精确 Provider key，并在 discovery candidate 匹配和生成 ID 时使用受控 normalization。
- [x] 保留 nil 与显式空 include/exclude slice 的区别。

提交：

- `c1fd0a52` — `feat(config): add effective model catalog`
- `0cec12ae` — `fix(config): address catalog review findings`
- `ae3e1152` — `fix(config): harden discovered model catalog`

验证记录：focused catalog/validation tests、完整 `internal/config`、race、vet 和完整仓库测试均通过。

### Task 2 — 有界安全 HTTP discovery

- [x] 支持严格 `openai-list` 和 `ollama-tags` envelope。
- [x] 2 MiB response body 上限、1～10 秒 timeout、same-origin path、no redirect。
- [x] clone caller client、移除 CookieJar、拒绝 nil/zero-value fallback 和 endpoint userinfo。
- [x] 状态码在 body 读取前分类；安全错误不包含 URL、credential、header 或 response body。
- [x] Provider header 确定性验证并屏蔽 Authorization/API-key/Cookie/路由/framing headers。

提交：

- `23ce693e` — `feat(config): discover provider models over HTTP`
- `b65bba98` — `fix(config): harden HTTP model discovery`

验证记录：focused protocol/security tests、完整及 race `internal/config`、vet 和完整仓库测试均通过。

### Task 3 — 严格独立 cache

- [x] 增加 `<config-home>/model-discovery-cache.json` 和 `0600` 原子写。
- [x] fingerprint `discoveryURL` 解析出的实际请求 URL + format，不分别 clean endpoint/path。
- [x] 保留 escaped request path 的 trailing/repeated slash、dot segment 和 percent spelling 差异。
- [x] 对 cache read/write 执行严格 8 MiB 上限。
- [x] 拒绝未知字段、多 JSON value、错误 version、null providers/models、非法 fingerprint/format/time/model。
- [x] cache models 安全过滤、数量上限、trim、去重、排序、non-nil 和 slice isolation。

提交：

- `0cff970f` — `feat(config): cache discovered provider models`
- `4a1eb034` — `fix(config): harden model discovery cache`

验证记录：focused cache/path/fingerprint/oversize tests、完整及 race `internal/config`、vet 和完整仓库测试均通过。

### Task 4 — Manager startup/cache lifecycle

- [x] cache 加载独立于 parse-only selection；无效 startup config 修复后可由 reload/watcher 使用已加载 cache。
- [x] active Provider 或 single-Provider bootstrap 最多一次 live request；多 Provider 无 active 时跳过。
- [x] 初始 discovery selection 是 parse-only，不构建 runtime Profiles 或做重复 credential resolution。
- [x] live 请求后、最终完整读取前注册 watcher，避免 global/workspace 变更丢失。
- [x] live result pending；最终文档确认 Provider 存在、discovery enabled、actual-URL fingerprint 与 format 未变后才发布。
- [x] cache update 使用 clone/write/assign；写失败保持已加载 cache 与磁盘不可变，同时当前进程保留 confirmed live result。
- [x] 每个 reload/update/watcher candidate 从 immutable full cache 重新派生，并以 provenance 匹配的 live 结果覆盖。
- [x] reload、Update 和 watcher 不发起 rediscovery；persistent diagnostics 按当前配置 applicability 过滤。

提交：

- `d7e41ac2` — `feat(config): discover models during top-level startup`
- `3b75d0ee` — `fix(config): harden manager model discovery state`
- `804f7b6b` — `fix(config): close model discovery lifecycle gaps`
- `b3fe1e76` — `fix(config): guard discovery cache lifecycle`

验证记录：repeated lifecycle/watcher/blocking/write-failure tests、完整及 race `internal/config`、vet 和完整仓库测试均通过。

### Task 5 — transactional discovered-model activation

- [x] 增加 revision/identity/source-bound `CatalogSelection`。
- [x] exact ID 优先；trim fallback 只接受唯一匹配；stale remap 和 ambiguous alias 被拒绝。
- [x] discovered-only selection 在同一 operation batch 只 pin 一个模型并设置 active；configured selection 只设置 active。
- [x] `Manager.PreviewUpdate` 不写盘、不发布、不修改 Manager。
- [x] Controller 在 `applyMu` 下执行 preview → runtime preflight → commitPreview；失败恢复旧 runtime。
- [x] commit 校验 preview 与重算 candidate 的 durable content、workspace、catalog 和 runtime config 一致。
- [x] revision、credential/workspace drift、runtime apply 和 deterministic write failure 不产生文件/Snapshot/runtime 分裂。

提交：

- `934f0e67` — `feat(config): pin selected discovered models`
- `33fd819e` — `fix(config): make model activation transactional`

验证记录：repeated Controller/PreviewUpdate tests、完整及 race `internal/config`、vet 和完整仓库测试均通过。

### Task 6 — TUI、wizard 和 diagnostics

- [x] `/model` wizard 和 Configuration Center 使用 `EffectiveModels` 并显示 source。
- [x] 交互选项保存 `CatalogSelection`，确认时不重新按 provider/name 猜测身份。
- [x] stale conflict 后刷新 Snapshot 和列表，Configuration Center 保持打开并可恢复操作。
- [x] `/model status`、`/config status` 和 Diagnostics 显示 discovery source/count/cache/time/skip/error。
- [x] 所有 DiscoveryStatus 文本字段经过 `sanitizeTerminalText`、空白折叠和 240 terminal-cell 截断。
- [x] UI 只读取 Snapshot，不执行网络 discovery。

提交：

- `64168a0f` — `feat(tui): expose discovered provider models`
- `c75409a0` — `fix(tui): preserve catalog selection identity`

验证记录：repeated wizard/config-center/status/sanitization tests、完整及 race bubble+config、vet 和完整仓库测试均通过。

### Task 7 — subagent worker boundary

- [x] `configOpenOptions` 只从显式 `workerMode` 设置 `DisableModelDiscovery`，不再从 depth 推断身份。
- [x] root 保持 `workerMode=false`；有效 `worker.start` 才设置 `workerMode=true`。
- [x] `readSubagentWorkerStart` 在 runner/config startup 前验证 session、`MaxDepth >= 1`、`1 <= Depth <= MaxDepth`。
- [x] 覆盖 first-level/delegated worker、非法/负 depth、Depth > MaxDepth 和 malformed JSON 类型。

提交：

- `27e00551` — `fix(agent): avoid model discovery in subagent workers`
- `d65c4eb6` — `fix(agent): identify subagent workers explicitly`

验证记录：focused `cmd/agent`/`internal/subagent`、完整 `cmd/agent`、vet 和完整仓库测试均通过。

## Task 8 — 最终验证与文档提交

功能实现保持冻结；Task 8 未扩展功能，也未修改 Go/JSON 生产代码。

### 格式检查

- [x] 对 `fcbd952c..HEAD` 的全部 26 个 feature Go 文件运行 `gofmt -d`，无输出。

### 必需测试

- [x] `go test ./internal/config ./internal/ui/bubble ./cmd/agent -count=1` — PASS。
- [x] `go test -race ./internal/config ./internal/ui/bubble -count=1` — PASS。
- [x] `go test ./... -count=1` — 最终 PASS。
- [x] `go vet ./...` — PASS，无诊断。

完整仓库测试前两次分别触发未被本 feature 修改的 `internal/tool/exec` 和 `internal/subagent` 时序型测试失败；对应目录相对 `fcbd952c` 无 diff，focused repetition 通过，第三次相同完整命令通过。未因此修改无关生产代码。

### 静态与安全检查

- [x] `git diff --check` — PASS。
- [x] schema JSON 可解析；discovery `additionalProperties=false`、format enum、timeout 1～10、include/exclude array 均匹配实现。
- [x] production cache schema 只有 version/providers/fingerprint/format/time/models，不包含 credential、Authorization、API key、Cookie、headers 或 secret 字段。
- [x] cache tests 中的假 secret 仅用于断言 fingerprint/serialization 排除敏感输入。
- [x] strict 8 MiB、actual-URL fingerprint、512-byte/control filtering、CatalogSelection/PreviewUpdate、terminal 240-cell 和 workerMode/depth 引用检查通过。
- [x] worktree 在提交前只有本 plan/design 两个 untracked 文件；无 tracked source diff。

### smoke 策略

- [x] 未连接真实外部 Provider。
- [x] `go test ./internal/config -run 'TestHTTPModelDiscoverer|TestModelDiscovery(Cache|EndpointFingerprint)|TestManager.*Discovery|Test.*HotReload|TestWatcher' -count=1` — PASS；作为 cold/startup-once、offline/cache、fingerprint、empty live 和 watch-safe smoke。

### 交付

- [x] `.superpowers/sdd/task-8-report.md` 记录精确命令、结果、最终 commit 和 concerns（该路径被 Git 忽略）。
- [x] 最终提交仅包含本 plan/design；提交主题 `docs: finalize provider model auto discovery design`。

## Final Important integration corrections

四项最终集成 findings 采用 TDD 修正，未启动 subagent，未增加无关功能：

- [x] Manager Snapshot 保存 global/workspace bytes+existence 基线；`PreviewUpdate` 和 commit 在 `updateMu` 内重读并比较，writer 前再次比较。content replacement、create/delete/missing 都返回 `ErrRevisionConflict`；Controller 恢复旧 runtime，外部 bytes 和旧 Snapshot 保持到 reload。
- [x] `ActivateCatalogSelection` 在 preview 后要求 `prospective.ActiveModelID == selection.ID`。workspace `activeModel` 或 `PAW_MODEL` 胜出时返回可操作错误；configured/discovered 两类都不改 runtime/file，discovered 不 pin。
- [x] `DiscoveryConfig.PathSet` 与 presence-aware JSON marshal/unmarshal 保留显式空 path。preset/nonempty programmatic path 视为 present；omitted 继承 preset，显式空保留 endpoint path；clone/merge/Upsert/reload round trip 均覆盖。
- [x] HTTP decoder 不再先 trim/dedup；Manager 对 raw 名称先做 512-byte/control/empty 检查，再规范化。危险名称不进入 retained/catalog/cache/selector，all-rejected 结果仍保留 `FilteredCount`。

提交：

- `ec85c22c` — `fix(config): close final discovery integration gaps`

验证记录：新增测试先以 `PathSet` 缺失编译失败进入 red；实现后 focused tests repeated 10 次、完整 `internal/config`、config+bubble race、`cmd/agent`、`go test ./...`、`go vet ./...`、gofmt、schema/security/diff checks 均通过。最终文档提交后再次执行交付验证。

## Final TOCTOU blocker — CAS atomic writer 与跨进程锁

最终 review 指出 Manager 原有“writer 前重读”与 `replaceFile` 之间仍有 TOCTOU：两个独立 Manager 可同时通过相同 baseline 校验，随后 last-write-win。该 blocker 采用 TDD 修正，未启动 subagent：

- [x] 先把 Controller 的 global/workspace 外部编辑测试移动到 temp 已创建同步、writer 内最终校验尚未执行的确定性 hook；初始编译因 `configWriteHook` 缺失进入 red。
- [x] 增加同路径双 Manager 并发测试；两个 writer 都先完成 temp 准备，再同时竞争提交，断言 exactly one success、one `ErrRevisionConflict`、disk 等于 winner、loser Snapshot 不发布 candidate。
- [x] 新 CAS writer 接收 expected global state 与 Manager file-state validation callback；temp `0600` write+`Sync`+close 后获取 lock，持锁重读 expected global、回调重读 global/workspace，成功才 atomic replace。
- [x] mismatch/read conflict 删除 temp 且不 replace；Controller 恢复旧 runtime，外部 global/workspace bytes 保留，Manager Snapshot 保持旧 revision/content。
- [x] `<global-config>.lock` 在 Flock-capable Unix 使用 `golang.org/x/sys/unix.Flock`，Windows 使用 `LockFileEx`；其他目标保留可构建的进程内 fallback。lock file mode 为 `0600`。
- [x] starter 与 legacy migration 也改用 expected-missing CAS，不覆盖其他进程已创建的 global winner；schema/cache 继续使用普通 `atomicWriteFile`，watcher 仍只匹配 global/workspace config path，不消费 lock/temp 事件。

提交：

- `56399b59` — `fix(config): serialize CAS config commits`

验证记录：新 transaction tests repeated 20 次、完整 config、config+bubble race、`cmd/agent`、`go test ./...`、`go vet ./...`、gofmt/diff/security checks，以及 Windows/Linux/Plan 9 config package cross-build 均通过。

## 提交总览

截至实现基线共有 19 个 feature/fix 提交：

```text
c1fd0a52 feat(config): add effective model catalog
0cec12ae fix(config): address catalog review findings
ae3e1152 fix(config): harden discovered model catalog
23ce693e feat(config): discover provider models over HTTP
b65bba98 fix(config): harden HTTP model discovery
0cff970f feat(config): cache discovered provider models
4a1eb034 fix(config): harden model discovery cache
d7e41ac2 feat(config): discover models during top-level startup
3b75d0ee fix(config): harden manager model discovery state
804f7b6b fix(config): close model discovery lifecycle gaps
b3fe1e76 fix(config): guard discovery cache lifecycle
934f0e67 feat(config): pin selected discovered models
33fd819e fix(config): make model activation transactional
64168a0f feat(tui): expose discovered provider models
c75409a0 fix(tui): preserve catalog selection identity
27e00551 fix(agent): avoid model discovery in subagent workers
d65c4eb6 fix(agent): identify subagent workers explicitly
ec85c22c fix(config): close final discovery integration gaps
56399b59 fix(config): serialize CAS config commits
```

逐任务 report、最终集成修正和 CAS/lock TOCTOU 修正记录 focused、package、race、vet、repository、cross-build、schema/security/diff 命令；最终结果以 `.superpowers/sdd/task-8-report.md` 为准。
