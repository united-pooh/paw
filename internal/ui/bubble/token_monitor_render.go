package bubble

import (
	"fmt"
	"math"
	"strings"
	"time"
	"unicode"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"paw/internal/runtime/loop"
	"paw/internal/tokentracer"
)

type tokenMonitorView struct {
	Snapshot tokentracer.MonitorSnapshot
	Rates    tokenRateView
	Context  loop.ContextStats
	Loading  bool
	Err      string
	Selected int
	Detail   bool
}

type tokenRateView struct {
	TurnAverage, TurnTokens                 float64
	TurnElapsed                             time.Duration
	TurnActive, TurnObserved, TurnEstimated bool
	Current, Average                        float64
	FirstText                               time.Duration
	FirstTextKnown, Active                  bool
	Observed                                bool
	RequestID                               string
	Samples                                 []float64
	ToolCalls                               int
	ToolDuration                            time.Duration
	ToolActive                              bool
	ToolCallsPartial                        bool
}

func renderTokenHistory(v tokenMonitorView, width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	title := wizardTitleStyle.Render(copyTokenHistory + " · " + copyMainTurn)
	if tokenHistoryEmpty(v) {
		// 空态单行：无任何数据时不渲染 8 行 — 骨架与「暂无流片段」，
		// 打开面板也能一眼看出还没有可统计的流。
		return fitStyledRect(title+"\n"+activityHintStyle.Render(copyEmptyData), width, height)
	}
	current := "—"
	if v.Rates.RequestID != "" && v.Rates.Observed {
		current = fmt.Sprintf("≈%.1f", tokenMonitorRate(v.Rates.Current))
	}
	lines := append([]string{title}, tokenMonitorTurnLines(v.Rates, width)...)
	summary := []string{activityHintStyle.Render(tokenMonitorPair("60s chart · recent", current+" tok/s", width))}
	s := v.Snapshot.Summary
	input := tokenMonitorKnown(s.Input, s.InputKnown, s.Attempts)
	output := tokenMonitorKnown(s.Output, s.OutputKnown, s.Attempts)
	if terminalCellWidth("Input "+input+" Output "+output) > width {
		summary = append(summary, tokenMonitorPair("Input", input, width), tokenMonitorPair("Output", output, width))
	} else {
		summary = append(summary, tokenMonitorPair("Input "+input, "Output "+output, width))
	}
	cacheHit := "—"
	if s.Attempts > 0 && s.InputKnown == s.Attempts && s.CacheKnown == s.Attempts && s.Input > 0 {
		cacheHit = fmt.Sprintf("%.1f%%", float64(s.CacheRead)*100/float64(s.Input))
	} else if s.CacheKnown > 0 && s.CacheKnown < s.Attempts || s.InputKnown > 0 && s.InputKnown < s.Attempts {
		cacheHit = "— partial"
	}
	cache := tokenMonitorKnown(s.CacheRead, s.CacheKnown, s.Attempts)
	if s.CacheKnown > 0 && s.CacheKnown == s.Attempts {
		cache += " · " + cacheHit
	}
	summary = append(summary, tokenMonitorPair("Cache hit", cache, width))
	tool := v.Snapshot.ToolTotal
	amount := tokenMonitorToolAggregate(tool.Definitions+tool.Arguments+tool.Results, v.Snapshot)
	calls := fmt.Sprintf("main %d calls", v.Rates.ToolCalls)
	if v.Rates.ToolCallsPartial {
		calls = fmt.Sprintf("main ≥%d calls", v.Rates.ToolCalls)
	}
	if terminalCellWidth("Tools in "+amount+" "+calls) > width {
		summary = append(summary, tokenMonitorPair("Tools in", amount, width))
	} else {
		summary = append(summary, tokenMonitorPair("Tools in "+amount, calls, width))
	}
	summary = append(summary, activityHintStyle.Render("Σ session+tasks · tools ⊂ input"))
	source := ""
	switch {
	case v.Err != "":
		source = "Source error · " + tokenMonitorText(v.Err)
	case v.Loading:
		source = "Loading token ledger…"
	case len(v.Snapshot.Issues) > 0:
		source = fmt.Sprintf("%s %d: %s", copyWorkspace, len(v.Snapshot.Issues), tokenMonitorIssue(v.Snapshot.Issues[0].Kind))
	}
	if source != "" {
		summary = append(summary, activityHintStyle.Render(source))
	}
	chartHeight := maxInt(1, height-len(lines)-len(summary))
	lines = append(lines, tokenMonitorReceivedGraph(v.Rates.Samples, width, chartHeight)...)
	lines = append(lines, summary...)
	return fitStyledRect(strings.Join(lines, "\n"), width, height)
}

