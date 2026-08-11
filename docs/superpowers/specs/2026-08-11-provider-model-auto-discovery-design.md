# Provider 模型自动发现设计（已实现）

**日期：** 2026-08-11
**状态：** 已实现、已评审；实现基线 `d65c4eb633ceb77c514b7409ea80696623b7a0f1`

## 概述

Paw 现在会在顶层进程启动时，对 active model 所属 Provider（或无 active model 时唯一的 Provider）执行一次有界模型发现。发现结果与匹配缓存、手工 `Document.Models` 合并为 `Snapshot.EffectiveModels`；运行时 Profiles、`/model`、Configuration Center 和 Diagnostics 都消费同一 Snapshot。

发现是增强能力而不是就绪条件。网络、认证、协议、缓存或写盘失败不会把原本可用的配置变成 `Ready=false`，也不会批量修改用户的模型注册表。只有用户选择 discovered-only 模型时，系统才把该单个模型与 active model 切换作为一个事务提交。

## 目标与边界

### 已实现目标

1. 每次顶层 Paw 启动最多请求一个目标 Provider。
2. live 发现失败时使用匹配缓存；没有匹配缓存时只使用手工模型。
3. `config.Manager` 发布统一、可深拷贝的有效目录和发现状态。
4. discovered-only 模型只在用户选择时单项持久化。
5. 配置文件、Manager Snapshot 与运行时模型切换保持事务一致。
6. 缓存和错误信息不保存或回显 credential、Authorization、Cookie 等敏感值。
7. 配置 reload/watcher 和 subagent worker 不发起重复发现请求。

### 非目标

- 不把全部发现模型写入 `config.jsonc`。
- 不做后台定时刷新、TTL 刷新或 UI 主动网络刷新。
- 不支持任意脚本、插件或用户自定义解析器。
- 不从远端补充价格、上下文窗口或能力元数据。
- 不改变手工模型身份与配置 v2 的既有语义。
- 不使用真实外部 Provider 作为测试依赖；协议和冷启动/离线/指纹冒烟由 `httptest.Server` 覆盖。

## 配置契约

Provider 可包含：

```jsonc
{
  "providers": {
    "local": {
      "transport": "openai-compatible",
      "endpoint": "http://127.0.0.1:11434/v1",
      "discovery": {
        "enabled": true,
        "path": "models",
        "format": "openai-list",
        "timeoutSeconds": 3,
        "include": [],
        "exclude": []
      }
    }
  }
}
```

字段语义：

- `enabled` 是 `*bool`，显式 `false` 可覆盖 preset 默认启用。
- `path` 只能是同 origin 路径；禁止绝对 URL、userinfo、query、fragment、协议相对 URL和 `..` 路径段。
  - `/...` 替换 endpoint path；
  - 非 `/` 开头的路径追加到 endpoint path；
  - 空路径保留 endpoint path。
- `format` 仅支持 `openai-list` 和 `ollama-tags`。
- `timeoutSeconds` 为 1～10 秒；Go 值 `0` 表示默认 3 秒。
- `include` / `exclude` 使用大小写敏感 glob；模型名按扁平命名空间匹配，`*`、`?` 可以跨模型名中的 `/`。
- `include` 非空时成为 allow-list，并可恢复被内置启发式排除的名称；`exclude` 最后执行并总是获胜。
- nil filter 与显式空数组语义在 clone、preset merge、Snapshot 和 JSONC patch round trip 中保持不同；显式空数组仍序列化为 `[]`。

内置自动发现默认值：

| Preset | Path | Format |
|---|---|---|
| `openai` | `models` | `openai-list` |
| `deepseek` | `models` | `openai-list` |
| `openrouter` | `models` | `openai-list` |
| `ollama` | `/api/tags` | `ollama-tags` |

`anthropic`、`custom` 和未知 preset 没有自动发现默认值；显式 discovery 配置仍可启用。

## 有效模型目录

`Snapshot` 增加：

```go
type Snapshot struct {
    Document        Document
    EffectiveModels map[string]CatalogModel
    Discovery       DiscoveryStatus
    // existing fields...
}

type CatalogModel struct {
    ID     string
    Model  Model
    Source ModelSource // configured / discovered
}
```

### 合并和身份规则

