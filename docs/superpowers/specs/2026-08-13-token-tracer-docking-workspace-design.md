# Token Tracer 高密度 Docking Workspace 规格

**状态：** Approved

**日期：** 2026-08-13

**涉及模块：** `internal/tokentracer`、新增 Token Tracer 前端工程

## 1. 背景

当前 Token Tracer dashboard 以真实墙钟时间为横轴，每个 turn 或 agent 独占一行。对于串行且持续时间较短的调用，大量画布用于表示空白时间，条形本身又窄到无法容纳名称、Token 构成与精确数值。页面虽然能表达调用先后和时长，却不能在一屏内高效回答以下诊断问题：

- 哪些调用消耗最多 Token；
- 输入、缓存读取、缓存创建与输出分别占多少；
- 哪些调用吞吐异常、失败或形成热点；
- 调用之间是否重叠；
- 某个异常调用在整体运行中的位置和上下文；
- 大量历史调用和事件如何在有限空间内比较。

现有 dashboard 还是嵌在 Go 源码中的单文件 HTML/CSS/JavaScript。继续在该结构内手写 IDE 式拖拽、拆分、Tab 堆叠与布局持久化，会显著增加交互状态复杂度和维护风险。

## 2. 目标

1. 用三个互补的高密度视图取代单一稀疏甘特图：Calls Table、Token Heatmap、Folded Flame。
2. 提供 IDE 式 Docking Workspace，允许面板任意拖拽、横纵拆分、Tab 堆叠、缩放、最大化和关闭。
3. 固定顶部全局控制区，避免核心 KPI 与布局一起滚动或被关闭。
4. 让所有数据面板共享选中调用和时间范围，高亮联动但不隐式过滤。
5. 全局自动保存最后一次布局，并提供可靠的恢复默认与撤销能力。
6. 将 dashboard 迁移为可测试的 React + TypeScript 前端，同时保持 Go 本地服务和现有追踪接口。
7. 使用选定的“氛围柔和”视觉方向，降低硬边框和网格噪声，同时维持高信息密度。
8. 在 2,000 条保留事件与常见桌面窗口尺寸下保持流畅、可读和可恢复。

## 3. 非目标

本规格不负责：

1. 修改 Token 采集、`Usage` 归一化或 Timeline 聚合语义。
2. 将 Token Trace 上传到远程服务或引入服务端布局存储。
3. 支持多个命名布局模板；第一版只保存全局最后布局。
4. 记住每次运行的筛选、选中调用、缩放范围或面板局部排序。
5. 同时打开同一种面板的多个实例。
6. 将面板弹出为独立浏览器窗口；第一版只在当前窗口内 Dock 或浮动。
7. 提供移动端完整 Dock 编辑体验；窄屏只提供临时单列 Tab 降级。
8. 引入费用估算、Provider 配额或新的追踪事件类型。
9. 改变 `/api/state` 与 `/events` 的既有公开语义，除非实现中发现现有字段无法支持已确认视图，并另行获得批准。

## 4. 技术架构

### 4.1 前端技术栈

采用：

- React；
- TypeScript；
- Vite；
- Dockview React；
- 浏览器原生 Canvas、SVG、EventSource 与 `localStorage`。

选择 Dockview 的原因：

- 原生提供 Panel、Group、Tab、四向拆分、拖放和浮动布局；
- `api.toJSON()` / `api.fromJSON()` 可直接支撑布局持久化；
- React 有正式绑定；
- 主题可由项目 CSS 完整控制；
- 无需自行维护复杂的拖拽命中区域、树形布局和 Tab 生命周期。

Golden Layout 可实现相似能力，但 React 组件绑定和布局集成更复杂。手写 Docking 系统不在考虑范围内。

### 4.2 目录结构

目标结构：

```text
internal/tokentracer/
├── dashboard/
│   ├── src/
│   │   ├── app/
│   │   ├── components/
│   │   ├── panels/
│   │   ├── stores/
│   │   ├── trace/
│   │   └── styles/
│   ├── index.html
│   ├── package.json
│   ├── tsconfig.json
│   ├── vite.config.ts
│   ├── package-lock.json
│   └── dist/
├── dashboard_embed.go
├── server.go
├── timeline.go
└── tracer.go
```

