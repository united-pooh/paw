package session

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"paw/internal/message"
	"strings"
	"testing"
)

func TestJSONLStoreMessagePersistsAttachmentReferenceWithoutImageBytes(t *testing.T) {
	store, err := NewJSONLStore(filepath.Join(t.TempDir(), ".paw"))
	if err != nil {
		t.Fatal(err)
	}
	data := []byte{0x89, 'P', 'N', 'G', 0x01, 0x02, 0x03}
	ref, err := store.SaveAttachment(context.Background(), "image/png", data)
	if err != nil {
		t.Fatal(err)
	}
	msg := message.Message{
		Role:    message.RoleUser,
		Content: "[Image 1]",
		Parts: []message.ContentPart{{
			Type:  message.ContentPartImage,
			Image: &message.ImagePart{MIMEType: "image/png", Attachment: ref, Data: data},
		}},
	}
	encoded, err := json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) == "" || strings.Contains(string(encoded), `"data"`) {
		t.Fatalf("serialized message unexpectedly contains image data: %s", encoded)
	}
	var restored message.Message
	if err := json.Unmarshal(encoded, &restored); err != nil {
		t.Fatal(err)
	}
	if len(restored.Parts) != 1 || restored.Parts[0].Image == nil || restored.Parts[0].Image.Attachment != ref || len(restored.Parts[0].Image.Data) != 0 {
		t.Fatalf("restored message = %#v", restored)
	}
}

func TestJSONLStoreAttachmentIsContentAddressedAndPrivate(t *testing.T) {
	root := filepath.Join(t.TempDir(), ".paw")
	store, err := NewJSONLStore(root)
	if err != nil {
		t.Fatal(err)
	}
	data := []byte("\x89PNG\r\n\x1a\nimage")

	ref, err := store.SaveAttachment(context.Background(), "image/png", data)
	if err != nil {
		t.Fatal(err)
	}
	if ref == "" || filepath.Dir(filepath.FromSlash(ref)) != attachmentsDir {
		t.Fatalf("reference = %q, want direct attachments path", ref)
	}
	path := filepath.Join(root, filepath.FromSlash(ref))
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("attachment permissions = %o, want 600", got)
	}

	refAgain, err := store.SaveAttachment(context.Background(), "image/png", data)
	if err != nil {
		t.Fatal(err)
	}
	if refAgain != ref {
		t.Fatalf("repeat reference = %q, want %q", refAgain, ref)
	}

	mime, got, err := store.ReadAttachment(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	if mime != "image/png" || string(got) != string(data) {
		t.Fatalf("read attachment = (%q, %q), want image/png and original bytes", mime, got)
	}
	if _, _, err := store.ReadAttachment(context.Background(), "other/attachments/escape.png"); err == nil {
		t.Fatal("nested attachment reference unexpectedly accepted")
	}
}
