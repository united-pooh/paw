# Paw 配置系统 v2

Paw v2 把连接信息（Provider）与模型信息（Model）分开管理，使用支持注释和尾逗号的 JSONC，并在运行中安全热重载。

## 配置目录

默认全局目录来自 Go 的 `os.UserConfigDir()`：

- Windows：`%AppData%\Paw`
- Linux：通常为 `$XDG_CONFIG_HOME/Paw` 或 `~/.config/Paw`
- macOS：`~/Library/Application Support/Paw`

可以用 `PAW_CONFIG_HOME` 完整覆盖该目录。目录内容：

```text
Paw/
├── config.jsonc
├── settings.json
├── mcp.toml
├── schemas/
│   └── config-v2.schema.json
└── skills/
```

工作区自己的 sessions、attachments、exports 仍保存在项目 `.paw/`。项目可选的 `.paw/config.jsonc` 只允许覆盖 `activeModel`、`stream` 和模型 `parameters`；Provider endpoint、auth、headers、body 等安全边界不能由项目覆盖。

查看当前路径：

```text
/config path
```

## 全局 `config.jsonc`

下面是一个同时包含托管 Provider 和本地 Provider 的完整示例：

```jsonc
{
  "$schema": "./schemas/config-v2.schema.json",
  "schemaVersion": 2,
  "activeModel": "deepseek/chat",

  "providers": {
    "deepseek": {
      "preset": "deepseek",
      "transport": "openai-compatible",
      "endpoint": "https://api.deepseek.com",
      "auth": {
        "credential": "provider/deepseek",
        "env": ["DEEPSEEK_API_KEY"]
      },
      "timeoutSeconds": 60,
      "retries": 3,
    },

    "local": {
      "transport": "openai-compatible",
      "endpoint": "http://127.0.0.1:11434/v1",
      "timeoutSeconds": 120,
      "retries": 0,
    },
  },

  "models": {
    "deepseek/chat": {
      "provider": "deepseek",
      "name": "deepseek-chat",
      "adapter": "deepseek",
      "contextWindow": 128000,
      "stream": true,
      "capabilities": {
        "tools": true,
        "reasoning": true
      },
      "parameters": {
        "temperature": 0.2
      }
    },

    "local/llama": {
      "provider": "local",
      "name": "llama3.2",
      "adapter": "openai-compatible"
    }
  }
}
```

### Provider 字段

- `preset`：可选内置预设；支持 `openai`、`anthropic`、`deepseek`、`openrouter`、`ollama`、`custom`。
- `transport`：`openai-responses`、`openai-compatible` 或 `anthropic-compatible`。
- `endpoint` / `apiPath`：连接地址与可选路径；未写 `apiPath` 时按 transport 补默认值。
- `auth.credential`：系统凭据库中的稳定 ID。
- `auth.env`：按顺序尝试的环境变量名；填写 `DEEPSEEK_API_KEY` 这类变量名，不使用 `${DEEPSEEK_API_KEY}` 插值语法。
- `headers`：附加的非敏感请求头。`Authorization`、`X-Api-Key` 等认证头必须通过 `auth` 配置。
- `body`：Provider 级附加请求参数；不能覆盖 `model`、`messages`、`input`、`stream` 等受保护字段。
- `proxy`：Provider 级代理覆盖（`{ "mode": "auto" | "direct" | "custom", "url": "..." }`）。`direct` 强制直连（忽略环境变量代理），`custom` 使用 `url` 指定的代理，缺省/`auto` 使用环境变量。未配置时继承全局 `proxy`。
- `timeoutSeconds`、`retries`、`stream`：Provider 默认请求策略。`retries: 0` 和 `stream: false` 都是有效的显式设置。

### 全局代理

`config.jsonc` 顶层 `proxy` 是全局默认代理，Provider 级 `proxy` 可覆盖：

```jsonc
{
  "proxy": { "mode": "direct" },

  "providers": {
    "openrouter": {
      "proxy": { "mode": "direct" }
    },
    "local": {
      "proxy": { "mode": "custom", "url": "http://127.0.0.1:7890" }
    }
  }
}
```

语义：`auto`（缺省）走进程环境变量（`HTTP_PROXY`/`HTTPS_PROXY`）；`direct` 强制直连，可解决「环境代理对某服务商不可达」的问题；`custom` 固定使用指定代理 URL（URL 缺失或非法时回退直连）。模型请求与模型 discovery 都遵循该配置；修改后模型请求热更生效，重新 discovery 需重启 Paw。

### Model 字段

- map key 是稳定模型 ID，供 `activeModel`、`PAW_MODEL` 和 `/model <id>` 使用。
- `provider`：引用 Provider ID。
- `name`：发送给上游 API 的真实模型名。
- `adapter`：可显式指定 `gpt`、`deepseek`、`openai-compatible`；省略时沿用兼容推断。
- `contextWindow`、`stream`、`capabilities`、`parameters`：模型级元数据与覆盖值。