源代码和构建产物分开管理。`package-lock.json` 与 `dist/` 均提交到仓库：前者锁定依赖，后者确保干净 checkout 无需 Node.js 也能直接执行 `go build ./...`。Vite 构建必须具有确定性；前端构建后若 `dist/` 出现差异，差异必须随源代码一起提交。

### 4.3 Go 静态资源承载

新增 `dashboard_embed.go`，使用 `go:embed` 承载构建后的 HTML、CSS、JavaScript 和其他静态资源。

`Server` 继续负责同一来源下的以下路由：

- `/`：前端入口；
- dashboard 静态资源路径；
- `/api/state`：完整快照；
- `/events`：SSE 事件流；
- `/healthz`：健康检查。

前端和 API 同源，不引入 CORS 配置。用户运行 Go 二进制时不需要安装 Node.js；Node.js 仅用于开发、测试与构建前端资源。

## 5. 页面信息架构

### 5.1 固定顶部区域

顶部不属于 Dockview 布局，始终固定并包含：

- 运行名称、运行状态与 SSE 连接状态；
- 运行时长；
- 调用次数；
- 总上下文；
- 缓存命中率；
- 输出 Token；
- 健康度；
- Scope、Model、异常等全局筛选入口；
- “添加面板”；
- “恢复默认布局”；
- 现有导出 JSON 能力。

固定区只展示全局状态和全局操作，不承载选中调用的详情。

### 5.2 默认 Dock 布局

首次打开或恢复默认时：

```text
┌──────────────────────────────┬──────────────────────┐
│ B Token Heatmap              │ C Folded Flame       │
├──────────────────────────────┤                      │
│                              ├──────────────────────┤
│ A Calls Table                │ Inspector | Events   │
│                              │                      │
└──────────────────────────────┴──────────────────────┘
```

- 左侧宽于右侧；
- Calls Table 获得最大面积；
- Heatmap 位于左上，承担全局模式扫描；
- Folded Flame 位于右上，承担层级和 Token 结构观察；
- Inspector 与 Events 默认堆叠为同组 Tab；
- 所有比例由 Dockview 默认布局定义，用户可任意调整。

## 6. Dock 面板

### 6.1 A：Calls Table

Calls Table 是默认精确读数面板，一行表示一个可诊断的 Timeline Row。目标行高约 22–26px，在常见桌面窗口中展示约 35–45 行。

默认列：

- 顺序或调用编号；
- scope / turn / agent 名称；
- 微型真实时间轴，显示开始位置、持续时间、重叠和失败标记；
- Token 构成堆叠条：input、cache read、cache creation、output；
- input 精确值；
- cache 精确值；
- output 精确值；
- throughput；
- 状态。

行为：

- 支持按开始时间、耗时、Token 总量、输出、吞吐和状态排序；
- 使用虚拟滚动；
- 单击选择调用并联动其他面板；
- 双击或 Enter 聚焦 Inspector；
- 当前时间范围以高亮或遮罩表示，不自动移除范围外行；
- 数值列保留完整 tooltip，窄列可显示紧凑格式。

### 6.2 B：Token Heatmap

Heatmap 用 Canvas 绘制，横轴为时间桶，纵轴可按 turn、agent 或 Timeline Row 展示。颜色编码 Token 活跃度，异常使用独立、可辨识的高优先级色。

行为：

- 支持滚轮或控件缩放时间轴；
- 支持拖拽框选时间范围；
- 悬停显示时间桶、调用、Token 构成、吞吐和错误摘要；
- 点击单元格选择最相关调用；多个调用落入同一桶时显示聚合值并在 Inspector 列出；
- 空白时间使用低对比背景，不绘制高对比网格；
- 大数据量下按像素宽度聚合，避免每个事件对应一个 DOM 节点。

### 6.3 C：Folded Flame

