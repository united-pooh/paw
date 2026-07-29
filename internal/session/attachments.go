package session

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

const attachmentsDir = "attachments"

// SaveAttachment stores image bytes by content hash and returns a path
// relative to the .paw directory, such as attachments/<sha256>.png.
func (s *JSONLStore) SaveAttachment(ctx context.Context, mimeType string, data []byte) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if len(data) == 0 {
		return "", fmt.Errorf("附件内容为空")
	}
	mimeType = normalizeImageMIME(mimeType, data)
	if !strings.HasPrefix(mimeType, "image/") {
		return "", fmt.Errorf("不支持的图片 MIME 类型: %s", mimeType)
	}

	digest := sha256.Sum256(data)
	name := hex.EncodeToString(digest[:]) + imageExtension(mimeType)
	relative := filepath.ToSlash(filepath.Join(attachmentsDir, name))
	target := filepath.Join(s.baseDir, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return "", fmt.Errorf("创建附件目录失败: %w", err)
	}

	f, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if !os.IsExist(err) {
			return "", fmt.Errorf("创建附件文件失败: %w", err)
		}
		// Content-addressed files are immutable. A repeated paste is already
		// persisted and does not need to rewrite the existing bytes.
		return relative, nil
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(target)
		return "", fmt.Errorf("写入附件失败: %w", err)
	}
	if err := f.Close(); err != nil {
		return "", fmt.Errorf("关闭附件文件失败: %w", err)
	}
	return relative, nil
}

// ReadAttachment loads an attachment referenced by a session message.
func (s *JSONLStore) ReadAttachment(ctx context.Context, reference string) (string, []byte, error) {
	if err := ctx.Err(); err != nil {
		return "", nil, err
	}
	target, err := s.attachmentPath(reference)
	if err != nil {
		return "", nil, err
	}
	data, err := os.ReadFile(target)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil, fmt.Errorf("附件不存在: %s", reference)
		}
		return "", nil, fmt.Errorf("读取附件失败(%s): %w", reference, err)
	}
	mimeType := normalizeImageMIME(mimeFromExtension(filepath.Ext(target)), data)
	return mimeType, data, nil
}

func (s *JSONLStore) attachmentPath(reference string) (string, error) {
	reference = filepath.Clean(filepath.FromSlash(strings.TrimSpace(reference)))
	if reference == "." || filepath.IsAbs(reference) || reference == ".." || strings.HasPrefix(reference, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("非法附件引用: %q", reference)
	}
	if filepath.Dir(reference) != attachmentsDir {
		return "", fmt.Errorf("附件引用必须位于 %s/: %q", attachmentsDir, reference)
	}
	target := filepath.Join(s.baseDir, reference)
	rel, err := filepath.Rel(s.baseDir, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("附件引用越界: %q", reference)
	}
	return target, nil
}

func normalizeImageMIME(mimeType string, data []byte) string {
	mimeType = strings.ToLower(strings.TrimSpace(strings.Split(mimeType, ";")[0]))
	if mimeType == "" || mimeType == "application/octet-stream" {
		mimeType = http.DetectContentType(data)
	}
	return mimeType
}

func imageExtension(mimeType string) string {
	switch mimeType {
	case "image/jpeg":
		return ".jpg"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	case "image/bmp":
		return ".bmp"
	default:
		return ".png"
	}
}

func mimeFromExtension(ext string) string {
	switch strings.ToLower(ext) {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".bmp":
		return "image/bmp"
	default:
		return "image/png"
	}
}
