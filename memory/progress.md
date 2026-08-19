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
- [x] P4 TaskActor 换壳：task/manager→actor；StopOwnedTasks→Tell(Stop)；task 流事件化；旧内存态机删除（ADR-11）；行为等价 <!-- todo:p4 -->
  - [x] P4-1 勘察：worker/pool/persona/launcher 与 Manager 消费方清单（bootstrap/tool_registration/bubble/loop Runner 四处） <!-- todo:p4-1 -->
  - [x] P4-2 TaskActor（事件化 task 流，meta.json 兼容双写）+ RegistryActor（索引/WaitAny/订阅）+ Host 装配 <!-- todo:p4-2 -->
  - [x] P4-3 Manager 换壳为 facade，删除旧内存态机 <!-- todo:p4-3 -->
  - [x] P4-4 行为等价验证：manager_test 全绿 + golden 事件流 + 崩溃恢复用例；spec 更新 <!-- todo:p4-4 -->
- [x] P5 SessionActor：loop 引擎入住；session JSONL 与 es 合流；权限门→Suspend+Decision；goal/plan 会话恢复（fold 重建 activeGoalID/activePlanID） <!-- todo:p5 -->
- [x] P5 订阅总线：领域事件显示流（Ephemeral 通道）+ 合规落库 <!-- todo:p5-bus -->
  - [x] P5-1 Engine：Runner 终名、字段收敛、Ephemeral display bus 与行为 golden <!-- todo:p5-1 -->
  - [x] P5-2 SessionActor：Durable turn、Host、transcript 单流 adapter 与 activation-aware resume <!-- todo:p5-2 -->
  - [x] P5-3 恢复矩阵：inbox/partial/tool-started/tool-result/pending-permission fold 与幂等处理 <!-- todo:p5-3 -->
  - [x] P5-4 权限门：工作区外 Read allow-once/deny、批次预检与 Bubble selection dock <!-- todo:p5-4 -->
  - [x] P5-5 Goal/Plan：会话绑定、Plan snapshot、控制器重绑定、恢复提示与 `/plan resume` <!-- todo:p5-5 -->
  - [x] P5-6 原子切换与验收：生产调用方只经 SessionActor，旧执行路径删除，全量门禁通过 <!-- todo:p5-6 -->
- [x] P4–P5 第三轮审查修复轮（spec §15）：H2/H3/H4/M1/M2 修复、Kind 空值归一、system.go/cell.go 拆分（<250 行）、内核管理端口单测、P5 事件流级 golden 三路径、flaky 测试受控时钟化、bench 基准入库 <!-- todo:review-round -->
- [ ] P6 loopRunner 接入换壳（前置债务：loop 包扇出 11→≤8；Durable 跨消息 group commit；流式端到端与常驻内存基准，见 spec §10/§15） <!-- todo:p6 -->