// tokenHistoryEmpty 报告令牌历史完全无数据（无在途流、无统计、无加载/错误
// 信号）。此时空态只渲染单行「暂无数据」，不再铺开 — 骨架。Samples 是 60 槽
// 环形缓冲，空闲时非空但全零，须按值判断而非长度。
func tokenHistoryEmpty(v tokenMonitorView) bool {
	if v.Err != "" ||
		v.Loading ||
		v.Snapshot.Summary.Attempts > 0 ||
		v.Snapshot.ToolTotal != (tokentracer.LedgerTool{}) ||
		v.Rates.RequestID != "" ||
		v.Rates.ToolCalls > 0 ||
		len(v.Snapshot.Issues) > 0 {
		return false
	}
	for _, sample := range v.Rates.Samples {
		if tokenMonitorRate(sample) > 0 {
			return false
		}
	}
	return true
}

func renderTokenMonitor(v tokenMonitorView, width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	scope := "SESSION + TASKS"
	switch {
	case v.Err != "":
		scope = "Unavailable · " + tokenMonitorText(v.Err)
	case v.Loading:
		scope = "Loading token ledger…"
	case len(v.Snapshot.Issues) > 0:
		scope += fmt.Sprintf(" · Workspace %d: %s", len(v.Snapshot.Issues), tokenMonitorIssue(v.Snapshot.Issues[0].Kind))
	case !v.Snapshot.GeneratedAt.IsZero():
		scope += " · " + v.Snapshot.GeneratedAt.Local().Format("15:04:05")
	}
	heading := activityHintStyle.Render(tokenMonitorText(scope))
	if v.Detail && (width < 80 || height < 23) {
		lines := append([]string{heading, wizardTitleStyle.Render("REQUEST DETAIL")}, tokenMonitorRequests(v, width, height-2)...)
		return fitStyledRect(strings.Join(lines, "\n"), width, height)
	}
	if width >= 80 && height >= 19 {
		topHeight := maxInt(6, minInt(11, (height-1)/3))
		middleHeight := 9
		bottomHeight := height - 1 - topHeight - middleHeight
		left := maxInt(40, width*3/5)
		right := width - left - 1
		top := tokenMonitorJoin(
			tokenMonitorBox("01 FLOW · main session", tokenMonitorFlow(v, left-2, topHeight-2), left, topHeight),
			tokenMonitorBox("02 CONTEXT · main", tokenMonitorContext(v, right-2, topHeight-2), right, topHeight),
		)
		middleLeft := (width - 1) / 2
		middleRight := width - middleLeft - 1
		middle := tokenMonitorJoin(
			tokenMonitorBox("03 TOKEN LEDGER", tokenMonitorLedger(v, middleLeft-2), middleLeft, middleHeight),
			tokenMonitorBox("04 TOOLS · input exposure ≈", tokenMonitorTools(v, middleRight-2, middleHeight-2), middleRight, middleHeight),
		)
		bottom := tokenMonitorBox("05 REQUESTS", tokenMonitorRequests(v, width-2, bottomHeight-2), width, bottomHeight)
		return fitStyledRect(strings.Join([]string{heading, top, middle, bottom}, "\n"), width, height)
	}
	lines := []string{heading}
	add := func(title string, body []string) {
		titleWidth := terminalCellWidth(title) + 3
		line := contextFreeStyle.Render("─ ") + wizardTitleStyle.Render(title) + contextFreeStyle.Render(" "+strings.Repeat("─", maxInt(0, width-titleWidth)))
		lines = append(lines, line)
		lines = append(lines, body...)
	}
	flow := tokenMonitorFlow(v, width, 4)
	if height < 24 {
		flow = flow[:2]
	}
	add("FLOW · received ≈", flow)
	add("CONTEXT · main", tokenMonitorContext(v, width, 3))
	add("TOKEN LEDGER", tokenMonitorLedger(v, width))
	toolsHeight := minInt(5, maxInt(2, height-len(lines)-3))
	add("TOOLS · input exposure ≈", tokenMonitorTools(v, width, toolsHeight))
	if remaining := height - len(lines); remaining >= 2 {
		add("REQUESTS", tokenMonitorRequests(v, width, remaining-1))
	}
	return fitStyledRect(strings.Join(lines, "\n"), width, height)
}

