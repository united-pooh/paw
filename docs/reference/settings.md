# `internal/platform/settings`

文件: [settings.go](../../internal/platform/settings/settings.go)

#### `type Config struct`

字段:
- `Subagent` (`SubagentConfig`) — 子 agent 默认配置
- `UI` (`UIConfig`) — UI 配置

##### `SubagentConfig`

字段:
- `DefaultContextMode` — `"empty"` 或 `"fork"`
- `DefaultRunMode` — `"sync"` 或 `"background"`

##### `UIConfig`

字段:
- `Theme` — 内置主题 ID，非法值回退到 `default`；`default` 使用终端默认前景/背景和 ANSI 调色板，可保留终端透明背景
- `ContextLimitTokens` — 通用上下文 token 上限（默认 `0` 表示自动；正整数为通用值，负数无效，模型专属窗口优先）
- `ContextMeterLocation` — context meter 位置（`"input-title"` / `"header"` / `"input-above"`）

#### `type Controller struct`

职责:
- 加载、保存、提供运行时配置
- 线程安全读写

##### `NewDefaultController(homeDir HomeDirFunc) (*Controller, error)`

职责:
- 兼容构造器；应用启动链改用解析后的显式 `settings.json` 路径创建 Controller，测试不再修改 HOME

##### `CurrentSettings() Config`

职责:
- 获取当前配置（线程安全，nil 安全）

##### `SaveSettings(cfg Config) error`

职责:
- 持久化配置到磁盘并更新内存（线程安全）

##### `Normalize(cfg Config) Config`

职责:
- 规范化配置字段值（大小写无关，非法值回退到默认）

