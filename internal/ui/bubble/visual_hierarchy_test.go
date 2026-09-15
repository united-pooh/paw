package bubble

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"paw/internal/capability/model"
	"paw/internal/runtime/loop"
)

func TestModelNoticeDisclosureClickAndKeyboard(t *testing.T) {
	m := newTestModel(&fakeRunner{})
	m.width, m.height = 100, 30
	m.relayout()
	m.addEntry(transcriptEntry{kind: entrySystem, body: formatModelSwitchBlock(model.Config{
		Model: "glm-5.3-flash", Provider: "openrouter", APIBaseURL: "https://example.test/v1",
	}, 0)})
	plain := ansi.Strip(m.renderTranscriptContent())
	if strings.Contains(plain, "https://example.test/v1") || strings.ContainsAny(plain, "╭╰│") {
		t.Fatalf("collapsed model notification leaks panel/details: %s", plain)
	}
	locations := m.transcriptEntryLocationsAt()
	if len(locations) != 1 || locations[0].height != 1 {
		t.Fatalf("notification must occupy one row: %#v", locations)
	}
	var handled bool
	m, handled, _ = m.performTranscriptClick(selectionPoint{row: locations[0].startRow, col: 3})
	if !handled || !strings.Contains(ansi.Strip(m.renderTranscriptContent()), "https://example.test/v1") {
		t.Fatal("click did not reveal complete model configuration")
	}
	m, handled, _ = m.performTranscriptClick(selectionPoint{row: locations[0].startRow, col: 3})
	if !handled || strings.Contains(ansi.Strip(m.renderTranscriptContent()), "https://example.test/v1") {
		t.Fatal("second click did not collapse configuration")
	}
	entry := transcriptEntry{kind: entrySystem, body: m.transcript[0].body}
	if !strings.Contains(ansi.Strip(renderEntryAt(entry, 100, m.animationNow(), true)), "https://example.test/v1") {
		t.Fatal("global detail view must reveal model configuration")
	}
	m.transcript[0] = entry
	m.invalidateTranscriptRender()
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	m = next.(appModel)
	if !strings.Contains(ansi.Strip(m.renderTranscriptContent()), "https://example.test/v1") {
		t.Fatal("Ctrl+O did not reveal model details through the input handler")
	}
}

func TestModelNoticeNarrowWidthsPreserveExpandedConfiguration(t *testing.T) {
	const url = "https://example.test/很长的地址/api/v1/chat/completions"
	body := formatModelSwitchBlock(model.Config{Model: "very-long/model-name", Provider: "provider", APIBaseURL: url}, 0)
	for _, width := range []int{20, 40, 60, 100} {
		collapsed := renderModelSwitchCard(body, width)
		if strings.Contains(collapsed, "\n") {
			t.Fatal("collapsed notice wraps")
		}
		assertRenderedLineWidthsAtMost(t, collapsed, width)
		expanded := renderModelSwitchNotice(body, width, true)
		assertRenderedLineWidthsAtMost(t, expanded, width)
		joined := strings.Join(strings.Fields(ansi.Strip(expanded)), "")
		if !strings.Contains(joined, url) {
			t.Fatalf("width %d lost address: %s", width, joined)
		}
	}
}

func TestContextOccupancyIsDistinctFromCacheProportion(t *testing.T) {
	m := newTestModel(&fakeRunner{stats: loop.ContextStats{UsedTokens: 68700, CacheTokens: 65265, LimitTokens: 256000}})
	full := ansi.Strip(m.renderBottomDockUsage())
	for _, want := range []string{"上下文 27%", "68.7k/256k", "缓存占比 95%"} {
		if !strings.Contains(full, want) {
			t.Fatalf("missing %q in %q", want, full)
		}
	}
	for _, width := range []int{1, 8, 16, 24, 40, 60, 100} {
		assertRenderedLineWidthsAtMost(t, m.renderBottomDockUsageWithin(width), width)
	}
	if got := ansi.Strip(m.renderBottomDockUsageWithin(14)); got != "上下文 27%" {
		t.Fatalf("narrow label is ambiguous: %q", got)
	}
}

func TestQuietTableRetainsValuesAndDeclaredAlignment(t *testing.T) {
	plain := ansi.Strip(renderMarkdownTable([]string{
		"| 项目 | 大小 |", "| :--- | ---: |", "| ~/.cache/uv | 10G |", "| ~/.npm | 3.7G |",
	}, 60))
	if strings.ContainsAny(plain, "┌┐└┘│├┼┤") || len(strings.Split(plain, "\n")) != 4 {
		t.Fatalf("table must have one header separator and no outer/row grid: %s", plain)
	}
	for _, want := range []string{"项目", "大小", "~/.cache/uv", "10G", "~/.npm", "3.7G"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("table lost %q", want)
		}
	}
	assertTableLinesFit(t, plain, 60)
}

func TestInputPromptRemainsVisibleAfterInteraction(t *testing.T) {
	m := newTestModel(&fakeRunner{})
	m.hasInteracted = true
	if !strings.Contains(ansi.Strip(m.renderInputContentWithHints(60, 1)), "›") {
		t.Fatal("empty input lost its visible entry point after a turn")
	}
	if got := ansi.Strip(m.renderDockStatusLine(60)); got != strings.Repeat("─", 60) {
		t.Fatalf("input separator should be a hairline: %q", got)
	}
}

func TestProseReadingWidthPreservesTextAndWideCode(t *testing.T) {
	text := strings.Repeat("中文阅读", 40)
	rendered := renderMarkdown(text, 140)
	for _, line := range strings.Split(rendered, "\n") {
		if ansi.StringWidth(line) > 104 {
			t.Fatalf("prose exceeds reading width: %d", ansi.StringWidth(line))
		}
	}
	if strings.ReplaceAll(strings.Join(strings.Fields(ansi.Strip(rendered)), ""), "\n", "") != text {
		t.Fatal("wrapping lost prose")
	}
	code := renderMarkdown("```text\n"+strings.Repeat("x", 115)+"\n```", 140)
	if !strings.Contains(ansi.Strip(code), strings.Repeat("x", 115)) {
		t.Fatal("reading width restricted code")
	}
}