1. 先复制全部手工 `Document.Models`，保留用户注册 ID 和完整元数据。
2. 仅为当前仍存在且 discovery 实际启用的 Provider 合并 retained live/cache 结果。
3. discovered 候选 Provider ID 以 `strings.TrimSpace` 比较，但产生的 `Model.Provider` 保留文档中的精确 Provider key。
4. 模型身份为“精确 Provider key + trim 后模型名”；手工项与 discovered 项身份相同时，手工项优先。
5. 不同 Provider 下的同名模型互不覆盖。
6. 默认 discovered 模型只推断安全 adapter：OpenAI → `gpt`，DeepSeek → `deepseek`，其余 → `openai-compatible`；不虚构 capabilities。
7. Snapshot clone 深拷贝目录、模型参数、布尔指针、Provider discovery 配置及 filter slice，供订阅和热重载安全使用。

### 不可信模型名过滤

发现和缓存模型名进入 glob、ID 生成或目录前遵守同一信任边界：

- 原始 UTF-8 字符串超过 **512 bytes** 时拒绝；
- 包含任何 `unicode.IsControl` rune（包括 ESC、换行、C0/C1 控制字符）时拒绝；
- 安全检查在 trim 前执行，不能靠去掉首尾控制字符绕过；
- 相同不安全原始名称只计一次过滤；
- 安全名称再 trim、去空、去重、排序；
- 启发式排除 embedding、rerank、moderation、transcription、speech/TTS、image generation 等明显非聊天名称。

### discovered-only 稳定 ID

基础 ID 为 `<trim(providerID)>/<trim(modelName)>`。若被其他身份占用，追加：

```text
~<sha256(trim(providerID) + "\x00" + trim(modelName)) 的前 8 个十六进制字符>
```

极端冲突继续追加 `~2`、`~3`。精确配置 ID 始终优先，不会被 discovered 条目替换。

## HTTP 发现客户端

`HTTPModelDiscoverer` 的实际约束：

- 只发送 GET；请求由调用方 context 和 1～10 秒 discovery timeout 共同约束。
- 默认响应体硬上限为 **2 MiB**。
- `NewHTTPModelDiscoverer` 复制调用方 `http.Client`，清除 CookieJar，设置不跟随 redirect；不修改调用方 client。
- nil client、nil/zero-value discoverer 不回退到进程全局默认行为，而返回安全的 `invalid_config`。
- Manager 的默认 discoverer 显式用 `http.DefaultClient` 构造上述隔离副本。
- endpoint userinfo 被拒绝；redirect 作为失败状态处理，Authorization 不会被转发到其他目标。
- Provider headers 在配置验证和请求前都按确定顺序校验：拒绝非法 token、大小写重复名、首尾空白和非法控制字节。
- Authorization、API-key、Cookie、Host、Content-Length、Transfer-Encoding 等敏感/路由/ framing headers 不从 Provider headers 复制；credential 只通过现有 auth 解析后设置 Bearer header。
- 非 2xx 状态在读取 body 前分类；401/403 为 `auth_failed`，429 为 `rate_limited`，3xx 为 `redirect`。
- 错误字符串只包含白名单 kind、可选状态码和固定安全摘要，不包含 URL、headers、credential 或响应 body。

响应 envelope 是严格的：

- `openai-list` 要求非 null 顶层对象和非 null `data` 数组；
- `ollama-tags` 要求非 null 顶层对象和非 null `models` 数组；
- 缺字段、null 字段、顶层数组、错误对象或类型不符均失败；
- 显式空数组是成功结果，并会替换旧缓存为空列表；
- 名称 trim、去空、去重、排序后返回，最终安全过滤仍由目录/缓存边界执行。

## Discovery cache

路径：`<config-home>/model-discovery-cache.json`。写入使用现有 `atomicWriteFile(..., 0600)`。

```json
{
  "version": 1,
  "providers": {
    "local": {
      "endpointFingerprint": "sha256:<64 lowercase hex>",
      "format": "openai-list",
      "discoveredAt": "2026-08-11T00:00:00Z",
      "models": ["model-a"]
    }
  }
}
```

### actual-URL fingerprint

fingerprint 来自 `discoveryURL(endpoint, path)` 解析出的**实际 GET URL**与 format，而不是分别清理 endpoint/path：

