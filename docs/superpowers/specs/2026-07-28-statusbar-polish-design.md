# Status Bar Polish — 均衡器过渡、速率、布局重设计

**Date:** 2026-07-28
**Scope:** `internal/ui/bubble` (header / status_line / equalizer / types / app)

## Context

当前 status bar 三处问题：

1. **idle↔generating 瞬间切换有割裂感**：均衡器在 idle（进度填充 `▆▆▆▆▃▁▁▁`）与 generating（波浪 `▂▄▆█▆▄▂`）之间硬切，无过渡。
2. **波浪太快**：`equalizerWaveStep=0.08` → ~2.6s/cycle，起伏急促。
3. **布局靠左、右侧大片留白**：header 左对齐 + 右补空格；status 均衡器固定 8 格上限，中段用空格 `gap` 填充，内容堆在左侧。

目标：均衡器成为 idle/generating 的**同一组件**（仅振幅不同），过渡连续；波浪放缓；header 与 status 充分利用全宽。

## 设计

### 1. 均衡器模型：fill + wave overlay（统一组件）

均衡器不再是两个分支，而是**进度填充基线 + 振幅缩放的正弦调制**：

```
level_i = clamp( round(base_i + amp * 3 * sin(2π·(i/cells − phase))), 0, 7 )
```

- `base_i`：由 `usedPct` 计算的填充高度（idle 形态 `▆▆▆▆▃▁▁▁` 的来源）。
- `amp ∈ [0,1]`：idle=0（纯填充），generating=1（满振幅波浪 ±3 格），过渡区间缓动。
- `amp=0` → 退化为纯填充；`amp=1` → 填充 ± 3-cell 波浪；结果 clamp 到 block 级别 `[0,7]`。
- `phase`：随 `spinnerFrameIdx * equalizerWaveStep` 推进，波浪横移。

这把 idle/generating 统一为一个连续函数，过渡天然无跳变。

**振幅缓动**（时间驱动、与帧率无关，沿用 `animatedLabelTokens` 模式）：

- `appModel` 新增：
  - `waveAmpTarget bool` — 目标态（= `isGenerating`）
  - `waveAmpStartedAt time.Time` — 目标态确立时刻
  - `waveAmpFrom float64` — 过渡起点振幅（用于反向退场）
- **变更检测集中在渲染时**（不改 5 处 `isGenerating=` 翻转点）：
  每次 `renderStatusMidSegment` 时，`target := m.isGenerating`；若 `target != m.waveAmpTarget`，记 `waveAmpStartedAt = now`、`waveAmpFrom = 当前amp`、`waveAmpTarget = target`。
- **amp 计算**：`p = clamp01((now − startedAt)/400ms)`；
  - 进入生成（target=true）：`amp = waveAmpFrom + (1 − waveAmpFrom) * easeOutCubic(p)`
  - 退出生成（target=false）：`amp = waveAmpFrom * (1 − easeOutCubic(p))`
  - `easeOutCubic` 已存在于 `context_meter.go`，复用。
- **时钟源**：`cursorFrameAt`（30fps，`cursorFrameMsg` 驱动）。不新增定时器，无累积漂移。
- **效果**：进入生成 → 波浪从填充渐起 400ms；退出 → 波浪落回填充 400ms。连续无割裂。

### 2. 波浪速率

- 现 `step=0.08`（~2.6s/cycle）→ 改 `equalizerWaveStep = 0.04`（~5.2s/cycle）。
- 缓慢起伏，不晃眼。作为具名 const 暴露，便于调参。
- spinner 速率不变（`idx/3`，~1s/cycle）——不同元素，互不干扰。

### 3. 布局：两端对齐 + 均衡器拉伸

**Header — 左右两组两端对齐，弹性 gap 居中：**

```
opus-4.8 · ⏱ 00:12          Σ 128k · 15:42
└── left group ──┘└─ gap ─┘└── right group ──┘
```

- 左组 `[model, timer]`（恒保留）。右组 `[session, clock]`（窄时先丢 clock、再丢 session）。
- 渲染：先算左组宽度 + 右组宽度，中间用空格填到 `width`（替换当前"左对齐 + 右补空格"）。
- 右组整体放不下时按优先级丢弃右组字段，保证不溢出。

**Status bar — 均衡器吃满中段，消除死空白：**

```
⠋ generating · ▂▄▆█▇▆▄▃▄▅▆▇▆▄▃ 45% · cache 2% · chat
└── left ──┘└── equalizer (cells = budget − text) ──┘└── right ──┘
```

- 均衡器格数 `cells = clamp(midBudget − (pctW + auxW), 4, 40)`。原上限 8 → 提至 40，波浪更宽更明显。
- 删除 mid 与 mode 之间的空格 `gap`，均衡器自身填满。
- 窄终端优先级：丢 aux(cache/free) → 丢 pct → 均衡器缩到 min 4 格。

**Input bar — 不在本次范围。** textarea 内容天然左对齐是正确的文本输入行为，非浪费空间。若后续指 textarea 本身再单独处理。

## 改动文件

| 文件 | 改动 |
|---|---|
| `equalizer.go` | `renderEqualizer` 签名加 `amp float64`；公式改 fill+wave overlay；`equalizerWaveStep=0.04` |
| `types.go` | `appModel` 加 `waveAmpTarget bool` / `waveAmpStartedAt time.Time` / `waveAmpFrom float64` |
| `status_line.go` | `renderStatusMidSegment`：渲染时检测 amp 目标变更 + 计算 amp；均衡器格数按预算拉伸（上限 40）；删 mid-mode 间空格 gap |
| `header.go` | `renderHeader`：改左右两组两端对齐（弹性 gap），替换左对齐+右补空格 |
| 测试 | `equalizer_test.go` 更新签名；新增 amp 边界（0/1/中间）、过渡连续性、拉伸格数、header 两端对齐用例；更新现有 status/header 测试 |

## 不做的事

- 不改 5 处 `isGenerating=` 翻转点（amp 目标在渲染时派生）。
- 不新增定时器（复用 `cursorFrameMsg`）。
- 不动数据源隔离边界（headerSnapshot / collectHeaderData / 纯渲染函数不变）。
- 不重构 input bar。
- 不改 spinner 速率。

## 验证

```sh
go build ./...
go test ./internal/ui/bubble/ -count=1
# 手动：go run . — 观察 idle↔generating 过渡 400ms 连续无跳变；波浪 ~5s/cycle；
#        header 两端对齐无右侧留白；status 均衡器拉伸填满中段。
```
