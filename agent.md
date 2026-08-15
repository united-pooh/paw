# Paw 项目指令（本仓库）


- 本项目是 **Paw 本体**（Go 语言）：任何改动后先 `go build ./...`、`go test ./...`
  验证通过再交付。
- 遵循仓库既有分层（internal/loop、internal/model、internal/ui 等）；新功能先查
  docs/ 下的设计文档再动手。
- **进度分工**：在规划任务分步执行或者多项调查的时候启用。会话内任务过程用 `update_todo` 工具跟踪（可恢复、TUI 可见）；
  `memory/*.md` 是跨会话档案，只在阶段完成时沉淀结果（completed 记录、教训、
  verify 标准），不逐条镜像 todo 状态。
