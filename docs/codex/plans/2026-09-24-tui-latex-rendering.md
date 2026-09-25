# TUI LaTeX 与 tikzcd 实验执行清单

用户已批准本方案并要求实施。分支 `codex/tui-latex-rendering`，不推送、不安装。

## 架构与接口

现有 `renderMarkdown` / 行内入口识别数学片段，公式与图形输出直接进入终端布局，不重新经过 Markdown。原始 transcript 不变。`PAW_TUI_TEX=0` 关闭实验。

公式使用固定版本 `github.com/doug/termtex v0.0.0-20260918165034-c407d5cf5251`。适配层先检查命令、环境、输入预算；返回 success/incomplete/unsupported/overflow 状态。行内单行；超宽块公式使用保留分组、矩阵行列和条件的线性表示。tikzcd 解析出行列节点与带标签有向边，宽度/碰撞不满足则显示完整关系文本。

## 执行切片

- [x] 识别回归：`markdown_tex_test.go` 覆盖四种公式定界符、代码/链接保护、金额、未知命令、未闭合流；先运行 `go test ./internal/ui/bubble -run TestTeX -count=1` 观察失败。
- [x] 公式适配：`tex_math.go` 实现验证、二维调用和递归线性表示；`markdown_tex.go` 负责定界符与原文隔离；`markdown.go` 接入块、行内和表格分列。
- [x] 图形：`tikzcd_test.go` 先覆盖方向/标签/重复边/空节点/中文与超宽；`tikzcd.go` 实现解析、布局、碰撞检测与关系文本。
- [x] 集成：公式标题/列表/引用/表格、流式每个前缀、恢复、选区、缩放；原文不变；二维块不走代码截断。
- [x] 验证：Bubble 全包、相关 race、全仓 vet、隔离构建、diff 检查与基准；60/100/140 列、深浅主题/无色渲染证据；隔离 PTY 流式与缩放。

## 验收命令

```sh
go test ./internal/ui/bubble -count=1
go test -race ./internal/ui/bubble -run 'TeX|TikZ|Selection|Stream' -count=1
go test ./internal/ui/bubble -run '^$' -bench 'BenchmarkTeX' -benchmem
go vet ./...
go build -o .agent/bin/paw-tui-latex ./cmd/paw
git diff --check
```

实际输出及截图保存 `.agent/visual/tui-latex/`，验证记录注明合成 View 或真实 PTY，不使用当前用户会话/模型服务。