- scheme/hostname 小写；hostname 尾点归一化；默认 80/443 端口移除；
- 保留实际 escaped path 拼写；不执行 `path.Clean` 或 decode；
- trailing slash、重复 slash、`.` segment 和 percent-escape 拼写差异保持隔离；
- 如果不同 endpoint/path 拆分最终解析为相同实际 URL，则共享 fingerprint；
- credential、auth env、headers、cookies、body、timeout、retry、stream、API path 和 include/exclude 不进入 fingerprint；
- 无效 URL 不产生可复用 fingerprint。

### strict 8 MiB cache

缓存读写使用严格 **8 MiB** 上限（`4 * 2 MiB`）：

- 读取使用 `LimitReader(limit+1)`，包括稠密和 sparse 超大文件；
- 编码写入同样拒绝超过 8 MiB 的结果；
- decoder 拒绝未知字段、null/非对象顶层、多个 JSON value；
- version 必须恰为 1，`providers` 和每个 `models` 必须非 null；
- fingerprint、format、非零可序列化时间和每项模型名都严格校验；
- 每个 entry 另有模型数量上限；模型结果总是 canonical、排序、去重且与调用方 slice 隔离；
- 损坏或不匹配缓存不阻断启动，也不会被复用。

缓存 schema 不包含 credential、Authorization、headers、Cookie 或 secret 字段。

## Manager 生命周期

### parse-only / watch-safe startup

启动顺序是：

1. 建立配置目录、schema、迁移和 starter config。
2. **独立加载完整 discovery cache**；即使全局或 workspace 配置此时无效，缓存仍保留供后续修复后的 reload/watcher candidate 使用。
3. 执行 parse-only discovery selection：解析全局和 workspace、确定 active ID/Provider，但不构建 runtime Profiles，不额外读取 runtime credential。
4. 对 active Provider 发起至多一次 live 请求；无 active model 且仅一个 Provider 时使用该 Provider；多个 Provider 且无 active 时跳过。
5. 在 live 请求后、完整 candidate load 前注册全局/workspace watcher，关闭请求期间和最终读取之间的变更丢失窗口。
6. 重新读取当前配置并只构建一次完整 runtime/Profile candidate。
7. 仅当最终文档仍包含同一 Provider、discovery 仍启用、actual-URL fingerprint 和 format 均相同时，才确认 pending live result。

因此请求阻塞期间发生的 Provider 删除、禁用、endpoint/path/format 变化不会把过时 live 结果发布或写入缓存。

### immutable / deferred cache writes

- 成功 live 结果先放入 pending，不立即修改 retained live map 或已加载 cache。
- 最终 provenance 确认后，live 结果才进入进程内 retained map。
- durable cache 更新在 clone 上构造，并把 clone 传给 writer。
- 只有 `0600` 原子写成功后，Manager 才替换内存中的 immutable loaded cache。
- 写失败时，原内存 cache 和磁盘 cache 均保持不变；已确认 live 结果仍在当前进程优先使用，并报告 `write-failed`。
- reload、Update 和 watcher 每次从 immutable full cache 重新派生当前文档可用条目，再以 provenance 匹配的 retained live 覆盖；空 live 结果也会覆盖匹配 cache。
- reload、Update、watcher 和 UI 页面切换都不会再次请求网络。

### 状态和诊断

`DiscoveryStatus` 记录 target、source（`live` / `cache` / `manual-only`）、attempt/success/discovered 时间、发现/过滤/有效数量、匹配 cache Provider 数、cache state、skip reason 和安全错误。

诊断按当前 target/fingerprint/format 重新判断适用性；旧 Provider 的启动失败不会污染后来不相关的配置。发现失败是 warning/fallback，不改变既有 `Ready` 语义。

## discovered-only 模型事务激活

交互选择不再只传字符串 ID，而是传：

```go
type CatalogSelection struct {
    Revision    uint64
    ID          string
    ProviderKey string
    ModelName   string
    Source      ModelSource
}
```

它绑定选择时 Snapshot revision、精确 ID、精确 Provider key、trim 后模型名和 source。激活时必须确认当前目录中同一 ID 仍映射到相同身份与 source；否则返回 `ErrRevisionConflict`。精确 ID 优先，trim fallback 只有唯一匹配时才接受。

事务流程：