func tokenMonitorFlow(v tokenMonitorView, width, height int) []string {
	if height <= 0 {
		return nil
	}
	state := "idle"
	if v.Rates.ToolActive {
		state = "tools"
	} else if v.Rates.Active {
		state = "receiving"
	}
	lines := tokenMonitorTurnLines(v.Rates, width)
	chartHeight := maxInt(1, height-4)
	lines = append(lines, tokenMonitorReceivedGraph(v.Rates.Samples, width, chartHeight)...)
	firstText := "—"
	if v.Rates.FirstTextKnown {
		firstText = tokenMonitorDuration(v.Rates.FirstText)
	}
	lines = append(lines, tokenMonitorPair("Req first text "+firstText, state, width))
	peak := 0.0
	for _, sample := range v.Rates.Samples {
		peak = math.Max(peak, tokenMonitorRate(sample))
	}
	caption := fmt.Sprintf("60s · peak ≈%.0f · text+reason+tools received", peak)
	if width < 38 {
		caption = "60s · text+reason+tools received"
	}
	lines = append(lines, activityHintStyle.Render(caption))
	return lines[:minInt(height, len(lines))]
}

func tokenMonitorTurnLines(r tokenRateView, width int) []string {
	rate, output, elapsed := "—", "—", "—"
	if r.TurnObserved {
		prefix := ""
		if r.TurnEstimated {
			prefix = "≈"
		}
		rate = prefix + fmt.Sprintf("%.1f", tokenMonitorRate(r.TurnAverage))
		output = prefix + formatCompactTokenCount(int(tokenMonitorRate(r.TurnTokens)))
	}
	if r.TurnActive || r.TurnObserved {
		elapsed = tokenMonitorDuration(r.TurnElapsed)
	}
	return []string{contextUsedStyle.Render("Turn avg " + rate + " tok/s"), tokenMonitorPair("Σout "+output, elapsed+" incl tools", width)}
}

func tokenMonitorContext(v tokenMonitorView, width, height int) []string {
	limit := "—"
	if v.Context.LimitTokens > 0 {
		limit = formatCompactTokenCount(v.Context.LimitTokens)
	}
	amount := "— / " + limit
	ratio := "—"
	known := contextCapacityKnown(v.Context)
	if known {
		amount = formatCompactTokenCount(v.Context.UsedTokens) + " / " + formatCompactTokenCount(v.Context.LimitTokens)
		ratio = fmt.Sprintf("%.1f%%", float64(v.Context.UsedTokens)*100/float64(v.Context.LimitTokens))
	}
	lines := []string{tokenMonitorPair(amount, ratio, width)}
	if known {
		filled := clampInt(int(math.Round(float64(width)*float64(v.Context.UsedTokens)/float64(v.Context.LimitTokens))), 0, width)
		lines = append(lines, contextUsedStyle.Render(strings.Repeat("▰", filled))+contextFreeStyle.Render(strings.Repeat("▱", maxInt(0, width-filled))))
	} else {
		lines = append(lines, contextFreeStyle.Render(strings.Repeat("·", maxInt(0, width))))
	}
	lines = append(lines, activityHintStyle.Render("Latest input + output"))
	if height >= 4 {
		lines = append(lines, activityHintStyle.Render("Capacity, not task completion"))
	}
	if height >= 5 {
		lines = append(lines, "")
	}
	if height >= 6 {
		lines = append(lines, activityHintStyle.Render("Main context excludes task totals"))
	}
	return lines
}