Folded Flame 使用 SVG 绘制，不按真实墙钟保留空白，而是按运行层级连续排布。

层级优先表达：

```text
run → stage / turn → agent / API call → tool or event cluster
```

行为：

- 宽度可在 `duration` 与 `tokens` 两种模式间切换；
- 默认使用 `tokens`，解决短调用在真实时间轴中几乎不可见的问题；
- 点击块选择对应调用并联动其他面板；
- 支持逐层 drill down 和返回上层；
- 失败块使用错误色，但不得仅靠颜色表达状态；
- 对过窄块省略文字，使用 tooltip 提供完整信息。

### 6.4 Inspector

Inspector 展示当前选中调用或时间桶的完整字段：

- ID、名称、kind、stage、agent、role、session；
- provider、model、调用次数；
- 开始、结束、耗时；
- input、cache read、cache creation、output、总 Token、占比、吞吐；
- 状态、错误；
- 相关 markers 和事件摘要。

未选择时显示全局摘要或明确的空状态，不自动选择失败行。

### 6.5 Events

Events 使用虚拟滚动展示事件流，并沿用现有重复/清理事件压缩语义。

行为：

- 支持事件类型和异常筛选；
- 点击事件选择对应调用或时间点；
- 错误详情可复制；
- 显示已隐藏重复事件数量；
- 不把完整、敏感请求内容加入页面或本地存储。

## 7. Docking 交互

### 7.1 支持能力

每个面板支持：

- 拖到容器四边形成横向或纵向拆分；
- 拖到现有 Group 标题栏形成 Tab；
- Group 内 Tab 排序；
- 拖动分隔条改变尺寸；
- 当前窗口内浮动；
- 最大化与恢复；
- 关闭；
- 从“添加面板”重新打开。

第一版不提供独立浏览器窗口 popout。

### 7.2 单实例约束

以下面板各自最多存在一个实例：

- Calls Table；
- Token Heatmap；
- Folded Flame；
- Inspector；
- Events。

已打开面板在“添加面板”菜单中标记为已打开并禁用。关闭后恢复为可添加状态。最后一个面板也允许关闭，顶部固定区仍保持可用。

## 8. 状态与数据流

### 8.1 TraceStore

`TraceStore` 是运行数据的唯一前端入口：

1. 页面启动时读取 `/api/state`；
2. 成功后建立 `/events` EventSource；
3. SSE 事件用于更新连接状态和触发节流后的快照同步；
4. 所有面板从同一规范化快照读取数据；
5. 不为每个 SSE 事件重绘整个工作台。

当前后端 SSE 只发送事件，不发送完整 Timeline 增量。第一版继续采用“事件提示 + 节流重新取快照”的一致性模型。默认节流窗口应通过性能测试确定，初始建议约 100–200ms，并保证同一窗口最多存在一个在途快照请求。

### 8.2 SelectionStore

`SelectionStore` 保存临时交互状态：

- `selectedRowID`；
- `selectedEventSeq`；
- `selectedTimeRange`；
- 选择来源面板。

联动规则：

- 任一面板选择调用，其他面板同步高亮；
- Heatmap 框选时间范围后，Table 和 Flame 显示范围高亮或遮罩；
- 联动不自动过滤或隐藏范围外数据；
- 点击空白处或执行“清除选择”清空联动状态；
- 运行快照更新导致目标消失时，安全清除对应选择。

### 8.3 FilterState

全局筛选和各面板的本地排序、缩放属于运行时 UI 状态：

- 不写入布局模板；
- 页面刷新后恢复默认值；
- 全局筛选由所有数据面板一致解释；
- 选择高亮与筛选是不同概念，不能互相隐式修改。

### 8.4 LayoutStore

布局存储在 `localStorage`，建议键名：

```text
paw.tokenTracer.layout.v1
```

存储 envelope：

```json
{
  "schemaVersion": 1,
  "savedAt": "2026-08-13T00:00:00Z",
  "layout": {}
}
```

规则：