## 项目覆盖

项目 `.paw/config.jsonc` 示例：

```jsonc
{
  "schemaVersion": 2,
  "activeModel": "deepseek/chat",
  "models": {
    "deepseek/chat": {
      "stream": false,
      "parameters": {
        "temperature": 0.1
      }
    }
  }
}
```

任何 Provider 字段，或模型中的 `provider`、`name`、`adapter`、`contextWindow`、`capabilities`，都会被拒绝。项目因此不能借用用户全局密钥连接任意 endpoint。

## 凭据

解析顺序固定为：

1. `auth.credential` 指向的系统凭据库；
2. `auth.env` 中第一个非空环境变量。

macOS 使用 Security.framework Keychain；`CGO_ENABLED=0` 的 macOS 构建回退为 env。Windows 使用 Credential Manager。没有可用 Secret Service 的 Linux 环境只使用 env。配置、界面和日志不会写入或回显完整密钥。

可以在 `/config` → Credentials 中写入、替换或删除 keyring 项。删除活动 Provider 的最后一个凭据前，需要先切换模型或配置有效的 env fallback，避免运行时继续依赖已经撤销的连接。

## 首次启动

当 `config.jsonc` 不存在时：

1. 有效 `PAW_MODEL` 优先；
2. 只有一个凭据完整的内置 Provider 时自动生成并激活；
3. 多个候选时，交互模式打开配置中心要求选择；
4. 没有候选时生成 starter config，交互模式打开凭据配置；
5. `-p`、subagent worker 等无头模式返回 `setup-required`，其中包含配置路径和修复命令。

## 配置中心与命令

`/setting` 与 `/config` 打开同一个配置中心。配置中心包含：

- General settings
- Providers
- Models
- Active model
- Credentials
- Connection（全局代理）
- Diagnostics

Provider 和 Model 支持添加、编辑、删除；删除 Provider、Model 或凭据需要二次确认。timeout、retries、stream、context window 与 capabilities 可直接编辑，headers、body、parameters 使用即时 JSONC 校验的高级编辑器。编辑页可按 `Ctrl+S` 或 `Enter` 显式保存，成功后显示 3 秒 `Saved` 提示；凭据输入始终显示为掩码。

「连接」页配置全局代理（模式：环境变量/直连/自定义 + 代理地址）；每个 Provider 的动作页也有「代理模式/代理地址」用于覆盖全局。代理改动即时热更模型请求；重新触发模型 discovery 需要重启 Paw。

辅助命令：

```text
/config reload
/config status
/config path
/model
/model <stable-model-id>
```

配置中心打开期间发生外部修改时，会显示 revision 已过期。保存采用乐观并发；旧草稿不会覆盖新配置，冲突后草稿仍保留。

## 热重载语义

Paw 监听全局和工作区配置文件的父目录，因此兼容编辑器的“写临时文件再原子替换”保存方式。事件会去抖，并按全局+工作区内容 hash 去重。

候选配置发布前依次完成：

- JSONC 语法与 JSON Schema；
- Provider/Model 引用；
- 项目安全覆盖；
- 受保护请求字段；
- 活动 Provider 凭据。

失败时保留 last-known-good 快照，并在 Diagnostics 中记录文件与原因。运行时删除配置文件只告警，不会自动重建。

每个模型请求开始时只读取一次运行时配置。热更之后：

- 新请求完整使用新 endpoint、headers、adapter、timeout 和参数；
- 已开始的普通或流式请求继续使用旧快照；
- 不会出现“旧请求体 + 新凭据”混合状态。

## v1 迁移

首次创建新目录时，Paw 会从旧 `~/.paw` 复制：

- `settings.json`
- `mcp.toml`
- `skills/`
- v1 `config.json`

`modelProfiles[]` 会转换成 Provider 与 Model；原目录不删除，新目录保留 `config-v1.backup.json` 和 `.migration-v2.json`。目标文件已存在时不覆盖，重复启动不会重复迁移。

v1 明文 API key 只有在成功写入系统凭据库后才完成迁移。凭据库不可用时，v1 保持原状，交互模式进入诊断/配置流程，无头模式返回明确的 setup-required。

## 配置 Manager API

`internal/config.Manager` 提供：

- `Snapshot()`：不可变文档、活动运行时配置、revision、diagnostics；
- `Update(ctx, expectedRevision, operations)`：定点 JSONC 更新、乐观并发、原子保存；
- `Reload()`：显式重新解析文件和凭据；
- `Subscribe()`：发布成功快照；
- `ConfigPath()`、`Paths()`、`Close()`。

`CredentialStore` 提供 `Get`、`Set`、`Delete`，测试使用内存 fake，业务代码不依赖真实用户 HOME 或凭据库。