func tokenMonitorLedger(v tokenMonitorView, width int) []string {
	s := v.Snapshot.Summary
	total := s.Attempts
	metric := func(label string, value, known int, style lipgloss.Style) string {
		return tokenMonitorPair(label, style.Render(tokenMonitorKnown(value, known, total)), width)
	}
	lines := []string{
		metric("Input", s.Input, s.InputKnown, bodyStyle),
		metric("Output", s.Output, s.OutputKnown, bodyStyle),
		metric("Cache read", s.CacheRead, s.CacheKnown, bodyStyle),
	}
	cacheHit := "—"
	if total > 0 && s.InputKnown == total && s.CacheKnown == total && s.Input > 0 {
		cacheHit = fmt.Sprintf("%.1f%%", float64(s.CacheRead)*100/float64(s.Input))
	}
	lines = append(lines, tokenMonitorPair("Cache hit", bodyStyle.Render(cacheHit), width))
	lines = append(lines, metric("Cache write", s.CacheCreation, v.Snapshot.CacheCreationKnown, bodyStyle))
	lines = append(lines, metric("Reasoning", s.Reasoning, v.Snapshot.ReasoningKnown, inputCommandTokenStyle))
	subsets := "cache ⊂ input · reasoning ⊂ output"
	if width < terminalCellWidth(subsets) {
		subsets = "cache ⊂ in · reasoning ⊂ out"
	}
	lines = append(lines, activityHintStyle.Render(subsets))
	return lines
}

func tokenMonitorTools(v tokenMonitorView, width, height int) []string {
	if height <= 0 {
		return nil
	}
	count := fmt.Sprint(v.Rates.ToolCalls)
	if v.Rates.ToolCallsPartial {
		count = "≥" + count
	}
	callCount := fmt.Sprintf("Main turn: %s calls", count)
	if v.Rates.ToolDuration > 0 {
		duration := tokenMonitorDuration(v.Rates.ToolDuration)
		if v.Rates.ToolCallsPartial {
			duration = "≥" + duration
		}
		callCount += " · Σwait " + duration
	}
	lines := []string{callCount}
	if height == 1 {
		return lines
	}
	if v.Snapshot.ToolsKnown == 0 && v.Snapshot.ToolTotal.Occurrences == 0 {
		lines = append(lines, activityHintStyle.Render("Tool exposure unavailable"))
	} else {
		total := v.Snapshot.ToolTotal
		if height >= 5 {
			lines = append(lines, tokenMonitorPair("Defs "+tokenMonitorToolAggregate(total.Definitions, v.Snapshot), "Args "+tokenMonitorToolAggregate(total.Arguments, v.Snapshot), width))
		}
		lines = append(lines, tokenMonitorPair("Result replay", tokenMonitorToolAggregate(total.Results, v.Snapshot), width))
		available := maxInt(0, height-len(lines))
		if height >= 6 {
			available--
			lines = append(lines, activityHintStyle.Render(tokenMonitorPair("Tool", "result replay", width)))
			available--
		}
		for i := 0; i < minInt(available, len(v.Snapshot.Tools)); i++ {
			tool := v.Snapshot.Tools[i]
			lines = append(lines, tokenMonitorPair(labelToolStyle.Render(tokenMonitorText(tool.Name)), tokenMonitorToolAmount(tool.Results, tool), width))
		}
	}
	if len(lines) < height {
		hint := "Includes history; already in input"
		if v.Rates.ToolCallsPartial {
			hint = "History in input; partial tool timing"
		}
		lines = append(lines, activityHintStyle.Render(hint))
	}
	return lines
}

func tokenMonitorToolAggregate(value int, snapshot tokentracer.MonitorSnapshot) string {
	tool := snapshot.ToolTotal
	if snapshot.ToolsKnown == 0 && tool.Occurrences == 0 || tool.Occurrences > 0 && tool.Unknown == tool.Occurrences {
		return "—"
	}
	text := "≈" + formatCompactTokenCount(value)
	if snapshot.ToolsKnown < snapshot.Summary.Attempts {
		return text + fmt.Sprintf(" partial %d/%d", snapshot.ToolsKnown, snapshot.Summary.Attempts)
	}
	if tool.Unknown > 0 {
		text += " partial"
	}
	return text
}