- Dockview 布局变化后 300ms 防抖保存；
- 只保存 Panel ID、Group、尺寸、拆分、Tab 顺序、可见/关闭状态和当前 Tab；
- 不保存 trace 数据、错误内容、筛选、选择或缩放范围；
- 读取时验证 envelope、版本和面板 ID；
- 未知面板 ID 被拒绝，不传给组件工厂猜测；
- 损坏或不兼容值先备份为带时间戳的单一 recovery key，再加载默认布局；
- “恢复默认布局”先保留当前布局用于一次撤销，然后应用默认布局；
- 撤销入口短暂显示，过期后不再保留额外历史。

## 9. 响应式策略

宽屏使用保存的 Dockview 布局。进入窄屏阈值后：

- 不修改或覆盖桌面布局存储；
- 临时切换为单列 Tab 容器；
- KPI 变为多行紧凑排列；
- 所有面板仍可访问，但关闭复杂拖拽命中区；
- 返回宽屏时恢复之前的桌面布局。

具体阈值以浏览器验收为准，初始建议 900–980px。

## 10. 视觉设计

采用“氛围柔和”方向，但必须限制装饰，保证诊断密度。

### 10.1 基础质感

- 深色低饱和背景；
- 12–13px 面板圆角；
- 半透明深色面板与轻量 backdrop blur；
- 使用背景明度、环境光和阴影建立层级；
- 边框透明度低，默认不形成高对比“钢丝网”；
- 分隔条默认低对比，hover 和拖拽时增强；
- 表格行分隔极弱，hover 与 selected 状态承担定位。

### 10.2 信息编码

- input：蓝色；
- cache read：青绿色；
- cache creation：琥珀色；
- output：珊瑚色；
- failed：红色；
- selected：蓝色外发光或描边；
- time range：低透明遮罩或柔和边界。

错误、选中和运行状态不得只依赖颜色，还应有文字、图标或形状。

### 10.3 密度约束

- 圆角和留白不能把表格变为逐行卡片；
- 面板内边距保持紧凑；
- 数值使用等宽数字或 tabular numerals；
- tooltip 补充被压缩内容，但不能把常用精确值全部藏进 tooltip；
- Canvas Heatmap 和 SVG Flame 不绘制无意义的高对比网格。

## 11. 错误处理

### 11.1 初次加载失败

`/api/state` 请求失败时：

- 显示明确错误和重试按钮；
- 不渲染伪数据；
- 顶部连接状态反映失败；
- 重试成功后正常初始化 Dock 工作台。

### 11.2 SSE 断线

- 保留最后成功快照；
- 顶部显示“重新连接中”；
- EventSource 恢复后重新请求完整快照；
- 不把短暂断线误报成运行失败。

### 11.3 布局恢复失败

- 捕获 JSON 解析、版本、字段和 Dockview 恢复异常；
- 备份旧值；
- 恢复默认布局；
- 显示一次非阻塞提示；
- 不允许白屏或无限恢复循环。

### 11.4 面板异常

每个 Panel 使用独立 Error Boundary。单个图表或组件失败时：

- 仅该面板显示错误态和重试；
- 顶部、其他面板和布局操作继续可用；
- 错误信息不包含敏感数据。

## 12. 性能约束

- Calls Table 和 Events 使用虚拟滚动；
- Heatmap 使用 Canvas，并按实际像素聚合数据；
- Folded Flame 使用 SVG，必要时仅渲染当前可见或 drill-down 子树；
- 面板使用 memoized selector，避免无关状态更新触发全量重绘；
- SSE 触发快照刷新时进行节流、去重和单航班控制；
- ResizeObserver 更新图表尺寸时进行动画帧合并；
- 布局写入使用 300ms 防抖；
- 后端当前 `maxEventHistory = 2000`，前端必须在这一规模下正常滚动、筛选与选择。

## 13. 安全与隐私

- 布局存储不得包含 API key、Authorization、请求正文、错误堆栈中的敏感字段或完整 trace 快照；
- 面板参数仅允许已注册的固定面板类型；
- 恢复布局时拒绝未知组件名和不合法结构；
- 现有 HTML 转义和文本安全要求在 React 迁移后继续保持；
- 不使用 `dangerouslySetInnerHTML` 渲染事件或错误内容；
- JSON 导出继续由显式用户动作触发。

