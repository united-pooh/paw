# Config Center — 模型与当前模型合并设计

日期：2026-08-14
状态：已实现并通过回归验证

## 背景

配置中心原有 `模型` 与 `当前模型` 两个一级 Tab。两页都读取
`Snapshot.EffectiveModels` 并展示同一模型目录：

- `模型`：进入模型动作页，支持激活、编辑、删除和注册 discovered-only 模型；
- `当前模型`：只提供激活操作。

推理开关和推理强度迁入 `通用` 后，`当前模型` 只剩一份重复的选择列表，继续占用
一级导航会增加认知和切换成本。

## 决策

删除 `当前模型` 一级 Tab，把激活能力保留在 `模型` 页的既有动作流中。代码层仍可
保留“目录选择”和“事务激活”的独立函数，界面层只保留一个入口。

顶部导航固定为：

```text
通用 / 服务商 / 模型 / 凭据 / 诊断
```

## 模型页交互

- 列表继续读取 `Snapshot.EffectiveModels`，同时展示 configured/discovered 来源。
- 当前激活模型排在列表首位，并在说明中显示 `当前` 标记。
- `Enter` 直接将所选目录项设为当前模型，并停留在合并后的模型列表：
  - configured 模型只切换 active model；
  - discovered-only 模型执行“设为当前并注册”，继续使用绑定 revision 与身份的
    `CatalogSelection` 事务激活。
- `Space` 进入所选模型的管理动作页；configured 模型可编辑、切换能力和参数、删除，
  discovered-only 模型只提供事务注册与激活。
- `+ 添加模型` 保持列表末尾，`Enter` 或 `Space` 都可进入添加流程。
- 首次配置已有模型但尚未设置 active model 时，直接进入 `模型` 页，不再进入隐藏或
  孤立的 `当前模型` 页面。

## 保持不变

- 推理开关、推理强度仍位于 `通用`，持久化到当前激活模型的 config-v2 parameters。
- 模型编辑、删除确认、discovered-only 单项注册及 TOCTOU/revision conflict 保护不变。
- `/model <id>` 命令和运行时同步不变。
- 顶部 Tab 仍支持 `Tab`、`←`、`→` 循环导航。

## 测试

- 顶部只渲染五个 Tab，且从 `模型` 向右进入 `凭据`。
- 模型列表包含 configured/discovered 项，当前模型置顶并带标记。
- configured 与 discovered-only 模型都能从模型动作页设为当前。
- stale `CatalogSelection` 仍被 revision conflict 拒绝。
- 无 active model 的首次配置落到模型页。
- `go build ./...`、`go test ./...`、`git diff --check` 全部通过。