func tokenMonitorIssue(kind string) string {
	if kind == "atom_retention_limit" {
		return "tool detail limit"
	}
	return strings.ReplaceAll(tokenMonitorText(kind), "_", " ")
}

func tokenMonitorToolAmount(value int, tool tokentracer.LedgerTool) string {
	if tool.Occurrences > 0 && tool.Unknown == tool.Occurrences {
		return "—"
	}
	text := "≈" + formatCompactTokenCount(value)
	if tool.Unknown > 0 {
		text += " partial"
	}
	return text
}

func tokenMonitorRequests(v tokenMonitorView, width, height int) []string {
	if height <= 0 {
		return nil
	}
	if len(v.Snapshot.Requests) == 0 {
		return []string{activityHintStyle.Render("No recorded requests")}
	}
	selected := clampInt(v.Selected, 0, len(v.Snapshot.Requests)-1)
	if v.Detail {
		r := v.Snapshot.Requests[selected]
		scope := "main session"
		if r.TaskID != "" {
			scope = "task · " + tokenMonitorText(r.TaskID)
		} else if r.SessionID != "" && r.SessionID != v.Snapshot.SessionID {
			scope = "session · " + tokenMonitorText(r.SessionID)
		}
		if width < 70 {
			return []string{
				tokenMonitorPair(tokenMonitorText(r.ID), tokenMonitorText(r.Status), width),
				tokenMonitorText(r.Model),
				scope,
				tokenMonitorPair("Input", tokenMonitorKnown(r.Summary.Input, r.Summary.InputKnown, r.Summary.Attempts), width),
				tokenMonitorPair("Output", tokenMonitorKnown(r.Summary.Output, r.Summary.OutputKnown, r.Summary.Attempts), width),
				tokenMonitorPair("Cache read", tokenMonitorKnown(r.Summary.CacheRead, r.Summary.CacheKnown, r.Summary.Attempts), width),
				activityHintStyle.Render("Session " + tokenMonitorText(r.SessionID)),
			}
		}
		lines := []string{tokenMonitorPair(tokenMonitorText(r.ID), tokenMonitorText(r.Status), width), tokenMonitorPair(tokenMonitorText(r.Model), scope, width)}
		if height >= 3 {
			lines = append(lines, fmt.Sprintf("Input %s · Output %s · Cache %s", tokenMonitorKnown(r.Summary.Input, r.Summary.InputKnown, r.Summary.Attempts), tokenMonitorKnown(r.Summary.Output, r.Summary.OutputKnown, r.Summary.Attempts), tokenMonitorKnown(r.Summary.CacheRead, r.Summary.CacheKnown, r.Summary.Attempts)))
		}
		if height >= 4 {
			lines = append(lines, activityHintStyle.Render("Session "+tokenMonitorText(r.SessionID)))
		}
		return lines
	}
	if height == 1 {
		r := v.Snapshot.Requests[selected]
		return []string{tokenMonitorPair("› "+tokenMonitorText(r.Model), tokenMonitorText(r.Status), width)}
	}
	lines := []string{activityHintStyle.Render(tokenMonitorPair("  time     model / task", "input / output", width))}
	available := height - 1
	start := maxInt(0, selected-available+1)
	for i := start; i < minInt(len(v.Snapshot.Requests), start+available); i++ {
		r := v.Snapshot.Requests[i]
		marker := "  "
		if i == selected {
			marker = "› "
		}
		scope := ""
		if r.TaskID != "" {
			scope = " · " + tokenMonitorText(r.TaskID)
		}
		amount := tokenMonitorKnown(r.Summary.Input, r.Summary.InputKnown, r.Summary.Attempts) + " / " + tokenMonitorKnown(r.Summary.Output, r.Summary.OutputKnown, r.Summary.Attempts)
		line := tokenMonitorPair(marker+r.StartedAt.Local().Format("15:04:05")+" "+tokenMonitorText(r.Model)+scope, amount, width)
		if i == selected {
			line = activityFocusStyle.Render(line)
		}
		lines = append(lines, line)
	}
	return lines
}

