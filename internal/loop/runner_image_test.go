package loop

import (
	"context"
	"paw/internal/message"
	"paw/internal/model"
	"testing"
)

type attachmentTestStore struct {
	fakeStore
	saved []byte
}

func (s *attachmentTestStore) SaveAttachment(_ context.Context, _ string, data []byte) (string, error) {
	s.saved = append([]byte(nil), data...)
	return "attachments/test.png", nil
}

func (s *attachmentTestStore) ReadAttachment(_ context.Context, _ string) (string, []byte, error) {
	return "image/png", append([]byte(nil), s.saved...), nil
}

func TestRunRichTurnPersistsAndMaterializesImageParts(t *testing.T) {
	store := &attachmentTestStore{}
	model := &fakeModel{rounds: []fakeRound{{events: []model.StreamEvent{{Delta: "ok"}, {Done: true}}}}}
	runner := NewRunner(model, &fakeUI{}, nil, store, "session-1")

	_, err := runner.RunRichTurn(context.Background(), message.Message{
		Role:    message.RoleUser,
		Content: "look [Image 1]",
		Parts: []message.ContentPart{
			{Type: message.ContentPartText, Text: "look "},
			{Type: message.ContentPartImage, Image: &message.ImagePart{MIMEType: "image/png", Data: []byte("png")}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(store.saved) != "png" {
		t.Fatalf("saved image = %q, want png", store.saved)
	}
	if len(model.calls) != 1 || len(model.calls[0]) < 2 {
		t.Fatalf("model calls = %#v", model.calls)
	}
	parts := model.calls[0][1].Parts
	if len(parts) != 2 || parts[1].Image == nil || string(parts[1].Image.Data) != "png" {
		t.Fatalf("materialized model parts = %#v", parts)
	}
	if len(store.appends) != 1 || store.appends[0][0].Parts[1].Image.Attachment != "attachments/test.png" {
		t.Fatalf("persisted parts = %#v", store.appends)
	}
}
