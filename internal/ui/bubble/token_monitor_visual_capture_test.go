package bubble

import (
	"fmt"
	"html"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"github.com/rivo/uniseg"
	modelpkg "paw/internal/capability/model"
	"paw/internal/runtime/loop"
	"paw/internal/todo"
	"paw/internal/tokentracer"
)

func TestCaptureTokenMonitorVisualFixtures(t *testing.T) {
	dir := os.Getenv("PAW_TOKEN_MONITOR_VISUAL_DIR")
	if strings.TrimSpace(dir) == "" {
		t.Skip("set PAW_TOKEN_MONITOR_VISUAL_DIR to capture terminal fixtures")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	previousProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(previousProfile) })
	for _, fixture := range []struct {
		name          string
		width, height int
		detail        bool
	}{
		{"token-monitor-fullscreen", 120, 34, false},
		{"token-monitor-sidebar", 42, 27, false},
		{"token-monitor-compact", 80, 24, false},
		{"token-monitor-detail", 120, 34, true},
	} {
		v := tokenMonitorFixture()
		v.Detail = fixture.detail
		v.Selected = 1
		view := renderTokenMonitor(v, fixture.width, fixture.height)
		tokenMonitorWriteVisual(t, dir, fixture.name, fmt.Sprintf("Paw Tokens · %d × %d cells · deterministic fixture", fixture.width, fixture.height), view)
	}
	for _, fixture := range []struct {
		name                     string
		width, height, paneWidth int
		tab                      activityTab
		partial                  bool
		overflow                 bool
		empty                    bool
	}{
		{"activity-monitor-tasks-32", 120, 40, 32, activityTabTasks, false, false, false},
		{"activity-monitor-todo-52", 140, 40, 52, activityTabTodo, false, false, false},
		{"activity-monitor-tasks-24", 100, 24, 32, activityTabTasks, false, false, false},
		{"activity-monitor-todo-36", 120, 36, 40, activityTabTodo, false, false, false},
		{"activity-monitor-partial", 120, 40, 32, activityTabTodo, true, false, false},
		{"activity-monitor-overflow", 120, 40, 32, activityTabTasks, false, true, false},
		{"activity-monitor-no-stream", 100, 24, 32, activityTabTasks, false, false, true},
	} {
		v := tokenMonitorFixture()
		if fixture.partial {
			v.Context.TotalKnown, v.Context.InputKnown, v.Context.OutputKnown = false, true, false
			v.Context.UsedTokens = 18400
			v.Snapshot.Summary.InputKnown, v.Snapshot.Summary.OutputKnown, v.Snapshot.Summary.CacheKnown = 3, 2, 1
			v.Snapshot.ToolTotal.Unknown = 1
			v.Snapshot.ToolsKnown = 3
		}
		if fixture.overflow {
			v.Context = loop.ContextStats{Known: true, TotalKnown: true, InputKnown: true, OutputKnown: true, CacheKnown: true,
				UsedTokens: 370796, InputTokens: 370619, OutputTokens: 177, CacheTokens: 369792, LimitTokens: 256000}
			v.Snapshot.Summary = tokentracer.RequestSummary{Requests: 5, Attempts: 5, Input: 1206498, Output: 1855, CacheRead: 881280, Reasoning: 1057, InputKnown: 5, OutputKnown: 5, CacheKnown: 5}
			v.Snapshot.ToolTotal = tokentracer.LedgerTool{Occurrences: 453, Definitions: 73335, Arguments: 507, Results: 822848}
			v.Snapshot.Issues = []tokentracer.LedgerIssue{{Kind: "atom_retention_limit"}}
		}
		if fixture.empty {
			v.Rates.Samples = nil
		}
		model := newActivityVisualModel(fixture.width, fixture.height)
		model.runner = &fakeRunner{stats: v.Context}
		model.activity.widthColumns = fixture.paneWidth
		model.openActivity(fixture.tab)
		model.activity.tab = fixture.tab
		model.input.Focus()
		model.hasCurrentTodo = true
		model.currentTodo = todo.Snapshot{UpdatedAt: time.Now(), Items: []todo.Item{
			{Content: "核对现有 Token 数据来源", Status: todo.StatusCompleted},
			{Content: "恢复输入框上沿容量条", Status: todo.StatusCompleted},
			{Content: "固定下半边 Token 历史曲线", Status: todo.StatusInProgress},
			{Content: "上半自动跟随任务和 Todo 更新", Status: todo.StatusPending},
			{Content: "验证输入始终可用", Status: todo.StatusPending},
		}}
		model.replaceTranscript([]transcriptEntry{
			{kind: entryUser, title: "you", body: "上半边放任务，下半边固定显示 Token 历史。"},
			{kind: entryAssistant, title: "assistant", body: "上半自动跟随最近更新，输入框一直可用。"},
		})
		model.tokenMonitor.snapshot = v.Snapshot
		now := time.Now()
		live := &model.tokenMonitor.live
		live.beginTurn(now.Add(-80 * time.Second))
		if !fixture.empty {
			live.start("previous-request", now.Add(-80*time.Second))
			live.usage(modelpkg.Usage{RequestID: "previous-request", CompletionTokens: 1000})
			live.requestEnd("previous-request", now.Add(-40*time.Second))
		}
		live.start(v.Rates.RequestID, now.Add(-20*time.Second))
		if !fixture.empty {
			live.firstText = now.Add(-18 * time.Second)
		}
		for i, value := range v.Rates.Samples {
			second := now.Unix() - int64(len(v.Rates.Samples)-i-1)
			live.buckets[int((second%60+60)%60)] = tokenRateBucket{second: second, tokens: value}
			live.total += value
		}
		live.tools = make(map[string]tokenToolTiming)
		for i := 0; i < 6; i++ {
			live.tools[fmt.Sprint(i)] = tokenToolTiming{started: now.Add(-5 * time.Second), ended: now.Add(-4300 * time.Millisecond)}
		}
		model.relayout()
		model.refreshViewport()
		view := model.View()
		if !strings.Contains(ansi.Strip(view), "TOKEN HISTORY") {
			t.Fatalf("%s did not render the fixed token history", fixture.name)
		}
		tokenMonitorWriteVisual(t, dir, fixture.name, fmt.Sprintf("Paw appModel.View · %d × %d cells", fixture.width, fixture.height), view)
	}
}

