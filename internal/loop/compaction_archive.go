package loop

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"

	"paw/internal/message"
)

type compactionArchive struct {
	dir       string
	sessionID string
	enabled   bool
	now       func() time.Time
	syncFile  func(*os.File) error
	mu        sync.Mutex
}

type archiveRequest struct {
	Operation       string
	MessageIndex    int
	ToolResultIndex int
	ToolUseID       string
	ToolName        string
	OriginalBytes   int
	Message         message.Message
	OriginalContent string
}

type archiveResult struct {
	Paths []string
	ByKey map[string]string
}

type archiveRecord struct {
	Operation       string          `json:"operation"`
	SessionID       string          `json:"session_id"`
	MessageIndex    int             `json:"message_index"`
	ToolResultIndex int             `json:"tool_result_index"`
	ToolUseID       string          `json:"tool_use_id"`
	ToolName        string          `json:"tool_name"`
	OriginalBytes   int             `json:"original_bytes"`
	Message         message.Message `json:"message"`
	CreatedAt       time.Time       `json:"created_at"`
}

type archiveIndexRecord struct {
	Key       string    `json:"key"`
	Path      string    `json:"path"`
	CreatedAt time.Time `json:"created_at"`
}

func newCompactionArchive(workRoot, sessionID string, enabled bool) (*compactionArchive, error) {
	if strings.TrimSpace(workRoot) == "" {
		var err error
		workRoot, err = os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("resolve compaction archive root: %w", err)
		}
	}
	root, err := filepath.Abs(filepath.Join(workRoot, ".paw", "sessions"))
	if err != nil {
		return nil, fmt.Errorf("resolve sessions archive root: %w", err)
	}
	dir := filepath.Join(root, safeSessionID(sessionID), "compactions")
	rel, err := filepath.Rel(root, dir)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return nil, fmt.Errorf("compaction archive path escapes sessions root")
	}
	return &compactionArchive{
		dir:       dir,
		sessionID: sessionID,
		enabled:   enabled,
		now:       time.Now,
		syncFile:  func(file *os.File) error { return file.Sync() },
	}, nil
}

func safeSessionID(sessionID string) string {
	var builder strings.Builder
	for _, r := range strings.TrimSpace(sessionID) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '.' || r == '_' || r == '-' {
			builder.WriteRune(r)
		} else {
			builder.WriteByte('_')
		}
	}
	if builder.Len() == 0 {
		return "session"
	}
	return builder.String()
}

func archiveKey(sessionID, toolUseID, content string) string {
	sum := sha256.Sum256([]byte(sessionID + "\x00" + toolUseID + "\x00" + content))
	return hex.EncodeToString(sum[:])
}

