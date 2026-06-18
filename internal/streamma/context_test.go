package streamma

import (
	"reflect"
	"testing"
)

func TestTranscriptAppendOnlyPromptPrefixAndOrder(t *testing.T) {
	transcript := NewTranscript(AgentSpec{ID: "b", SystemPrompt: "system"}, "fixed problem")
	transcript.AppendInbound("a", StepPacket{Content: StepContent{Text: "from a"}})
	transcript.AppendOwn(StepPacket{Content: StepContent{Text: "own b"}})

	firstPrompt := BuildPrompt(transcript)
	transcript.AppendInbound("c", StepPacket{Content: StepContent{Text: "from c"}})
	secondPrompt := BuildPrompt(transcript)

	if len(secondPrompt) <= len(firstPrompt) {
		t.Fatalf("second prompt did not grow: first=%d second=%d", len(firstPrompt), len(secondPrompt))
	}
	if !reflect.DeepEqual(firstPrompt, secondPrompt[:len(firstPrompt)]) {
		t.Fatalf("previous prompt is not a stable prefix\nfirst=%#v\nsecond=%#v", firstPrompt, secondPrompt)
	}
	if secondPrompt[2].Content != "Inbound step from a:\nfrom a" ||
		secondPrompt[3].Content != "Own step:\nown b" ||
		secondPrompt[4].Content != "Inbound step from c:\nfrom c" {
		t.Fatalf("prompt order mismatch: %#v", secondPrompt)
	}
}
