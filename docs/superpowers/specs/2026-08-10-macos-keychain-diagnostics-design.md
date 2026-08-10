# macOS Keychain 与配置诊断修复设计

**日期：** 2026-08-10  
**状态：** 已确认方向，待书面复核

## 背景

配置系统 v2 会在首次启动时把 `~/.paw/config.json` 中的 v1 明文 API key 迁移到 `CredentialStore`，成功后才生成新的 `config.jsonc`。当前只有 Windows 实现了原生凭据存储；`credentials_other.go` 的 `!windows` 构建条件也覆盖 macOS，因此 macOS 上的 `Get`、`Set` 和 `Delete` 恒定返回 `ErrCredentialStoreUnavailable`。只要旧配置包含明文 key，迁移就必然被阻塞。

配置中心的 Diagnostics 页面还有一个独立问题：modal 固定最多为 80 个终端 cell，而诊断消息在进入固定矩形前没有换行。`fitStyledRect` 最终只保留每行左侧内容，导致真正的错误尾部不可见。

## 目标

1. 在启用 CGO 的 macOS 构建中使用原生 Security.framework 保存、读取和删除 Paw 凭据。
2. 保持 Windows Credential Manager 的现有实现、凭据命名和行为不变。
3. 不通过命令行参数、配置文件、日志或诊断文本暴露密钥。
4. 在不支持安全存储的平台或 macOS 无 CGO 构建中继续使用现有的 env fallback。
5. Diagnostics 中的长路径和长错误按终端 cell 安全换行，完整内容可见且不破坏 ANSI、CJK 或 emoji。

## 非目标

- 本次不实现 Linux Secret Service。
- 不改变 v2 配置 schema、Provider/Model 结构或 `CredentialStore` 接口。
- 不自动删除或重写原始 v1 配置；现有备份和幂等迁移语义保持不变。
- 不改变 Windows 凭据 ID，也不在平台之间共享凭据实体。

## 方案比较

### 方案 A：Security.framework 原生后端（采用）

通过仅在 `darwin && cgo` 下编译的文件调用 Security.framework 的 Generic Password API。密钥直接从进程内存传给系统 API，不出现在子进程参数中。优点是符合现有 `OSCredentialStore` 抽象、安全边界清晰，并能自动迁移两个以上的旧 Provider；代价是 macOS 原生构建需要 CGO。

### 方案 B：调用 `/usr/bin/security`

实现简单，但常见调用方式会把密钥作为命令参数传递，可能短暂暴露在进程列表和诊断工具中。即使增加复杂的 stdin 包装，错误和交互行为也不如直接调用框架可控，因此不采用。

### 方案 C：仅允许环境变量迁移

无需原生代码，但不能安全、自动地处理旧配置中没有 `apiKeyEnvName` 的多个 Provider；还会把迁移策略和运行时凭据解析混在一起。它继续作为无安全存储平台的显式配置路径，而不是 macOS 的主迁移方案。

## 架构与构建边界

平台文件按以下规则互斥编译：

| 文件 | 构建条件 | 后端 |
|---|---|---|
| `credentials_windows.go` | `windows` | Windows Credential Manager，保持不变 |
| `credentials_darwin.go` | `darwin && cgo` | Security.framework Generic Password |
| `credentials_other.go` | `!windows && (!darwin || !cgo)` | 返回 unavailable，由 env fallback 接管 |

公共的 `OSCredentialStore`、`resolveCredential` 和迁移调用链不增加平台分支。macOS 条目使用稳定的 service `Paw` 与 account `<credential-id>`；例如 `provider/deepseek` 仍是上层唯一标识。

## macOS 数据流

### 写入

1. 校验 credential ID 与 secret 非空。
2. 使用 class、service、account 查询 Generic Password。
3. 条目存在时使用 `SecItemUpdate` 替换数据；不存在时使用 `SecItemAdd` 创建。
4. 仅在系统 API 成功后，迁移器才继续生成不含明文 key 的 v2 配置。

### 读取

1. 使用 class、service、account 和 `kSecReturnData` 查询单条记录。
2. 将返回的 `CFData` 复制到 Go 字符串后立即释放 Core Foundation 对象。
3. `errSecItemNotFound` 映射为 `ErrCredentialNotFound`，使既有 env fallback 生效。

### 删除

按 class、service、account 调用 `SecItemDelete`。不存在仍映射为 `ErrCredentialNotFound`，保持 Windows 与 fake store 的调用约定。

## 错误处理与安全

- `errSecItemNotFound` 映射为 `ErrCredentialNotFound`。
- 系统凭据服务不可用映射为 `ErrCredentialStoreUnavailable`。
- 取消授权、访问被拒绝等其他 OSStatus 保留可读的系统错误和状态码，但永不包含 secret。
- 所有 Core Foundation 对象和临时 C 缓冲区在同一次调用内释放。
- 不调用外部命令，不把 secret 写入 argv、环境变量、临时文件或测试输出。
- 迁移任一 Provider 失败时继续保持当前的全有或全无落盘行为；不会生成半完成的 `config.jsonc`。

## Diagnostics 渲染

配置中心在构造 Diagnostics 内容时，使用 modal 的实际 body cell 宽度调用现有 `wrapStyledCellText`。路径、revision 状态和每条诊断分别换行，再交给 `renderFixedStyledPanel` 做最终高度裁剪。这样保留固定 panel 几何，同时避免横向静默丢失错误尾部。

本次不引入滚动状态；当全部诊断超过可用高度时仍遵循现有纵向裁剪规则。该限制与本次横向丢字问题分离。

## 测试与验收

### 自动测试

- macOS 后端的状态映射和参数校验单测不访问真实用户凭据。
- 既有 fake-store 迁移测试继续证明明文 key 只在安全写入成功后迁移。
- 新增 Diagnostics 回归测试：长迁移错误的尾部仍出现在渲染结果中，且每行不超过 modal cell 宽度。
- 运行 `go test ./internal/config ./internal/ui/bubble` 和完整 `go test ./...`。
- 在 macOS 上分别验证 CGO 开启与关闭的构建边界。
- 使用 `GOOS=windows`、`GOARCH=amd64/arm64`、`CGO_ENABLED=0` 交叉编译配置包测试二进制，证明 Windows 文件选择与编译保持正常。

### 手动验收

1. 使用隔离的临时 HOME、`PAW_CONFIG_HOME`、临时 credential ID 和含两个明文 Provider 的 v1 fixture 启动 Paw。
2. 确认两个临时凭据均进入 macOS Keychain，v2 配置不含明文 secret，迁移 marker 与备份按原语义生成。
3. 再次启动确认迁移幂等，配置为 `ready: true`。
4. 删除测试创建的临时 Keychain 条目，不触碰已有 Paw 凭据。
5. 在窄终端打开 Diagnostics，确认错误完整换行且边框宽度稳定。

真实旧配置只在用户明确同意后用于最终启动验证；自动测试和开发过程不读取、打印或修改其中的 secret。
