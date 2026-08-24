package bubble

import (
	"testing"
)

func TestApplyModelWizardSelectionEmitsModelCardBlock(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.modelWizard = &modelWizard{
		step:            modelWizardConfirm,
		providerOptions: []modelProviderOption{{id: "gateway", label: "Gateway"}},
		selectedIndex:   0,
		selectedModel:   0,
		modelOptions:    []string{"model-x"},
	}
	next := model.applyModelWizardSelection()
	if next.modelWizard != nil {
		t.Fatalf("wizard should close after apply")
	}
	last := next.transcript[len(next.transcript)-1]
	if !isModelCardBlock(last.body) {
		t.Fatalf("apply entry body not a model card block: %q", last.body)
	}
	info, ok := parseModelCardBlock(last.body)
	if !ok || info.Model != "model-x" {
		t.Fatalf("apply card info = %#v, ok=%v", info, ok)
	}
}
