package bubble

import (
	"html"
	"os"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestCaptureModelWizardSearchVisualFixture(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 80
	model.height = 24
	model.relayout()
	model.modelWizard = &modelWizard{
		step: modelWizardModel,
		providerOptions: []modelProviderOption{{
			id:    "gateway",
			label: "Gateway",
		}},
		modelOptions: []string{"alpha", "beta", "gamma"},
	}
	empty := ansi.Strip(model.renderModelStep())

	model.modelWizard.search = "beta"
	model.modelWizard.resetModelSelectionForSearch()
	filtered := ansi.Strip(model.renderModelStep())
	if strings.TrimSpace(empty) == "" || strings.TrimSpace(filtered) == "" {
		t.Fatal("model wizard visual fixture is empty")
	}

	page := `<!doctype html><html><head><meta charset="utf-8"><style>
body{margin:0;background:#111827;color:#e5e7eb;font:16px/1.55 -apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif}
main{width:980px;margin:0 auto;padding:42px 48px 56px;box-sizing:border-box}
h1{font-size:24px;margin:0 0 8px}p{color:#9ca3af;margin:0 0 28px}.grid{display:grid;grid-template-columns:1fr 1fr;gap:22px}
section{background:#1f2937;border:1px solid #374151;border-radius:14px;padding:22px;box-shadow:0 14px 32px #0005}h2{font-size:16px;margin:0 0 16px;color:#fbbf24}pre{white-space:pre-wrap;margin:0;color:#f3f4f6;font:15px/1.65 ui-monospace,SFMono-Regular,Menlo,monospace}
</style></head><body><main><h1>/model 模型搜索</h1><p>当前 provider 下的模型列表，以及输入搜索词后的过滤结果。</p><div class="grid"><section><h2>Empty search</h2><pre>` + html.EscapeString(empty) + `</pre></section><section><h2>Filtered: beta</h2><pre>` + html.EscapeString(filtered) + `</pre></section></div></main></body></html>`
	if err := os.WriteFile("/tmp/paw-model-wizard-search.html", []byte(page), 0o644); err != nil {
		t.Fatal(err)
	}
}
