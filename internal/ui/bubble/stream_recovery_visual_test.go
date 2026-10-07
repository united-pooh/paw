package bubble

import (
	"fmt"
	"html"
	"os"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"paw/internal/ui"
)

func TestStreamRecoverySeparatesInterruptedThinkingInBubble(t *testing.T) {
	var panels strings.Builder
	for _, structured := range []bool{false, true} {
		name := "legacy thinking"
		if structured {
			name = "structured reasoning"
		}
		t.Run(name, func(t *testing.T) {
			model := newTestModel(&fakeRunner{})
			model.ready = true
			model.width = 86
			model.height = 24
			model.turnStartedAt = time.Unix(1000, 0)
			model.cursorFrameAt = model.turnStartedAt
			model.relayout()
			model.addEntry(transcriptEntry{kind: entryUser, title: "you", body: "继续调查已有任务，保留已完成工具结果。"})
			if !model.queryGuard.StartModel() {
				t.Fatal("start model failed")
			}
			output := New()
			output.sendMsg = func(msg tea.Msg) {
				next, _ := model.Update(msg)
				model = next.(appModel)
			}
			publish := func(text string) {
				if structured {
					if err := output.OnReasoningStart(0, false); err != nil {
						t.Fatal(err)
					}
					if err := output.OnReasoningDelta(0, text); err != nil {
						t.Fatal(err)
					}
				} else if err := output.OnThinkingDelta(text); err != nil {
					t.Fatal(err)
				}
				for model.thinkingStream.HasPendingCharacters() {
					model.releaseThinkingCharacters()
				}
				model.refreshViewport()
			}
			capture := func(label string) {
				model.refreshViewport()
				fmt.Fprintf(&panels, "<section><h2>%s · %s</h2><pre>%s</pre></section>", html.EscapeString(name), html.EscapeString(label), html.EscapeString(ansi.Strip(model.View())))
			}
			const oldThought = "正在分析已完成的工具结果，连接在此中断。\n"
			const newThought = "重新连接后，从已完成步骤继续调查。\n"
			publish(oldThought)
			capture("中断之前")
			model.cursorFrameAt = model.turnStartedAt.Add(90 * time.Second)
			if err := output.OnDone(); err != nil {
				t.Fatal(err)
			}
			if model.isGenerating || model.activeThinking >= 0 || model.activeReasoning >= 0 {
				t.Errorf("Done left an open stream: generating=%v thinking=%d reasoning=%d", model.isGenerating, model.activeThinking, model.activeReasoning)
			}
			if err := output.OnSystemMessage(ui.SystemEvent{Title: "stream-recovery", Body: "模型连接中断，正在从已完成步骤自动继续（1/10）。已完成工具结果已保留。"}); err != nil {
				t.Fatal(err)
			}
			capture("旧输出结束，等待续接")
			model.cursorFrameAt = model.turnStartedAt.Add(92 * time.Second)
			publish(newThought)
			var thoughts []transcriptEntry
			for _, entry := range model.transcript {
				if entry.kind == entryReasoning {
					thoughts = append(thoughts, entry)
				}
			}
			if len(thoughts) != 2 {
				t.Errorf("thought entries=%d, want separate interrupted and resumed entries: %#v", len(thoughts), thoughts)
			} else if thoughts[0].body != oldThought || thoughts[0].reasoningFinishedAt == nil || thoughts[1].body != newThought || thoughts[1].reasoningFinishedAt != nil {
				t.Errorf("interrupted/resumed lifecycle is mixed: %#v", thoughts)
			}
			next, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
			model = next.(appModel)
			capture("新思考独立显示（展开历史）")
		})
	}
	if path := strings.TrimSpace(os.Getenv("PAW_STREAM_RECOVERY_VISUAL_FIXTURE")); path != "" {
		page := `<!doctype html><html lang="zh"><meta charset="utf-8"><style>
body{margin:0;background:#11131b;color:#e5e7eb;font:15px/1.5 -apple-system,BlinkMacSystemFont,sans-serif}main{padding:28px;width:1790px;box-sizing:border-box}h1{font-size:24px;margin:0 0 8px}p{color:#b1b6c5;margin:0 0 20px}.grid{display:grid;grid-template-columns:repeat(3,1fr);gap:16px}section{padding:16px;background:#1a1d28;border:1px solid #41465b;border-radius:10px}h2{font-size:15px;color:#8bddff;margin:0 0 12px}pre{white-space:pre;margin:0;font:10px/1.6 Menlo,monospace}
</style><main><h1>断流自动续接：旧思考结束，新思考独立显示</h1><p>使用真实 Bubble UI adapter → Update → View；固定 86 × 24 终端。上排为 Thinking，下排为 structured reasoning。内容为测试样例。</p><div class="grid">` + panels.String() + `</div></main></html>`
		if err := os.WriteFile(path, []byte(page), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDoneFinalizesStructuredReasoningOnce(t *testing.T) {
	for _, mode := range []string{"interrupted", "canceled", "completed", "redacted"} {
		t.Run(mode, func(t *testing.T) {
			model := newTestModel(&fakeRunner{})
			model.ready = true
			model.width, model.height = 80, 20
			model.cursorFrameAt = time.Unix(1000, 0)
			model.relayout()
			next, _ := model.Update(assistantPartMsg{lifecycle: "start", blockIndex: 0, partType: "reasoning", redacted: mode == "redacted"})
			model = next.(appModel)
			if mode == "completed" {
				next, _ = model.Update(assistantPartMsg{lifecycle: "end", blockIndex: 0, partType: "reasoning"})
				model = next.(appModel)
			}
			model.modelCancelRequested = mode == "canceled"
			priorFinished := model.transcript[0].reasoningFinishedAt
			model.cursorFrameAt = model.cursorFrameAt.Add(2 * time.Second)
			next, _ = model.Update(doneMsg{})
			model = next.(appModel)
			finished := model.transcript[0].reasoningFinishedAt
			if model.activeReasoning != -1 || finished == nil {
				t.Fatalf("Done left unfinished reasoning: active=%d finished=%v", model.activeReasoning, finished)
			}
			if priorFinished != nil && !finished.Equal(*priorFinished) {
				t.Fatal("Done changed the completion time of an already finished reasoning block")
			}
			model.cursorFrameAt = model.cursorFrameAt.Add(2 * time.Second)
			next, _ = model.Update(doneMsg{})
			model = next.(appModel)
			if !model.transcript[0].reasoningFinishedAt.Equal(*finished) {
				t.Fatal("repeated Done changed reasoning completion time")
			}
		})
	}
}