## 14. 测试与验收

### 14.1 前端单元测试

覆盖：

- TraceStore 初始加载、节流刷新、断线和恢复；
- SelectionStore 跨面板高亮与清除；
- FilterState 与 SelectionStore 互不隐式修改；
- LayoutStore 保存、读取、版本拒绝、损坏恢复、默认布局和撤销；
- Timeline Row 到 Table、Heatmap Bucket、Flame Node 的转换；
- Token 构成、吞吐和错误聚合。

### 14.2 组件测试

覆盖：

- 每类面板只允许一个实例；
- 关闭后可从“添加面板”恢复；
- 已打开面板在菜单中禁用；
- Table 选择联动 Heatmap、Flame 和 Inspector；
- Heatmap 框选只高亮，不自动过滤；
- Reset 应用默认布局；
- Reset 后撤销恢复上一个布局；
- Error Boundary 隔离单面板故障。

### 14.3 浏览器端到端测试

覆盖真实用户路径：

1. 打开 dashboard 并看到真实快照；
2. 拖拽面板形成左、右、上、下拆分；
3. 将面板拖入另一 Group 形成 Tab；
4. 调整分隔条并刷新，布局保持；
5. 关闭面板并从菜单恢复；
6. 恢复默认布局并撤销；
7. 从 Table 选择失败调用，其他面板同步高亮；
8. Heatmap 框选时间范围，Table 不丢失范围外行；
9. 模拟 SSE 断线和恢复；
10. 写入损坏布局后刷新，页面恢复且不白屏；
11. 窄屏进入临时单列 Tab，返回宽屏后桌面布局不变。

### 14.4 Go 测试

覆盖：

- 嵌入资源存在且入口可读取；
- `/` 返回前端入口；
- 静态资源 Content-Type 正确；
- 未知静态路径返回 404；
- `/api/state`、`/events`、`/healthz` 行为不回归；
- 关闭 Server 后静态和流式请求正确结束。

### 14.5 必须执行的验证

实现完成后至少执行：

```bash
npm --prefix internal/tokentracer/dashboard test
npm --prefix internal/tokentracer/dashboard run build
go build ./...
go test ./...
```

并启动真实 Paw Token Tracer dashboard，在桌面与窄屏视口各完成一次浏览器截图验收。视觉证据写入 `.agent/visual/`，包含：

- Changed files；
- Route / URL；
- Viewport；
- Artifact filename；
- Observed result。

## 15. 完成定义

同时满足以下条件才算完成：

1. A/B/C 三个高密度视图均使用真实 `/api/state` 数据；
2. 五类面板均能 Dock、拆分、Tab 堆叠、缩放、最大化、关闭和恢复；
3. 单实例约束生效；
4. 布局能自动保存、刷新恢复、Reset 和撤销；
5. 布局存储不包含运行数据或筛选状态；
6. 跨面板选择和时间范围高亮联动，不自动过滤；
7. SSE 断线、坏布局和单面板异常不会导致整页失效；
8. 2,000 条事件规模下 Table、Events、Heatmap 和 Flame 可正常交互；
9. 选定的“氛围柔和”主题通过桌面与窄屏视觉验收；
10. 前端测试与构建、`go build ./...`、`go test ./...` 全部通过。

## 16. 已确认决策

- Calls Table、Token Heatmap、Folded Flame 全部保留；
- 采用 IDE 式任意拖拽与拆分，而非预设布局切换；
- 全局自动保存最后一次布局，并提供恢复默认；
- 跨面板联动为高亮，不强制过滤；
- KPI 与全局筛选固定，Inspector 和 Events 进入 Dock；
- 接受前端构建流程与成熟 Docking 库；
- 采用 React + TypeScript + Vite + Dockview，并由 Go embed 发布；
- 每种面板只允许一个实例；
- 视觉采用“氛围柔和”；
- 第一版不做跨窗口 popout、多命名模板和多实例面板。
