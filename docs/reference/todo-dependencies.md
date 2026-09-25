# TODO 前置依赖

`update_todo` 维护当前会话的完整 TODO 快照。执行原任务时发现需要先解决的问题，可以把该问题加入列表，并在原任务的 `blocked_by` 中引用它的 ID。

`status` 仍只有 `pending`、`in_progress`、`completed` 三种状态。阻塞由依赖项的当前状态计算：`blocked_by` 中任意一项尚未 `completed`，该任务就尚不能开始或完成，应保持 `pending`。所有前置项完成后，阻塞自动解除；依赖关系可以保留，无须手动删除 `blocked_by`，随后可把原任务更新为 `in_progress`。

## 完整更新示例

假设正在交付功能时发现数据加载问题。先把修复作为前置任务，原任务回到等待状态：

```json
{
  "explanation": "先修复数据加载，再继续功能交付",
  "items": [
    {
      "id": "fix-loader",
      "content": "修复数据加载并验证",
      "status": "in_progress"
    },
    {
      "id": "deliver-feature",
      "content": "完成原功能并验证",
      "status": "pending",
      "blocked_by": ["fix-loader"]
    }
  ]
}
```

修复完成后，提交下一份完整快照，继续原任务：

```json
{
  "explanation": "数据加载已验证，继续功能交付",
  "items": [
    {
      "id": "fix-loader",
      "content": "修复数据加载并验证",
      "status": "completed"
    },
    {
      "id": "deliver-feature",
      "content": "完成原功能并验证",
      "status": "in_progress",
      "blocked_by": ["fix-loader"]
    }
  ]
}
```

每次调用都提交完整、有序的 `items` 列表，并保持各项 ID 稳定；这不是增量更新。最多允许一个 `in_progress` 项。`blocked_by` 引用同一快照中的任务 ID，可包含多个前置任务，也可形成多层依赖，但不能引用自身、不存在的 ID 或形成循环。依赖 ID 必须为非空字符串，去除首尾空白后不能重复；保存时按 ID 排序。被依赖的已完成项应保留在快照中；如果要删除它，也需要同时更新引用它的任务。

未提供 `blocked_by` 或提供空数组表示无前置依赖。旧会话中的 TODO JSON 不需要迁移。依赖随 TODO 快照保存，重开会话后恢复；状态压缩上下文会同时保留声明的依赖和当前尚未完成的依赖。

依赖校验针对每次提交的新快照。需要重新规划时，可以显式修改或移除依赖；普通进度更新应保留原有依赖，省略字段会被视为解除该依赖，而不是保留旧值。

## 继续执行的边界

依赖解除表示任务已具备继续执行的条件，不会自动把任务标成已完成。Agent 应继续完成可执行的任务，并通过 `update_todo` 及时更新进度。

继续执行仍受现有预算、用户取消、暂停和会话运行状态约束。依赖功能不会绕过这些限制，也没有独立的后台唤醒或定时器；已停止的会话需要用户继续后才能执行下一步。
