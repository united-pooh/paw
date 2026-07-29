package bubble

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"os/exec"
	"paw/internal/message"
	"runtime"
	"strings"

	textclipboard "github.com/atotto/clipboard"
	tea "github.com/charmbracelet/bubbletea"
)

var errClipboardNoImage = fmt.Errorf("剪贴板不包含图片")

// These function variables keep the OS integration small and make the TUI
// behavior deterministic in tests without adding a clipboard-image library.
var readClipboardImageFn = readSystemClipboardImage
var readClipboardTextFn = textclipboard.ReadAll

type clipboardPasteMsg struct {
	cursor int
	image  *message.ImagePart
	text   string
	err    error
}

func clipboardPasteCmd(ctx context.Context, cursor int) tea.Cmd {
	return func() tea.Msg {
		if ctx == nil {
			ctx = context.Background()
		}
		image, imageErr := readClipboardImageFn(ctx)
		if imageErr == nil {
			return clipboardPasteMsg{cursor: cursor, image: &image}
		}
		text, textErr := readClipboardTextFn()
		if textErr == nil {
			return clipboardPasteMsg{cursor: cursor, text: text}
		}
		if imageErr != nil && imageErr != errClipboardNoImage {
			return clipboardPasteMsg{cursor: cursor, err: imageErr}
		}
		return clipboardPasteMsg{cursor: cursor, err: textErr}
	}
}

func readSystemClipboardImage(ctx context.Context) (message.ImagePart, error) {
	if runtime.GOOS == "darwin" {
		if image, err := readMacOSClipboardImage(ctx); err == nil {
			return image, nil
		}
	}
	commands := clipboardImageCommands(runtime.GOOS)
	for _, command := range commands {
		data, err := exec.CommandContext(ctx, command[0], command[1:]...).Output()
		if err != nil || len(data) == 0 {
			continue
		}
		mimeType := imageMIMEType(data)
		if mimeType == "" {
			continue
		}
		return message.ImagePart{MIMEType: mimeType, Data: data}, nil
	}
	return message.ImagePart{}, errClipboardNoImage
}

// macOS's pbpaste only supports text/RTF/PostScript on current systems; it
// cannot read the public.png/public.tiff flavors produced by screenshots. Use
// the system-provided JXA bridge to read NSPasteboard and normalize TIFF to
// PNG with AppKit's built-in image encoder. The command returns base64 so
// binary image data is not corrupted by osascript's text output.
func readMacOSClipboardImage(ctx context.Context) (message.ImagePart, error) {
	cmd := exec.CommandContext(ctx, "osascript", "-l", "JavaScript")
	cmd.Stdin = strings.NewReader(macOSClipboardImageScript)
	// osascript's JXA console.log is emitted on stderr on macOS, so use the
	// combined stream here instead of Output (which would silently return an
	// empty stdout buffer).
	encoded, err := cmd.CombinedOutput()
	if err != nil {
		return message.ImagePart{}, errClipboardNoImage
	}
	encodedText := strings.TrimSpace(string(encoded))
	if encodedText == "" || encodedText == "NO_IMAGE" {
		return message.ImagePart{}, errClipboardNoImage
	}
	data, err := base64.StdEncoding.DecodeString(encodedText)
	if err != nil {
		return message.ImagePart{}, errClipboardNoImage
	}
	mimeType := imageMIMEType(data)
	if mimeType == "" {
		return message.ImagePart{}, errClipboardNoImage
	}
	return message.ImagePart{MIMEType: mimeType, Data: data}, nil
}

const macOSClipboardImageScript = `ObjC.import('AppKit');
ObjC.import('Foundation');

var pasteboard = $.NSPasteboard.generalPasteboard;
var types = ['public.png', 'public.jpeg', 'public.gif', 'public.webp', 'public.tiff'];
for (var i = 0; i < types.length; i++) {
  var data = pasteboard.dataForType(types[i]);
  if (!data) continue;
  if (types[i] === 'public.tiff') {
    var bitmap = $.NSBitmapImageRep.imageRepWithData(data);
    if (!bitmap) continue;
    data = bitmap.representationUsingTypeProperties($.NSBitmapImageFileTypePNG, $({}));
    if (!data) continue;
  }
  console.log(ObjC.unwrap(data.base64EncodedStringWithOptions(0)));
  break;
}
if (i === types.length) console.log('NO_IMAGE');
`

