package bubble

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	configv2 "paw/internal/config"
)

func TestModelWizardSearchFiltersModelsAndPreservesCatalogSelection(t *testing.T) {
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
		modelSelections: []configv2.CatalogSelection{
			{ID: "gateway/alpha", ProviderKey: "gateway", ModelName: "alpha"},
			{ID: "gateway/beta", ProviderKey: "gateway", ModelName: "beta"},
			{ID: "gateway/gamma", ProviderKey: "gateway", ModelName: "gamma"},
		},
	}

	initialRendered := ansi.Strip(model.renderModelStep())
	if !strings.Contains(initialRendered, "搜索模型…") {
		t.Fatalf("empty model search placeholder missing:\n%s", initialRendered)
	}

	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("beta")})
	model = next.(appModel)
	if got := model.modelWizard.search; got != "beta" {
		t.Fatalf("model search = %q, want beta", got)
	}
	if got := model.modelWizard.selectedModelName(); got != "beta" {
		t.Fatalf("selected model = %q, want beta", got)
	}
	selection, ok := model.modelWizard.selectedCatalogSelection()
	if !ok || selection.ID != "gateway/beta" {
		t.Fatalf("selected catalog model = %#v, ok=%v; want gateway/beta", selection, ok)
	}
	rendered := model.renderModelStep()
	if !strings.Contains(ansi.Strip(rendered), "beta") {
		t.Fatalf("model search result missing:\n%s", rendered)
	}
	for _, unwanted := range []string{"alpha", "gamma"} {
		if strings.Contains(rendered, unwanted) {
			t.Fatalf("filtered model list contains %q:\n%s", unwanted, rendered)
		}
	}

	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = next.(appModel)
	if model.modelWizard != nil {
		t.Fatalf("filtered Enter should apply and close wizard: %#v", model.modelWizard)
	}
	last := model.transcript[len(model.transcript)-1]
	info, ok := parseModelCardBlock(last.body)
	if !ok || info.Model != "beta" {
		t.Fatalf("applied model card = %#v, ok=%v; want beta", info, ok)
	}
}

func TestModelWizardSearchEscAndBackspaceFollowConfigCenterSemantics(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.modelWizard = &modelWizard{
		step:            modelWizardModel,
		providerOptions: []modelProviderOption{{id: "gateway", label: "Gateway"}},
		modelOptions:    []string{"alpha", "beta"},
	}

	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("be")})
	model = next.(appModel)
	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	model = next.(appModel)
	if model.modelWizard.search != "b" {
		t.Fatalf("search after backspace = %q, want b", model.modelWizard.search)
	}

	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = next.(appModel)
	if model.modelWizard == nil || model.modelWizard.search != "" {
		t.Fatalf("Esc should clear search without closing wizard: %#v", model.modelWizard)
	}

	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = next.(appModel)
	if model.modelWizard != nil {
		t.Fatalf("second Esc should close wizard: %#v", model.modelWizard)
	}
}

func TestModelWizardSearchPrintableKeysNeverHijackedAsShortcuts(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.modelWizard = &modelWizard{
		step:            modelWizardModel,
		providerOptions: []modelProviderOption{{id: "gateway", label: "Gateway"}},
		modelOptions:    []string{"beta", "jamba", "kimi"},
	}

	// 逐个按键输入 "bjk"：b/j/k 都必须进入搜索内容，而不是触发返回或移动。
	for _, r := range []rune("bjk") {
		next, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		model = next.(appModel)
		if model.modelWizard == nil || model.modelWizard.step != modelWizardModel {
			t.Fatalf("typing %q left the model step: %#v", r, model.modelWizard)
		}
	}
	if got := model.modelWizard.search; got != "bjk" {
		t.Fatalf("search = %q, want bjk", got)
	}

	// 清空搜索后，Backspace 才承担返回服务商的职责。
	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = next.(appModel)
	if model.modelWizard == nil || model.modelWizard.search != "" {
		t.Fatalf("Esc should clear search: %#v", model.modelWizard)
	}
	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	model = next.(appModel)
	if model.modelWizard == nil || model.modelWizard.step != modelWizardProvider {
		t.Fatalf("Backspace with empty search should return to provider step: %#v", model.modelWizard)
	}
}

func TestModelWizardSearchWithNoMatchCannotConfirmHiddenModel(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.modelWizard = &modelWizard{
		step:            modelWizardModel,
		providerOptions: []modelProviderOption{{id: "gateway", label: "Gateway"}},
		modelOptions:    []string{"alpha", "beta"},
	}

	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("missing")})
	model = next.(appModel)
	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = next.(appModel)
	if model.modelWizard == nil || model.modelWizard.step != modelWizardModel {
		t.Fatalf("Enter with no matching model changed wizard state: %#v", model.modelWizard)
	}
}
