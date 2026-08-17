package bubble

import (
	"html"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

func TestCaptureReasoningSummaryVisualFixture(t *testing.T) {
	started := time.Unix(500, 0)
	finished := started.Add(2 * time.Second)
	entries := []transcriptEntry{
		{kind: entryUser, title: "you", body: "Inspect the current Responses reasoning implementation", createdAt: started},
		{
			kind:                entryReasoning,
			title:               "reasoning",
			body:                "The provider returned a concise reasoning summary.\nIt remains collapsed until the user expands it.",
			reasoningStartedAt:  &started,
			reasoningFinishedAt: &finished,
			createdAt:           started,
		},
		{kind: entryAssistant, title: "assistant", body: "The summary is persisted and available after resume.", createdAt: finished},
	}
	collapsed := ansi.Strip(renderTranscriptAt(entries, 72, false, finished))
	expanded := ansi.Strip(renderTranscriptAt(entries, 72, true, finished))
	page := `<!doctype html><html><head><meta charset="utf-8"><style>
body{margin:0;background:#111827;color:#e5e7eb;font:16px/1.55 -apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif}
main{width:980px;margin:0 auto;padding:42px 48px 56px;box-sizing:border-box}
h1{font-size:24px;margin:0 0 8px}p{color:#9ca3af;margin:0 0 28px}.grid{display:grid;grid-template-columns:1fr 1fr;gap:22px}
section{background:#1f2937;border:1px solid #374151;border-radius:14px;padding:22px;box-shadow:0 14px 32px #0005}h2{font-size:16px;margin:0 0 16px;color:#fbbf24}pre{white-space:pre-wrap;margin:0;color:#f3f4f6;font:15px/1.65 ui-monospace,SFMono-Regular,Menlo,monospace}
</style></head><body><main><h1>GPT Responses reasoning summary</h1><p>Collapsed disclosure and Ctrl+O-expanded summary rendered from the real Bubble transcript renderer.</p><div class="grid"><section><h2>Collapsed</h2><pre>` + html.EscapeString(collapsed) + `</pre></section><section><h2>Expanded</h2><pre>` + html.EscapeString(expanded) + `</pre></section></div></main></body></html>`
	if err := os.WriteFile("/tmp/paw-reasoning-summary.html", []byte(page), 0o644); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(collapsed) == "" || strings.TrimSpace(expanded) == "" {
		t.Fatal("fixture render is empty")
	}
}