func clipboardImageCommands(goos string) [][]string {
	switch goos {
	case "darwin":
		return [][]string{{"pbpaste", "-Prefer", "png"}}
	case "linux":
		return [][]string{
			{"wl-paste", "--no-newline", "--type", "image/png"},
			{"xclip", "-selection", "clipboard", "-target", "image/png", "-out"},
			{"xclip", "-selection", "clipboard", "-t", "image/jpeg", "-out"},
		}
	default:
		return nil
	}
}

func imageMIMEType(data []byte) string {
	detected := strings.ToLower(strings.Split(http.DetectContentType(data), ";")[0])
	switch detected {
	case "image/png", "image/jpeg", "image/gif", "image/webp", "image/bmp":
		return detected
	default:
		return ""
	}
}

func (m *appModel) insertClipboardText(text string) {
	if m == nil || text == "" {
		return
	}
	beforeText := m.input.Value()
	beforeCursor := textareaAbsoluteCursor(m.input)
	beforeTokens := cloneInputTokens(m.inputTokens)
	m.input.InsertString(text)
	afterText := m.input.Value()
	afterCursor := textareaAbsoluteCursor(m.input)
	if beforeText != afterText {
		m.reconcileInputTokenEdit(beforeText, afterText, beforeCursor, afterCursor, beforeTokens)
	}
}

func (m *appModel) insertClipboardImage(cursor int, image message.ImagePart) {
	if m == nil || len(image.Data) == 0 {
		return
	}
	text := m.input.Value()
	cursor = clampInt(cursor, 0, len([]rune(text)))
	number := 1
	for _, token := range m.inputTokens {
		if token.Kind == inputTokenImage {
			number++
		}
	}
	label := fmt.Sprintf("[Image %d]", number)
	m.replaceInputRangeWithToken(cursor, cursor, label, label, inputTokenImage, false)
	for i := range m.inputTokens {
		if m.inputTokens[i].Kind == inputTokenImage && m.inputTokens[i].Start == cursor && m.inputTokens[i].Label == label {
			m.inputTokens[i].Image = &message.ImagePart{
				MIMEType: image.MIMEType,
				Data:     append([]byte(nil), image.Data...),
			}
			break
		}
	}
	// replaceInputRangeWithToken leaves the caret immediately after the new
	// token, which is the expected insertion point for repeated pastes.
	m.inputPasteFoldActive = false
}

func messageFromInputDraft(draft inputDraft) message.Message {
	msg := message.Message{Role: message.RoleUser, Content: draft.Text}
	imageTokens := make([]inputToken, 0)
	for _, token := range normalizeInputTokens(draft.Text, draft.Tokens) {
		if token.Kind == inputTokenImage && token.Image != nil {
			imageTokens = append(imageTokens, token)
		}
	}
	if len(imageTokens) == 0 {
		return msg
	}

	runes := []rune(draft.Text)
	position := 0
	for _, token := range imageTokens {
		if token.Start > position {
			msg.Parts = append(msg.Parts, message.ContentPart{Type: message.ContentPartText, Text: string(runes[position:token.Start])})
		}
		image := *token.Image
		image.Data = append([]byte(nil), image.Data...)
		msg.Parts = append(msg.Parts, message.ContentPart{Type: message.ContentPartImage, Image: &image})
		position = token.End
	}
	if position < len(runes) {
		msg.Parts = append(msg.Parts, message.ContentPart{Type: message.ContentPartText, Text: string(runes[position:])})
	}
	return msg
}

func inputTokensFromMessage(msg message.Message) []inputToken {
	if len(msg.Parts) == 0 {
		return canonicalSkillReferenceTokens(msg.Content)
	}
	tokens := canonicalSkillReferenceTokens(msg.Content)
	searchAt := 0
	for _, part := range msg.Parts {
		if part.Type != message.ContentPartImage || part.Image == nil {
			continue
		}
		startByte := strings.Index(msg.Content[searchAt:], "[Image ")
		if startByte < 0 {
			continue
		}
		startByte += searchAt
		endByte := strings.IndexByte(msg.Content[startByte:], ']')
		if endByte < 0 {
			continue
		}
		endByte += startByte + 1
		label := msg.Content[startByte:endByte]
		if !strings.HasPrefix(label, "[Image ") {
			continue
		}
		tokens = append(tokens, inputToken{
			Kind:  inputTokenImage,
			Start: len([]rune(msg.Content[:startByte])),
			End:   len([]rune(msg.Content[:endByte])),
			Label: label,
			Image: &message.ImagePart{
				MIMEType:   part.Image.MIMEType,
				Attachment: part.Image.Attachment,
				Data:       append([]byte(nil), part.Image.Data...),
			},
		})
		searchAt = endByte
	}
	return normalizeInputTokens(msg.Content, tokens)
}
