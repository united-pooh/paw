# Activity Right Sidebar — Visual Evidence

- **Changed files:**
  - `internal/ui/bubble/layout.go`（overlayAlignRight 合成、任务卡隐藏条件、renderActiveModalBox 移除 Activity 分支）
  - `internal/ui/bubble/activity.go`（renderActivityBox 改为右侧面板尺寸 + activityPanelWidth/Height）
  - `internal/ui/bubble/app.go`（ctrl+g toggle 语义，上一条修复保留）
  - `internal/ui/bubble/activity_side_panel_test.go`（新增 3 个测试）
  - `README.md`
- **Route / URL:** temporary local capture page generated from the real `appModel.View()` output
- **Viewport:** browser `1300 × 640`; rendered TUI frame `110 × 26` terminal cells
- **Artifacts:** `activity-side-panel-open.png`（ctrl+g 打开右侧面板）、`activity-side-panel-card.png`（仅运行中悬浮任务卡）
- **Observed result:**
  - open：Activity 面板贴右边界（右缘距终端框右缘约 1 列呼吸空间），宽 = min(60, contentWidth/2)，垂直居中；内容含标题、Subagents/Pipeline 双 tab、任务列表（`> 高松灯 3m 0s` 选中行 + `✓ 二叶筑 done`）与快捷键提示行；左侧 transcript 内容保留可见。
  - card：面板未打开时右侧仅显示运行中任务小卡（`subagents · 1 运行中` + `◐ 高松灯`），与 open 面板右缘完全对齐。
  - 独立验证（subagent 像素分析）：open 面板 x=[436,858]、card 卡片 x=[615,858]，右缘同为 857–858、距框右缘 12px，均垂直居中；内容行数与预期一致。
- **Known caveat:** card 截图右侧有一条 21px 的游离竖线伪影（截图层渲染伪影，非 TUI 输出；两图同一合成流程，open 图无此现象）。
