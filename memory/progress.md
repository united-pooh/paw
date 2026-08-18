# Progress

- [x] 确认 ANSI 控制序列泄漏的具体渲染路径与根因 <!-- todo:investigate -->
- [x] 调整终端命令结果渲染，避免 shell 输出被重新包装为 OSC 8 超链接，并补回归测试 <!-- todo:fix -->
- [x] 运行 gofmt、相关测试及 go test ./... <!-- todo:verify -->
- [x] 确认旧 config.json 兼容路径、discover 触发条件及迁移方案 <!-- todo:design -->
- [x] 实现 /config 通用设置中的 YOLO 配置，并停止旧 config.json 支持 <!-- todo:implement -->
- [x] 补充/更新测试与文档，运行 go test ./... <!-- todo:tests -->
- [x] 检查 provider 配置架构与相关设计文档 <!-- todo:inspect -->

## Actor 运行时重构（docs/spec-actor-runtime-refactor.md）

- [x] P0 清场：删 eventing/ipynb/py/hello 死包，仓库异物清零 <!-- todo:p0 -->
- [x] P1 装配收敛：buildRunner 9 元组 → appContext；plan/goal_controller 迁入 internal/ <!-- todo:p1 -->
- [x] P2 hook 收编：Hook 链 + 12 内聚协作者，Runner 字段 51→23 <!-- todo:p2 -->
- [x] P3 actor 内核：internal/actor 全量落地（分片单写者/Journal-First+Outbox/监督隔离/持久化定时器/虚拟时钟），覆盖 92.4%，-race 全绿，I1-I6 + 崩溃矩阵全过 <!-- todo:p3 -->
- [ ] P4 TaskActor 换壳：task/manager→actor；StopOwnedTasks→Tell(Stop)；task 流事件化；旧内存态机删除（ADR-11）；行为等价 <!-- todo:p4 -->
  - [ ] P4-1 勘察：worker/pool/persona/launcher 与 Manager 消费方清单（bootstrap/tool_registration/bubble/loop Runner 四处） <!-- todo:p4-1 -->
  - [ ] P4-2 TaskActor（事件化 task 流，meta.json 兼容双写）+ RegistryActor（索引/WaitAny/订阅）+ Host 装配 <!-- todo:p4-2 -->
  - [ ] P4-3 Manager 换壳为 facade，删除旧内存态机 <!-- todo:p4-3 -->
  - [ ] P4-4 行为等价验证：manager_test 全绿 + golden 事件流 + 崩溃恢复用例；spec 更新 <!-- todo:p4-4 -->
- [ ] P5 SessionActor：loop 引擎入住；session JSONL 与 es 合流；权限门→Suspend+Decision；goal/plan 会话恢复（fold 重建 activeGoalID/activePlanID） <!-- todo:p5 -->
- [ ] P5 订阅总线：领域事件显示流（Ephemeral 通道）+ 合规落库 <!-- todo:p5-bus -->
- [ ] P6 loopRunner 接入换壳 <!-- todo:p6 -->