func tokenMonitorKnown(value, known, total int) string {
	if known <= 0 {
		return "—"
	}
	text := formatCompactTokenCount(value)
	if known < total {
		text += fmt.Sprintf(" partial %d/%d", known, total)
	}
	return text
}

func tokenMonitorRate(value float64) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
		return 0
	}
	return value
}

func tokenMonitorDuration(value time.Duration) string {
	if value < 0 {
		return "—"
	}
	if value < time.Second {
		return fmt.Sprintf("%dms", value.Milliseconds())
	}
	return fmt.Sprintf("%.1fs", value.Seconds())
}

func tokenMonitorText(value string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, ansi.Strip(value))
}

func tokenMonitorPair(left, right string, width int) string {
	right = truncateStyledCellLine(right, maxInt(0, width))
	rightWidth := terminalCellWidth(right)
	leftWidth := maxInt(0, width-rightWidth-1)
	left = truncateStyledCellLine(left, leftWidth)
	return left + strings.Repeat(" ", maxInt(0, width-terminalCellWidth(left)-rightWidth)) + right
}

func tokenMonitorBox(title string, body []string, width, height int) string {
	if width < 2 || height < 2 {
		return fitStyledRect(strings.Join(body, "\n"), width, height)
	}
	inner := width - 2
	title = truncateStyledCellLine(" "+title+" ", inner)
	top := contextFreeStyle.Render("╭") + wizardTitleStyle.Render(title) + contextFreeStyle.Render(strings.Repeat("─", maxInt(0, inner-terminalCellWidth(title)))+"╮")
	content := strings.Split(fitStyledRect(strings.Join(body, "\n"), inner, height-2), "\n")
	lines := []string{top}
	for _, line := range content {
		lines = append(lines, contextFreeStyle.Render("│")+line+contextFreeStyle.Render("│"))
	}
	lines = append(lines, contextFreeStyle.Render("╰"+strings.Repeat("─", inner)+"╯"))
	return fitStyledRect(strings.Join(lines, "\n"), width, height)
}

func tokenMonitorJoin(left, right string) string {
	return lipgloss.JoinHorizontal(lipgloss.Top, left, " ", right)
}

func tokenMonitorReceivedGraph(samples []float64, width, height int) []string {
	lines := tokenMonitorGraph(samples, width, height)
	for _, sample := range samples {
		if tokenMonitorRate(sample) > 0 {
			return lines
		}
	}
	if len(lines) > 0 {
		lines[len(lines)/2] = activityHintStyle.Render(fitStyledCellLine("暂无流片段", width))
	}
	return lines
}

func tokenMonitorGraph(samples []float64, width, height int) []string {
	if width <= 0 || height <= 0 {
		return nil
	}
	cells := make([]float64, width*2)
	peak := 1.0
	count := minInt(60, len(samples))
	for _, sample := range samples[len(samples)-count:] {
		peak = math.Max(peak, tokenMonitorRate(sample))
	}
	for x := range cells {
		// Each column keeps its position in the same sixty-second window.
		index := int(float64(x)*60/float64(len(cells))) - (60 - count)
		if index >= 0 && index < count {
			cells[x] = tokenMonitorRate(samples[len(samples)-count+index])
		}
	}
	lines := make([]string, height)
	dots := [4][2]rune{{0x1, 0x8}, {0x2, 0x10}, {0x4, 0x20}, {0x40, 0x80}}
	for y := 0; y < height; y++ {
		var row strings.Builder
		for x := 0; x < width; x++ {
			mask := rune(0)
			for subX := 0; subX < 2; subX++ {
				level := int(math.Round(cells[x*2+subX] / peak * float64(height*4)))
				for subY := 0; subY < 4; subY++ {
					if level >= (height-y)*4-subY {
						mask |= dots[subY][subX]
					}
				}
			}
			if mask == 0 {
				row.WriteRune(' ')
			} else {
				row.WriteRune(0x2800 + mask)
			}
		}
		lines[y] = contextUsedStyle.Render(row.String())
	}
	return lines
}
