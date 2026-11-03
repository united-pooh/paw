package bubble

import (
	"fmt"
	"math"
	"slices"
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
	hasSamples := tokenMonitorHasSamples(v.Rates.Samples)
	empty := tokenHistoryEmpty(v, hasSamples)
	caption := "近60秒"
	if !empty && !hasSamples {
		caption = "暂无流片段"
	}
	title := tokenMonitorPair(wizardTitleStyle.Render(copyTokenHistory), contextFreeStyle.Render(caption), width)
	lines := []string{title}
	var summary []string
	if empty {
		if height > 2 {
			lines = append(lines, contextFreeStyle.Render(copyEmptyData))
		}
	} else {
		lines = append(lines, tokenHistoryRates(v.Rates, width)...)
		summary = tokenHistorySummary(v, width)
	}
	lines = lines[:minInt(len(lines), height-1)]
	chartHeight := maxInt(1, height-len(lines)-len(summary))
	lines = append(lines, tokenMonitorGraph(v.Rates.Samples, width, chartHeight)...)
	lines = append(lines, summary...)
	return fitStyledRect(strings.Join(lines, "\n"), width, height)
}

func tokenHistoryEmpty(v tokenMonitorView, hasSamples bool) bool {
	return !hasSamples &&
		v.Err == "" &&
		!v.Loading &&
		v.Snapshot.Summary.Attempts == 0 &&
		v.Snapshot.ToolTotal == (tokentracer.LedgerTool{}) &&
		v.Rates.RequestID == "" &&
		v.Rates.ToolCalls == 0 &&
		len(v.Snapshot.Issues) == 0
}

func tokenHistoryRates(r tokenRateView, width int) []string {
	average, output, elapsed, current := "—", "—", "—", "—"
	if r.TurnObserved {
		prefix := ""
		if r.TurnEstimated {
			prefix = "约"
		}
		average = prefix + fmt.Sprintf("%.1f/秒", tokenMonitorRate(r.TurnAverage))
		output = prefix + tokenHistoryCount(int(tokenMonitorRate(r.TurnTokens)))
	}
	if r.TurnActive || r.TurnObserved {
		elapsed = tokenMonitorDuration(r.TurnElapsed)
		if number, ok := strings.CutSuffix(elapsed, "ms"); ok {
			elapsed = number + "毫秒"
		} else if number, ok := strings.CutSuffix(elapsed, "s"); ok {
			elapsed = number + "秒"
		}
	}
	if r.RequestID != "" && r.Observed {
		current = fmt.Sprintf("约%.1f/秒", tokenMonitorRate(r.Current))
	}
	left, right := "均速 "+average, "实时 "+current
	if terminalCellWidth(left+" "+right) > width {
		left = strings.TrimSuffix(left, "/秒")
	}
	return []string{
		tokenMonitorPair(contextUsedStyle.Render(left), contextFreeStyle.Render(right), width),
		bodyStyle.Render(tokenMonitorPair(copyMainTurn+"输出 "+output, "耗时 "+elapsed, width)),
	}
}

func tokenHistorySummary(v tokenMonitorView, width int) []string {
	s := v.Snapshot.Summary
	input := tokenHistoryKnown(s.Input, s.InputKnown, s.Attempts)
	cache := tokenHistoryKnown(s.CacheRead, s.CacheKnown, s.Attempts)
	track := contextFreeStyle.Render(strings.Repeat(tokenTrackTexture, width))
	if s.Attempts > 0 && s.InputKnown == s.Attempts && s.CacheKnown == s.Attempts && s.Input > 0 && s.CacheRead >= 0 && s.CacheRead <= s.Input {
		ratio := float64(s.CacheRead) / float64(s.Input)
		cache += fmt.Sprintf(" %.1f%%", ratio*100)
		filled := int(math.Round(ratio * float64(width)))
		if width > 1 && s.CacheRead > 0 && s.CacheRead < s.Input {
			filled = clampInt(filled, 1, width-1)
		}
		track = contextCacheStyle.Render(strings.Repeat("█", filled)) +
			contextUsedStyle.Render(strings.Repeat(tokenTrackTexture, width-filled))
	}
	output := tokenHistoryKnown(s.Output, s.OutputKnown, s.Attempts)
	tools := "—"
	tool := v.Snapshot.ToolTotal
	if tokenMonitorToolTotalKnown(v.Snapshot) {
		tools = "约" + tokenHistoryCount(tool.Definitions+tool.Arguments+tool.Results)
		if v.Snapshot.ToolsKnown < s.Attempts {
			tools += fmt.Sprintf(" 部分 %d/%d", v.Snapshot.ToolsKnown, s.Attempts)
		} else if tool.Unknown > 0 {
			tools += " 部分"
		}
	}
	calls := fmt.Sprintf("本轮 %d 次", v.Rates.ToolCalls)
	if v.Rates.ToolCallsPartial {
		calls = fmt.Sprintf("本轮至少 %d 次", v.Rates.ToolCalls)
	}
	toolLine := "其中工具 " + tools
	if terminalCellWidth(toolLine+" "+calls) <= width {
		toolLine = tokenMonitorPair(toolLine, contextFreeStyle.Render(calls), width)
	}
	inputLabel := "累计输入（含子任务）"
	if terminalCellWidth(inputLabel+" "+input) > width {
		inputLabel = "累计输入"
	}
	summary := []string{
		bodyStyle.Render(tokenMonitorPair(inputLabel, input, width)),
		track,
		bodyStyle.Render(tokenMonitorPair("缓存命中", cache, width)),
		bodyStyle.Render(toolLine),
		bodyStyle.Render(tokenMonitorPair("累计输出", output, width)),
	}
	source := ""
	switch {
	case v.Err != "":
		source = "账本读取失败"
	case v.Loading:
		source = "正在读取账本…"
	case len(v.Snapshot.Issues) > 0:
		issue := "账本记录不完整"
		switch v.Snapshot.Issues[0].Kind {
		case "atom_retention_limit":
			issue = "工具明细受限"
		case "usage_inconsistent":
			issue = "账本用量不一致"
		}
		source = fmt.Sprintf("%s %d · %s", copyWorkspace, len(v.Snapshot.Issues), issue)
	}
	if source != "" {
		summary = append(summary, contextFreeStyle.Render(source))
	}
	return summary
}