1. discovered source 生成 `UpsertModel(exactID, exactModel)`；configured source 不 upsert。
2. 同一 operation batch 追加 exact `SetActiveModel`。
3. `Manager.PreviewUpdate` 用与 Update 相同的 revision、JSONC patch 和验证规则生成不写盘、不发布的 prospective Snapshot。
4. Controller 在 `applyMu` 下保存旧 runtime config，先把 prospective runtime 应用到 client。
5. `commitPreview` 重新构造 candidate，并校验 durable content、workspace、effective catalog 和 runtime config 与 preview 完全一致。
6. 校验成功后才用 `0600` 原子写 config 并发布 Snapshot。
7. runtime apply、revision、credential/workspace drift 或写盘失败时恢复旧 runtime；文件和 Manager Snapshot 不提交。

该流程保证只登记所选 discovered 模型，不产生悬空 active model，也不静默激活被其他身份占用的 stale ID。

## UI 与终端安全

- `/model` wizard、Configuration Center Models/Active 页面读取 `EffectiveModels`，显示 configured/discovered source。
- 交互式行保存 `CatalogSelection` 并调用 `ActivateCatalogSelection`；stale conflict 后刷新 Snapshot、清除过期 target，配置中心保持打开并可继续操作。
- 直接文本 `/model <id>` 仍是当前 Snapshot 的即时精确 ID 操作，通过 `SetActiveModelID` 包装为当前 selection。
- `/model status`、`/config status` 和 Diagnostics 显示 discovery source、Provider、计数、cache、时间/年龄、skip 和安全错误。
- 所有 DiscoveryStatus 文本字段都经过 `sanitizeTerminalText`、空白折叠和 **240 terminal-cell** 截断，防止 CSI、OSC、DCS、BEL、C0/C1、CR/LF、tab 或宽字符破坏终端布局。
- UI 只消费 Snapshot，不发起 discovery 网络请求。

## subagent 进程边界

是否禁用发现由显式 `subagentRuntimeContext.workerMode` 决定，而不是从 depth 推断：

- root 进程：`workerMode=false`，即使 depth 字段非零也不自动视为 worker；
- 有效 `worker.start`：解码和校验成功后设置 `workerMode=true`，`configOpenOptions` 才设置 `DisableModelDiscovery=true`。

worker 在调用 `config.Open` 前必须满足：

- `session_id` 非空；
- `MaxDepth >= 1`；
- `1 <= Depth <= MaxDepth`；
- malformed depth 类型由 JSON decoder 拒绝。

首层和 delegated worker 都禁用 discovery；in-process subagent 复用顶层运行时，本来也不会重新 Open 配置。

## 安全不变量

1. 发现最多一个目标 Provider、一次启动请求，且同步有界。
2. 不跟随 redirect，不继承 CookieJar，不接受 endpoint userinfo。
3. 模型名在持久化/显示/ID 生成前执行 512-byte/control filtering。
4. cache 严格限制 8 MiB，schema 和 entry 全量校验。
5. fingerprint 绑定实际请求 URL 与 format，不包含秘密。
6. cache、错误、diagnostic 和 UI status 不保存或回显 credential、Authorization、Cookie 或 response body。
7. Snapshot/retained/cache 都通过 clone 和只读替换保持发布后不可变。
8. 用户选择通过 CatalogSelection + PreviewUpdate/commitPreview 保持运行时、文件和 Snapshot 一致。

## 实现文件

- 配置契约/目录：`internal/config/types.go`、`catalog.go`、`validate.go`、`model_catalog.go`
- HTTP discovery：`internal/config/model_discovery.go`
- cache/path：`internal/config/model_cache.go`、`paths.go`
- lifecycle/runtime：`internal/config/manager.go`
- transactional activation：`internal/config/controller.go`
- UI：`internal/ui/bubble/{bubble.go,command_helpers.go,config_center.go,model_wizard.go,types.go}`
- worker boundary：`cmd/agent/{bootstrap.go,worker.go}`
- schema：`internal/config/schema/config-v2.schema.json`
- 对应 `_test.go` 文件覆盖全部关键边界。

## 验收状态

实现已经通过逐任务 spec/quality review。Task 8 仅执行最终文档、格式、测试、race、vet、schema/secret/cache 和 worktree 验证；不使用真实外部 Provider，不扩展功能。最终命令和结果记录在 `.superpowers/sdd/task-8-report.md`。
