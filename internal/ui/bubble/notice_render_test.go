package bubble

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"paw/internal/storage/session"
)

const insufficientBalanceError = `模型接口返回异常状态 402: {"error":{"message":"Insufficient Balance (request_id: 6db10f18-42fd-4d9a-997f-223b378325c4)","type":"insufficient_balance_error","param":null,"code":"invalid_request_error"}}`

func TestCompactionNoticeRendersAsSingleMutedLine(t *testing.T) {
	got := ansi.Strip(renderEntry(transcriptEntry{kind: entrySystem, title: "context-compaction", body: "上下文已用 50%，保留可缓存前缀，需要时再清理"}, 80))
	want := transcriptEntryGutter + glyphNotice + " 上下文已用 50%，保留可缓存前缀，需要时再清理"
	if strings.TrimRight(got, " ") != want {
		t.Fatalf("notice = %q, want %q", got, want)
	}
}

func TestModelAPIErrorCollapsesRawJSON(t *testing.T) {
	got := ansi.Strip(renderEntry(transcriptEntry{kind: entryError, title: "error", body: insufficientBalanceError}, 120))
	lines := strings.Split(got, "\n")
	if len(lines) != 2 {
		t.Fatalf("collapsed error = %q, want headline and disclosure only", got)
	}
	for i, want := range []string{
		glyphFailure + " 模型接口错误 402 · Insufficient Balance",
		glyphFolded + " " + copyErrorRawResponse,
	} {
		if !strings.Contains(lines[i], want) {
			t.Fatalf("line %d = %q, want %q", i, lines[i], want)
		}
	}
	for _, hidden := range []string{`{"error"`, "insufficient_balance_error", "request_id"} {
		if strings.Contains(got, hidden) {
			t.Fatalf("collapsed error leaked %q: %q", hidden, got)
		}
	}

	expanded := ansi.Strip(renderEntry(transcriptEntry{kind: entryError, title: "error", body: insufficientBalanceError, errorDetailsExpanded: true}, 100))
	if !strings.Contains(expanded, glyphExpanded+" "+copyErrorRawResponse) || !strings.Contains(expanded, `"type": "insufficient_balance_error"`) {
		t.Fatalf("expanded error = %q, want indented raw JSON", expanded)
	}
}

func TestPlainErrorKeepsTextWithFailureGlyph(t *testing.T) {
	got := ansi.Strip(renderEntry(transcriptEntry{kind: entryError, title: "error", body: "dial tcp: connection refused"}, 80))
	if strings.TrimRight(got, " ") != transcriptEntryGutter+glyphFailure+" dial tcp: connection refused" {
		t.Fatalf("plain error = %q", got)
	}
	titled := ansi.Strip(renderEntry(transcriptEntry{kind: entryError, title: "turn metadata", body: "disk full"}, 80))
	if !strings.Contains(titled, glyphFailure+" turn metadata: disk full") {
		t.Fatalf("titled error = %q, want title kept as prefix", titled)
	}
}

func TestClickTogglesModelAPIErrorDetails(t *testing.T) {
	m := newTestModel(&fakeRunner{})
	m.transcript = []transcriptEntry{{kind: entryError, title: "error", body: insufficientBalanceError}}
	m.relayout()
	m.refreshViewport()
	location := m.transcriptEntryLocationsAt()[0]
	if m.toggleErrorDetailsAtRow(location.startRow + location.height + 5) {
		t.Fatal("click outside the error must not toggle")
	}
	if !m.toggleErrorDetailsAtRow(location.startRow + 1) {
		t.Fatal("click on the disclosure row must toggle raw response")
	}
	if !m.transcript[0].errorDetailsExpanded {
		t.Fatal("raw response must be expanded after click")
	}
}

func TestRecoveryErrorReadsInChinese(t *testing.T) {
	for _, tc := range []struct {
		state session.RecoveryState
		want  string
	}{
		{session.RecoveryState{Error: "context canceled"}, "上一轮失败：context canceled"},
		{session.RecoveryState{Error: "context canceled", Interrupted: true}, "上一轮中断：context canceled"},
		{session.RecoveryState{}, "上一轮失败：未完成就结束了"},
		{session.RecoveryState{Error: "previous turn ended before completion"}, "上一轮失败：未完成就结束了"},
		{session.RecoveryState{Error: "recovered turn ended before completion"}, "上一轮失败：恢复执行后仍未完成就结束了"},
	} {
		body := recoveryDisplayText(&tc.state)
		got := ansi.Strip(renderEntry(transcriptEntry{kind: entryError, title: "recovery", body: body}, 80))
		if strings.TrimRight(got, " ") != transcriptEntryGutter+glyphFailure+" "+tc.want {
			t.Errorf("recovery %+v = %q, want %q", tc.state, got, tc.want)
		}
	}
}