func tokenHistoryKnown(value, known, total int) string {
	if known <= 0 {
		return "—"
	}
	text := tokenHistoryCount(value)
	if known < total {
		text += fmt.Sprintf(" 部分 %d/%d", known, total)
	}
	return text
}

func tokenHistoryCount(value int) string {
	switch {
	case value >= 100_000_000:
		return formatThreeDigitNumber(float64(value)/100_000_000) + "亿"
	case value >= 10_000:
		return formatThreeDigitNumber(float64(value)/10_000) + "万"
	default:
		return fmt.Sprint(maxInt(0, value))
	}
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
	lines = append(lines, tokenMonitorGraph(v.Rates.Samples, width, chartHeight)...)
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
	ratio, amount := contextReadout(v.Context, 1)
	known := contextCapacityKnown(v.Context)
	lines := []string{tokenMonitorPair(amount, ratio, width)}
	if known {
		filled := clampInt(int(math.Round(float64(width)*float64(v.Context.UsedTokens)/float64(v.Context.LimitTokens))), 0, width)
		lines = append(lines, contextUsedStyle.Render(strings.Repeat("▰", filled))+contextFreeStyle.Render(strings.Repeat("▱", maxInt(0, width-filled))))
	} else {
		lines = append(lines, contextFreeStyle.Render(strings.Repeat(tokenTrackTexture, maxInt(0, width))))
	}
	caption := "Latest input + output"
	if v.Context.Estimated {
		caption = "Estimated from active history"
	}
	lines = append(lines, activityHintStyle.Render(caption))
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
	if !tokenMonitorToolTotalKnown(snapshot) {
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

func tokenMonitorToolTotalKnown(snapshot tokentracer.MonitorSnapshot) bool {
	tool := snapshot.ToolTotal
	return (snapshot.ToolsKnown > 0 || tool.Occurrences > 0) && (tool.Occurrences == 0 || tool.Unknown != tool.Occurrences)
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

func tokenMonitorHasSamples(samples []float64) bool {
	return slices.ContainsFunc(samples[maxInt(0, len(samples)-60):], func(sample float64) bool {
		return tokenMonitorRate(sample) > 0
	})
}

func tokenMonitorGraph(samples []float64, width, height int) []string {
	if width <= 0 || height <= 0 {
		return nil
	}
	// A single terminal row contains the axis, including the continuous zero line.
	graphHeight := height
	if graphHeight%2 == 0 {
		graphHeight--
	}
	center := graphHeight / 2
	lines := make([]string, height)
	var window [60]float64
	samples = samples[maxInt(0, len(samples)-len(window)):]
	peak := 0.0
	for i, sample := range samples {
		rate := tokenMonitorRate(sample)
		window[len(window)-len(samples)+i] = rate
		peak = math.Max(peak, rate)
	}
	if peak == 0 {
		lines[center] = contextFreeStyle.Render(strings.Repeat("─", width))
		return lines
	}
	peak = math.Max(1, peak)
	halfDots := graphHeight * 2
	levels := make([]int, width*2)
	for x := range levels {
		start := x * len(window) / len(levels)
		end := maxInt(start+1, (x+1)*len(window)/len(levels))
		// Keep short bursts visible when several seconds share one dot column.
		if rate := slices.Max(window[start:end]); rate > 0 {
			levels[x] = maxInt(1, int(math.Round(rate/peak*float64(halfDots))))
		}
	}
	dots := [4][2]rune{{0x1, 0x8}, {0x2, 0x10}, {0x4, 0x20}, {0x40, 0x80}}
	for y := 0; y < graphHeight; y++ {
		var distances [4]int
		for subY := range distances {
			dot := y*4 + subY
			distances[subY] = maxInt(dot-halfDots, halfDots-1-dot)
		}
		var row strings.Builder
		for x := 0; x < width; x++ {
			left, right := levels[x*2], levels[x*2+1]
			if y == center && left == 0 && right == 0 {
				row.WriteRune('─')
				continue
			}
			mask := rune(0)
			for subX, level := range [...]int{maxInt(1, left), maxInt(1, right)} {
				for subY, distance := range distances {
					if distance < level {
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
		distance := maxInt(y-center, center-y)
		lines[y] = tokenMonitorWaveStyle(float64(distance) / float64(maxInt(1, center))).Render(row.String())
	}
	return lines
}

func tokenMonitorWaveStyle(distance float64) lipgloss.Style {
	cool := colorManager.Hex(colorContextUsed)
	hot := colorManager.Hex(colorStatusError)
	color := cool
	switch {
	case strings.HasPrefix(cool, "#") && strings.HasPrefix(hot, "#"):
		color = interpolateHexColor(cool, hot, distance)
	case distance >= 0.75:
		color = hot
	case distance >= 0.35:
		color = colorManager.Hex(colorStatusWarning)
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color(color))
}
