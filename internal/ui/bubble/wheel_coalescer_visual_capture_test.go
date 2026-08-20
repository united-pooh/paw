package bubble

import (
	"fmt"
	"html"
	"os"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestCaptureWheelCoalescerVisualFixture(t *testing.T) {
	outputPath := strings.TrimSpace(os.Getenv("PAW_WHEEL_VISUAL_FIXTURE"))
	if outputPath == "" {
		t.Skip("set PAW_WHEEL_VISUAL_FIXTURE to capture the visual fixture")
	}
	model := wheelFilterTestModel()
	model.width = 100
	model.height = 32
	model.relayout()
	entries := make([]transcriptEntry, 0, 48)
	for index := range 16 {
		entries = append(entries,
			transcriptEntry{kind: entryUser, title: "you", body: fmt.Sprintf("Inspect transcript segment %02d", index)},
			transcriptEntry{kind: entryAssistant, title: "assistant", body: "The cached history remains complete while wheel input is coalesced."},
		)
		if index%4 == 1 {
			entries = append(entries, transcriptEntry{
				kind:             entryTool,
				title:            "tool",
				toolName:         "Read",
				toolTarget:       fmt.Sprintf("fixture-%02d.go", index),
				toolStatus:       "ok",
				toolGroupPending: true,
			})
		}
	}
	model.replaceTranscript(entries)
	model.refreshViewport()
	model.viewport.SetYOffset(model.viewport.YOffset / 2)
	model.newMessageNoticeCount = 12
	before := ansi.Strip(model.View())

	filter := newProgramEventFilter()
	var current tea.Model = model
	forward := wheelFilterMouse(tea.MouseButtonWheelUp)
	reverse := wheelFilterMouse(tea.MouseButtonWheelDown)
	accepted := 0
	for range 3_001 {
		filtered := filter.Filter(current, forward)
		if filtered == nil {
			continue
		}
		next, _ := current.Update(filtered)
		current = next
		accepted++
	}
	filtered := filter.Filter(current, reverse)
	if filtered == nil {
		t.Fatal("reverse wheel was filtered")
	}
	next, _ := current.Update(filtered)
	current = next
	accepted++
	updated := current.(appModel)
	if accepted != 3_002 || updated.viewport.YOffset != 3 {
		t.Fatalf("visual fixture accepted=%d offset=%d, want 3002/3", accepted, updated.viewport.YOffset)
	}
	next, _ = updated.Update(assistantDeltaMsg("Canceled output remains complete after wheel input.\n"))
	updated = next.(appModel)
	updated.cancelModelWork()
	refreshes := updated.transcriptRefreshCount
	next, doneCmd := updated.Update(doneMsg{})
	updated = next.(appModel)
	if doneCmd == nil || updated.transcriptRefreshCount != refreshes {
		t.Fatalf("canceled visual tail was not deferred: cmd=%v refreshes=%d->%d", doneCmd != nil, refreshes, updated.transcriptRefreshCount)
	}
	next, _ = updated.Update(transcriptWheelBatchMsg{lines: -3, x: 10, y: 3})
	updated = next.(appModel)
	idleOffset := updated.viewport.YOffset
	next, _ = updated.Update(transcriptDeferredRefreshMsg{generation: updated.transcriptRefreshDeferredGeneration})
	updated = next.(appModel)
	if updated.transcriptRefreshCount != refreshes+1 || updated.viewport.YOffset != idleOffset {
		t.Fatalf("idle refresh count/offset = %d/%d, want %d/%d", updated.transcriptRefreshCount, updated.viewport.YOffset, refreshes+1, idleOffset)
	}
	after := ansi.Strip(updated.View())
	if strings.TrimSpace(before) == "" || strings.TrimSpace(after) == "" {
		t.Fatal("visual fixture render is empty")
	}

	page := `<!doctype html><html><head><meta charset="utf-8"><style>
body{margin:0;background:#0b1020;color:#e5e7eb;font:15px/1.45 -apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif}
main{width:1360px;margin:0 auto;padding:28px 32px 36px;box-sizing:border-box}h1{font-size:23px;margin:0 0 6px}.subtitle{color:#94a3b8;margin:0 0 18px}
.metrics{display:flex;gap:10px;margin:0 0 18px}.metric{background:#172033;border:1px solid #334155;border-radius:9px;padding:8px 13px}.metric b{color:#7dd3fc}
.grid{display:grid;grid-template-columns:1fr 1fr;gap:18px}section{min-width:0;background:#111827;border:1px solid #334155;border-radius:12px;padding:15px;box-shadow:0 12px 28px #0005}
h2{font-size:15px;margin:0 0 10px;color:#fbbf24}pre{height:650px;overflow:hidden;white-space:pre;margin:0;color:#f8fafc;font:12px/1.28 ui-monospace,SFMono-Regular,Menlo,monospace}
</style></head><body><main><h1>Post-cancel transcript scrolling</h1><p class="subtitle">Non-private fixture rendered through the real Bubble app model before wheel reversal and after a canceled-tail idle refresh.</p>
<div class="metrics"><div class="metric"><b>3,002</b> wheel events</div><div class="metric"><b>3,002</b> applied scroll batches</div><div class="metric"><b>1</b> idle final refresh</div><div class="metric"><b>preserved</b> manual YOffset</div></div>
<div class="grid"><section><h2>Before burst</h2><pre>` + html.EscapeString(before) + `</pre></section><section><h2>After cancel + wheel idle</h2><pre>` + html.EscapeString(after) + `</pre></section></div></main></body></html>`
	if err := os.WriteFile(outputPath, []byte(page), 0o644); err != nil {
		t.Fatal(err)
	}
}