func (archive *compactionArchive) archive(requests []archiveRequest) (archiveResult, error) {
	result := archiveResult{ByKey: make(map[string]string)}
	if archive == nil || !archive.enabled || len(requests) == 0 {
		return result, nil
	}
	archive.mu.Lock()
	defer archive.mu.Unlock()

	if err := os.MkdirAll(archive.dir, 0o700); err != nil {
		return archiveResult{}, fmt.Errorf("create compaction archive directory: %w", err)
	}
	index, err := archive.loadIndex()
	if err != nil {
		return archiveResult{}, err
	}

	type pendingRecord struct {
		key     string
		request archiveRequest
	}
	pending := make([]pendingRecord, 0, len(requests))
	pathsByRequest := make([]string, len(requests))
	for i, request := range requests {
		key := archiveKey(archive.sessionID, request.ToolUseID, request.OriginalContent)
		if path, ok := index[key]; ok && archive.validExistingPath(path) {
			pathsByRequest[i] = path
			result.ByKey[key] = path
			continue
		}
		pending = append(pending, pendingRecord{key: key, request: request})
	}

	if len(pending) > 0 {
		operation := safeOperation(pending[0].request.Operation)
		now := archive.now().UTC()
		base := now.Format("20060102-150405.000") + "-" + operation + ".jsonl"
		finalPath := filepath.Join(archive.dir, base)
		for suffix := 1; ; suffix++ {
			if _, statErr := os.Stat(finalPath); os.IsNotExist(statErr) {
				break
			}
			finalPath = filepath.Join(archive.dir, fmt.Sprintf("%s-%d-%s.jsonl", now.Format("20060102-150405.000"), suffix, operation))
		}
		temp, err := os.CreateTemp(archive.dir, ".compaction-*.tmp")
		if err != nil {
			return archiveResult{}, fmt.Errorf("create compaction archive temp file: %w", err)
		}
		tempPath := temp.Name()
		published := false
		defer func() {
			_ = temp.Close()
			if !published {
				_ = os.Remove(tempPath)
			}
		}()
		encoder := json.NewEncoder(temp)
		for _, item := range pending {
			request := item.request
			originalBytes := request.OriginalBytes
			if originalBytes == 0 {
				originalBytes = len([]byte(request.OriginalContent))
			}
			record := archiveRecord{
				Operation: request.Operation, SessionID: archive.sessionID,
				MessageIndex: request.MessageIndex, ToolResultIndex: request.ToolResultIndex,
				ToolUseID: request.ToolUseID, ToolName: request.ToolName,
				OriginalBytes: originalBytes, Message: request.Message, CreatedAt: now,
			}
			if err := encoder.Encode(record); err != nil {
				return archiveResult{}, fmt.Errorf("write compaction archive: %w", err)
			}
		}
		if err := archive.syncFile(temp); err != nil {
			return archiveResult{}, fmt.Errorf("sync compaction archive: %w", err)
		}
		if err := temp.Close(); err != nil {
			return archiveResult{}, fmt.Errorf("close compaction archive: %w", err)
		}
		if err := os.Rename(tempPath, finalPath); err != nil {
			return archiveResult{}, fmt.Errorf("publish compaction archive: %w", err)
		}
		published = true

		newIndexRecords := make([]archiveIndexRecord, 0, len(pending))
		for _, item := range pending {
			newIndexRecords = append(newIndexRecords, archiveIndexRecord{Key: item.key, Path: finalPath, CreatedAt: now})
			result.ByKey[item.key] = finalPath
		}
		if err := archive.appendIndex(newIndexRecords); err != nil {
			return archiveResult{}, err
		}
		for i, request := range requests {
			if pathsByRequest[i] == "" {
				pathsByRequest[i] = result.ByKey[archiveKey(archive.sessionID, request.ToolUseID, request.OriginalContent)]
			}
		}
	}

	seen := make(map[string]bool)
	for _, path := range pathsByRequest {
		if path != "" && !seen[path] {
			seen[path] = true
			result.Paths = append(result.Paths, path)
		}
	}
	return result, nil
}

func (archive *compactionArchive) loadIndex() (map[string]string, error) {
	result := make(map[string]string)
	file, err := os.Open(filepath.Join(archive.dir, "index.jsonl"))
	if err != nil {
		if os.IsNotExist(err) {
			return result, nil
		}
		return nil, fmt.Errorf("open compaction archive index: %w", err)
	}
	defer func() { _ = file.Close() }()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		var record archiveIndexRecord
		if json.Unmarshal(scanner.Bytes(), &record) == nil && record.Key != "" && record.Path != "" {
			result[record.Key] = record.Path
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read compaction archive index: %w", err)
	}
	return result, nil
}

func (archive *compactionArchive) appendIndex(records []archiveIndexRecord) error {
	if len(records) == 0 {
		return nil
	}
	file, err := os.OpenFile(filepath.Join(archive.dir, "index.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open compaction archive index: %w", err)
	}
	encoder := json.NewEncoder(file)
	for _, record := range records {
		if err := encoder.Encode(record); err != nil {
			_ = file.Close()
			return fmt.Errorf("write compaction archive index: %w", err)
		}
	}
	if err := archive.syncFile(file); err != nil {
		_ = file.Close()
		return fmt.Errorf("sync compaction archive index: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close compaction archive index: %w", err)
	}
	return nil
}

func (archive *compactionArchive) validExistingPath(path string) bool {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(archive.dir, absolute)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return false
	}
	info, err := os.Stat(absolute)
	return err == nil && !info.IsDir()
}

func safeOperation(operation string) string {
	operation = strings.ToLower(strings.TrimSpace(operation))
	switch operation {
	case "snip", "prune", "fold":
		return operation
	default:
		return "compact"
	}
}
