package bubble

import (
	"context"
	"errors"
	"paw/internal/message"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

type clipboardImageTestRunner struct {
	fakeRunner
	richInputs []message.Message
	richErr    error
}

func (r *clipboardImageTestRunner) RunRichTurn(_ context.Context, input message.Message) (message.Message, error) {
	rich := input
	rich.Parts = append([]message.ContentPart(nil), input.Parts...)
	r.richInputs = append(r.richInputs, rich)
	return message.Message{Role: message.RoleAssistant, Content: "ok"}, r.richErr
}

func setTestClipboardImage(t *testing.T, image message.ImagePart) {
	t.Helper()
	readClipboardImageFn = func(context.Context) (message.ImagePart, error) {
		return image, nil
	}
	readClipboardTextFn = func() (string, error) { return "unused", nil }
}

func TestClipboardImageSubmitsRichMessageAndRestoresFailedDraft(t *testing.T) {
	oldImageReader := readClipboardImageFn
	oldTextReader := readClipboardTextFn
	t.Cleanup(func() {
		readClipboardImageFn = oldImageReader
		readClipboardTextFn = oldTextReader
	})
	runner := &clipboardImageTestRunner{richErr: errors.New("image endpoint rejected the request")}
	setTestClipboardImage(t, message.ImagePart{MIMEType: "image/png", Data: []byte("png")})
	model := newTestModel(runner)
	model.input.SetValue("describe ")
	next, cmd := model.Update(tea.KeyMsg{Type: tea.KeyCtrlV})
	next, _ = next.(appModel).Update(cmd())
	model = next.(appModel)

	next, submitCmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = next.(appModel)
	if submitCmd == nil {
		t.Fatal("image submit command is nil")
	}
	msg := submitCmd()
	next, _ = model.Update(msg)
	model = next.(appModel)
	if len(runner.richInputs) != 1 || len(runner.richInputs[0].Parts) != 2 ||
		runner.richInputs[0].Parts[1].Image == nil {
		t.Fatalf("rich inputs = %#v", runner.richInputs)
	}
	if got := model.input.Value(); got != "describe [Image 1]" {
		t.Fatalf("failed draft = %q, want restored image chip", got)
	}
}

func TestClipboardImageQueuePreservesRichParts(t *testing.T) {
	oldImageReader := readClipboardImageFn
	oldTextReader := readClipboardTextFn
	t.Cleanup(func() {
		readClipboardImageFn = oldImageReader
		readClipboardTextFn = oldTextReader
	})
	runner := &clipboardImageTestRunner{}
	setTestClipboardImage(t, message.ImagePart{MIMEType: "image/png", Data: []byte("png")})
	model := newTestModel(runner)
	model.input.SetValue("first")
	next, firstCmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = next.(appModel)
	if firstCmd == nil {
		t.Fatal("first submit command is nil")
	}
	next, pasteCmd := model.Update(tea.KeyMsg{Type: tea.KeyCtrlV})
	model = next.(appModel)
	next, _ = model.Update(pasteCmd())
	model = next.(appModel)
	next, queueCmd := model.Update(tea.KeyMsg{Type: tea.KeyTab})
	model = next.(appModel)
	if queueCmd != nil || model.chatQueue.Len() != 1 {
		t.Fatalf("queue command/length = %v/%d", queueCmd, model.chatQueue.Len())
	}

	next, queuedTurnCmd := model.Update(firstCmd())
	model = next.(appModel)
	if queuedTurnCmd == nil {
		t.Fatal("queued turn command is nil")
	}
	_ = queuedTurnCmd()
	if len(runner.richInputs) != 2 || len(runner.richInputs[1].Parts) != 1 ||
		runner.richInputs[1].Parts[0].Type != message.ContentPartImage {
		t.Fatalf("queued rich inputs = %#v", runner.richInputs)
	}
}

func TestClipboardImageChipDeletesAsOneToken(t *testing.T) {
	oldImageReader := readClipboardImageFn
	oldTextReader := readClipboardTextFn
	t.Cleanup(func() {
		readClipboardImageFn = oldImageReader
		readClipboardTextFn = oldTextReader
	})
	setTestClipboardImage(t, message.ImagePart{MIMEType: "image/png", Data: []byte("png")})
	model := newTestModel(&fakeRunner{})
	next, cmd := model.Update(tea.KeyMsg{Type: tea.KeyCtrlV})
	next, _ = next.(appModel).Update(cmd())
	model = next.(appModel)
	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	model = next.(appModel)
	if model.input.Value() != "" || len(model.inputTokens) != 0 {
		t.Fatalf("after chip deletion value=%q tokens=%#v", model.input.Value(), model.inputTokens)
	}
}

func TestRestoredImageHistoryRebuildsChipAndAttachmentReference(t *testing.T) {
	entries := transcriptEntriesFromMessage(message.Message{
		Role:    message.RoleUser,
		Content: "inspect [Image 1]",
		Parts: []message.ContentPart{
			{Type: message.ContentPartText, Text: "inspect "},
			{Type: message.ContentPartImage, Image: &message.ImagePart{MIMEType: "image/png", Attachment: "attachments/hash.png"}},
		},
	}, time.Now(), "")
	if len(entries) != 1 || len(entries[0].inputTokens) != 1 {
		t.Fatalf("restored entries = %#v", entries)
	}
	model := newTestModel(&fakeRunner{})
	next, _ := model.Update(sessionRestoredMsg{sessionID: "restored", entries: entries})
	model = next.(appModel)
	model.running = false
	next, _ = model.handleHistoryNavigation(-1)
	model = next.(appModel)
	if model.input.Value() != "inspect [Image 1]" || len(model.inputTokens) != 1 ||
		model.inputTokens[0].Image == nil || model.inputTokens[0].Image.Attachment != "attachments/hash.png" {
		t.Fatalf("restored image draft value=%q tokens=%#v", model.input.Value(), model.inputTokens)
	}
}

func TestClipboardImageInsertsAtomicImageTokenAndRichParts(t *testing.T) {
	oldImageReader := readClipboardImageFn
	oldTextReader := readClipboardTextFn
	t.Cleanup(func() {
		readClipboardImageFn = oldImageReader
		readClipboardTextFn = oldTextReader
	})
	readClipboardImageFn = func(context.Context) (message.ImagePart, error) {
		return message.ImagePart{MIMEType: "image/png", Data: []byte("png")}, nil
	}
	readClipboardTextFn = func() (string, error) { return "unused", nil }

	model := newTestModel(&fakeRunner{})
	model.input.SetValue("before ")
	model.input.CursorEnd()
	next, cmd := model.Update(tea.KeyMsg{Type: tea.KeyCtrlV})
	if cmd == nil {
		t.Fatal("Ctrl+V returned no clipboard command")
	}
	next, _ = next.(appModel).Update(cmd())
	model = next.(appModel)

	if got := model.input.Value(); got != "before [Image 1]" {
		t.Fatalf("input = %q", got)
	}
	if len(model.inputTokens) != 1 || model.inputTokens[0].Kind != inputTokenImage {
		t.Fatalf("input tokens = %#v", model.inputTokens)
	}
	if got := string(model.inputTokens[0].Image.Data); got != "png" {
		t.Fatalf("image data = %q", got)
	}
	rich := messageFromInputDraft(model.currentInputDraft())
	if len(rich.Parts) != 2 || rich.Parts[1].Type != message.ContentPartImage {
		t.Fatalf("rich parts = %#v", rich.Parts)
	}
}

func TestClipboardImagesKeepOrderAndInsertAtCursor(t *testing.T) {
	oldImageReader := readClipboardImageFn
	oldTextReader := readClipboardTextFn
	t.Cleanup(func() {
		readClipboardImageFn = oldImageReader
		readClipboardTextFn = oldTextReader
	})
	index := 0
	readClipboardImageFn = func(context.Context) (message.ImagePart, error) {
		index++
		return message.ImagePart{MIMEType: "image/png", Data: []byte{byte(index)}}, nil
	}
	readClipboardTextFn = func() (string, error) { return "unused", nil }

	model := newTestModel(&fakeRunner{})
	model.input.SetValue("before after")
	setTextareaAbsoluteCursor(&model.input, len([]rune("before ")))
	for i := 0; i < 2; i++ {
		next, cmd := model.Update(tea.KeyMsg{Type: tea.KeyCtrlV})
		model = next.(appModel)
		next, _ = model.Update(cmd())
		model = next.(appModel)
	}
	if got := model.input.Value(); got != "before [Image 1][Image 2]after" {
		t.Fatalf("multiple image input = %q", got)
	}
	rich := messageFromInputDraft(model.currentInputDraft())
	if len(rich.Parts) != 4 || rich.Parts[0].Text != "before " ||
		rich.Parts[1].Image == nil || string(rich.Parts[1].Image.Data) != string([]byte{1}) ||
		rich.Parts[2].Image == nil || string(rich.Parts[2].Image.Data) != string([]byte{2}) ||
		rich.Parts[3].Text != "after" {
		t.Fatalf("multiple image parts = %#v", rich.Parts)
	}
}

func TestClipboardTextFallsBackToExistingPasteBehavior(t *testing.T) {
	oldImageReader := readClipboardImageFn
	oldTextReader := readClipboardTextFn
	t.Cleanup(func() {
		readClipboardImageFn = oldImageReader
		readClipboardTextFn = oldTextReader
	})
	readClipboardImageFn = func(context.Context) (message.ImagePart, error) {
		return message.ImagePart{}, errClipboardNoImage
	}
	readClipboardTextFn = func() (string, error) { return "pasted text", nil }

	model := newTestModel(&fakeRunner{})
	model.input.SetValue("prefix ")
	next, cmd := model.Update(tea.KeyMsg{Type: tea.KeyCtrlV})
	next, _ = next.(appModel).Update(cmd())
	if got := next.(appModel).input.Value(); got != "prefix pasted text" {
		t.Fatalf("input = %q", got)
	}
}

func TestClipboardReadErrorDoesNotMutateInput(t *testing.T) {
	oldImageReader := readClipboardImageFn
	oldTextReader := readClipboardTextFn
	t.Cleanup(func() {
		readClipboardImageFn = oldImageReader
		readClipboardTextFn = oldTextReader
	})
	readClipboardImageFn = func(context.Context) (message.ImagePart, error) {
		return message.ImagePart{}, errors.New("image unavailable")
	}
	readClipboardTextFn = func() (string, error) { return "", errors.New("text unavailable") }

	model := newTestModel(&fakeRunner{})
	model.input.SetValue("keep")
	next, cmd := model.Update(tea.KeyMsg{Type: tea.KeyCtrlV})
	next, _ = next.(appModel).Update(cmd())
	if got := next.(appModel).input.Value(); got != "keep" {
		t.Fatalf("input = %q", got)
	}
}