func tokenMonitorWriteVisual(t *testing.T, dir, name, title, view string) {
	t.Helper()
	if strings.TrimSpace(ansi.Strip(view)) == "" {
		t.Fatalf("%s is empty", name)
	}
	page := `<!doctype html><html><head><meta charset="utf-8"><style>
body{margin:0;background:#101216;color:#d8dde7;font:15px/1.45 ui-monospace,SFMono-Regular,Menlo,Monaco,Consolas,monospace}
main{display:inline-block;padding:28px 32px}h1{font:500 16px/1.4 -apple-system,BlinkMacSystemFont,sans-serif;color:#aab4c4;margin:0 0 16px}
pre{display:inline-block;margin:0;white-space:pre;font:inherit}
.terminal-cell{display:inline-block;width:1ch;height:1.45em;line-height:1.45em;vertical-align:top;overflow:hidden}
</style></head><body><main><h1>` + html.EscapeString(title+" · reference ANSI palette") + `</h1><pre>` + tokenMonitorVisualANSIToHTML(view) + `</pre></main></body></html>`
	if err := os.WriteFile(filepath.Join(dir, name+".html"), []byte(page), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".ansi"), []byte(view), 0o644); err != nil {
		t.Fatal(err)
	}
}

func tokenMonitorVisualANSIToHTML(input string) string {
	var output strings.Builder
	style := activityVisualANSIStyle{}
	writeText := func(text string) {
		current := style
		if current.reverse {
			if current.foreground == "" {
				current.foreground = "#d8dde7"
			}
			if current.background == "" {
				current.background = "#101216"
			}
		}
		graphemes := uniseg.NewGraphemes(text)
		for graphemes.Next() {
			grapheme := graphemes.Str()
			if grapheme == "\n" {
				output.WriteByte('\n')
				continue
			}
			width := terminalCellWidth(grapheme)
			output.WriteString(fmt.Sprintf(`<span class="terminal-cell" style="width:%dch">`, width))
			output.WriteString(activityVisualStyledHTML(grapheme, current))
			output.WriteString(`</span>`)
		}
	}
	for len(input) > 0 {
		index := strings.Index(input, "\x1b[")
		if index < 0 {
			writeText(input)
			break
		}
		writeText(input[:index])
		input = input[index+2:]
		end := strings.IndexByte(input, 'm')
		if end < 0 {
			writeText(input)
			break
		}
		activityVisualApplySGR(&style, tokenMonitorVisualColorSGR(input[:end]))
		input = input[end+1:]
	}
	return output.String()
}

