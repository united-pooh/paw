# Gotchas

- Preserve the existing `/api/state`, `/events`, and `/healthz` semantics unless a separately approved contract change is required.
- Do not store trace data, filters, selections, errors, credentials, or request bodies in the persisted Dockview layout.
- Narrow-screen fallback must not overwrite the saved desktop layout.
- Linked selection highlights related data; it must not silently filter other panels.
- Do not claim UI completion from tests alone; verify the rendered workflow and record visual evidence.
- 更纱黑体 Sarasa Mono SC 混用日文新字形（如「店」「置」「径」显示为日文写法），不能推荐给需要简体字形的中文用户；简体终端字体优先 Maple Mono NF / 苹方 fallback。
- Ghostty 配置里 `font-style = Bold` 会把所有文本强制粗体，导致 markdown 加粗无法区分；排查「加粗不生效」先查终端全局 font-style，再查字体 Bold 变体。
- Maple Mono NF CN 的 CJK 字形同样混入日文写法（「径」「店」「置」等），与更纱黑体同类问题；简体中文终端字体可靠选择：Noto Sans Mono CJK SC（思源等宽官方简体，brew 无 cask，从 notofonts/noto-cjk Sans2.004 release 的 13_NotoSansMonoCJKsc.zip 手动安装）或苹方 fallback。
- 判断字体字形风格（SC vs JP）时，跨字体像素/轮廓对比不可靠（不同字体设计差异 0.3+ 会淹没风格差异）；以官方文档声明和用户视觉为准。

## Token Tracer docking workspace (2026-08-13)

- Dockview 7's default `dndStrategy: 'auto'` drives MOUSE drags through native HTML5 DnD, which Playwright's `mouse.*` cannot trigger; use `dndStrategy="pointer"` so `mouse.down/move/up` works, and dwell ~250ms at the drop point before `mouse.up`.
- Dockview drop overlays shift the layout mid-drag: measure the target panel/tab position AFTER the drag starts, then move to the re-measured point.
- A tab-to-tab drop must land on the LEFT/right half of the target tab (the center falls between drop zones).
- When two panels share a group, the inactive tab's content is NOT in the DOM; assert docking via the shared `.dv-tabs-container` strip or `panel-tab-*`, not the hidden panel body.
- `panel.api.isMaximized()` is not reactive: subscribe to `containerApi.onDidMaximizedGroupChange` to refresh header-action labels.
- Panel content must subscribe to `api.onDidLocationChange` to re-render `data-location` after floating.
- Per-group `rightHeaderActionsComponent` renders for the ACTIVE panel only; render it in every group and it can overlap tabs when too wide (keep compact, no titles).
- The tracer keeps only the latest 2,000 events: emit deterministic filler events BEFORE structural turn events so the timeline still has agent rows and errors.
- vitest 4 + jsdom 29 ship a hollow `localStorage` and no `matchMedia`; stub both in `src/test/setup.ts`.
- `npm exec --prefix` does not change cwd; run playwright from the dashboard directory.

## Transcript envelope migration (2026-08-15)

- session transcript 写入已统一为 `es.Envelope` 信封格式（appendRecords → recordToEnvelope 无条件写），任何**直接读 transcript.jsonl 的代码**必须用 `session.ParseTranscriptLine`（或 store 的 `LoadResolvedRecords`），不能直接 unmarshal 到 `session.Record`——envelope 行 unmarshal 不报错但 message 全空（消息在 payload 里），表现为"静默空白"而非报错，极难排查。
- 排查此类问题的快捷路径：先看 `internal/session/record_envelope.go` 的 `isEnvelopeLine` 使用方 vs transcript 实际文件格式。

## Activity 面板渲染形态 (2026-08-15)

- Activity（subagent/pipeline 选择器）已从居中 modal 改为右侧边栏：渲染入口在 renderTranscriptRegion（subagentPicker != nil 时 overlayAlignRight 合成），**不要**再在 renderActiveModalBox 里加 subagentPicker 分支，否则会左右重复渲染。
- 面板打开时悬浮任务卡（renderSubagentTaskCard）被跳过（m.subagentPicker == nil 才渲染），避免同屏两份任务列表。
- 面板宽度受 activityPanelWidth 约束（min(60, contentWidth/2)），改宽度时同步更新 activity_side_panel_test.go 的宽度断言。

## 共享样式令牌 (2026-08-15)

- `StyleSet.Selected` 是**跨 UI 共享**的选中令牌：设置页 tab 反色块、配置中心
  列表行、主题选择器、provider 选择器都在用。为了 question dock 归一化而
  删除它的背景（只留前景色）会同时弄坏所有使用点——设置项选中行文字在
  浅色主题下隐形（`SelectedProviderFG` 浅色主题下等于背景色，它只设计来配
  `SelectedProviderBG` 的 pill 背景读），tab 反色块也变透明。
- 改共享令牌前先 `grep -rn 'styles.Selected' internal/ui/bubble` 找全使用点；
  行级列表统一用 `SelectionSelected`/`SelectionFocusedSelected`
  （`SelectionBackground`/`SelectionForeground` 是经过双对比度验证的选区配色，
  深浅主题都可见），紧凑 pill 场景才用 `Selected`。
- `visual_features_test.go` 的 TestSelectionTokensStayVisibleInDarkAndLightThemes
  锁定了三个令牌必须同时有背景+前景，防止再退化。