func tokenMonitorVisualColorSGR(raw string) string {
	parts := strings.Split(raw, ";")
	var result []string
	for i := 0; i < len(parts); i++ {
		n, err := strconv.Atoi(parts[i])
		if err != nil {
			result = append(result, parts[i])
			continue
		}
		foreground, index := true, -1
		switch {
		case n >= 30 && n <= 37:
			index = n - 30
		case n >= 90 && n <= 97:
			index = n - 90 + 8
		case n >= 40 && n <= 47:
			foreground, index = false, n-40
		case n >= 100 && n <= 107:
			foreground, index = false, n-100+8
		case (n == 38 || n == 48) && i+2 < len(parts) && parts[i+1] == "5":
			foreground = n == 38
			index, _ = strconv.Atoi(parts[i+2])
			i += 2
		case (n == 38 || n == 48) && i+4 < len(parts) && parts[i+1] == "2":
			result = append(result, parts[i:i+5]...)
			i += 4
			continue
		}
		if index < 0 {
			result = append(result, parts[i])
			continue
		}
		color := tokenMonitorVisualIndexedColor(index)
		code := "38"
		if !foreground {
			code = "48"
		}
		result = append(result, code, "2", fmt.Sprint(color>>16), fmt.Sprint((color>>8)&255), fmt.Sprint(color&255))
	}
	return strings.Join(result, ";")
}

func tokenMonitorVisualIndexedColor(index int) uint32 {
	colors := [16]uint32{0x000000, 0xcd0000, 0x00cd00, 0xcdcd00, 0x0000ee, 0xcd00cd, 0x00cdcd, 0xe5e5e5, 0x7f7f7f, 0xff0000, 0x00ff00, 0xffff00, 0x5c5cff, 0xff00ff, 0x00ffff, 0xffffff}
	if index < 16 {
		return colors[clampInt(index, 0, 15)]
	}
	if index >= 232 {
		v := uint32(8 + clampInt(index-232, 0, 23)*10)
		return v<<16 | v<<8 | v
	}
	v := index - 16
	levels := [6]uint32{0, 95, 135, 175, 215, 255}
	return levels[v/36]<<16 | levels[(v/6)%6]<<8 | levels[v%6]
}

func TestTokenMonitorVisualPreservesANSIColorsAndCellWidths(t *testing.T) {
	got := tokenMonitorVisualANSIToHTML("\x1b[1;36m⣶▰中\x1b[0m\x1b[7mT\x1b[0m")
	for _, want := range []string{"color:#00cdcd", "width:1ch", "width:2ch", "background:#d8dde7", "color:#101216"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %s", want, got)
		}
	}
	if got := tokenMonitorVisualColorSGR("38;2;32;36;48"); got != "38;2;32;36;48" {
		t.Errorf("truecolor changed to %q", got)
	}
}
