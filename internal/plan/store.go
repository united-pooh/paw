package plan

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// FileStore persists PlanDocs as markdown files. Each file is the source of
// truth; the first line is an HTML-comment front matter carrying id, optional
// session id, title and status so the directory stays human-readable and
// git-friendly.
type FileStore struct {
	dir string
	now func() time.Time
}

func NewFileStore(dir string) *FileStore {
	return &FileStore{dir: dir, now: time.Now}
}

func (s *FileStore) Dir() string { return s.dir }

var (
	slugRe          = regexp.MustCompile(`[^a-z0-9]+`)
	safeFileRe      = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
	errInvalidID    = errors.New("invalid plan id")
	errPlanNotFound = errors.New("plan not found")
)

func safeID(id PlanID) (string, error) {
	raw := strings.TrimSpace(string(id))
	if raw == "" || !safeFileRe.MatchString(raw) || strings.Contains(raw, "..") {
		return "", errInvalidID
	}
	return raw, nil
}

// Slug converts a title/requirement into a filesystem-safe slug.
func Slug(text string) string {
	text = strings.ToLower(strings.TrimSpace(text))
	text = slugRe.ReplaceAllString(text, "-")
	text = strings.Trim(text, "-")
	runes := []rune(text)
	if len(runes) > 48 {
		text = string(runes[:48])
		text = strings.Trim(text, "-")
	}
	if text == "" {
		text = "plan"
	}
	return text
}

// FileNameForDate builds the file stem for a doc created on the given date.
func (s *FileStore) FileNameForDate(created time.Time, title string) string {
	return created.Format("2006-01-02") + "-" + Slug(title) + "-plan"
}

// NextID picks a unique file stem for a new document, appending -2/-3 when
// the natural stem already exists.
func (s *FileStore) NextID(ctx context.Context, title string) (PlanID, error) {
	if err := contextErr(ctx); err != nil {
		return "", err
	}
	stamp := s.now()
	base := s.FileNameForDate(stamp, title)
	id := PlanID(base)
	for n := 2; ; n++ {
		if _, err := os.Stat(filepath.Join(s.dir, string(id)+".md")); os.IsNotExist(err) {
			return id, nil
		} else if err != nil {
			return "", err
		}
		id = PlanID(fmt.Sprintf("%s-%d", base, n))
	}
}

func (s *FileStore) pathFor(id PlanID) (string, error) {
	raw, err := safeID(id)
	if err != nil {
		return "", err
	}
	return filepath.Join(s.dir, raw+".md"), nil
}

// Create writes a new plan document file.
func (s *FileStore) Create(ctx context.Context, doc PlanDoc) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	if doc.ID == "" {
		return errors.New("plan id is empty")
	}
	if doc.Status == "" {
		doc.Status = PlanDraft
	}
	path, err := s.pathFor(doc.ID)
	if err != nil {
		return err
	}
	if _, statErr := os.Stat(path); statErr == nil {
		return fmt.Errorf("plan %q already exists", doc.ID)
	}
	if doc.CreatedAt.IsZero() {
		doc.CreatedAt = s.now()
	}
	doc.UpdatedAt = doc.CreatedAt
	doc.Path = path
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(encodeDoc(doc)), 0o644)
}

// Update overwrites an existing plan document, preserving its front matter id.
func (s *FileStore) Update(ctx context.Context, doc PlanDoc) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	path, err := s.pathFor(doc.ID)
	if err != nil {
		return err
	}
	if _, statErr := os.Stat(path); os.IsNotExist(statErr) {
		return errPlanNotFound
	}
	doc.UpdatedAt = s.now()
	doc.Path = path
	return os.WriteFile(path, []byte(encodeDoc(doc)), 0o644)
}

// Get reads one plan document by id.
func (s *FileStore) Get(ctx context.Context, id PlanID) (PlanDoc, bool, error) {
	if err := contextErr(ctx); err != nil {
		return PlanDoc{}, false, err
	}
	path, err := s.pathFor(id)
	if err != nil {
		return PlanDoc{}, false, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return PlanDoc{}, false, nil
	}
	if err != nil {
		return PlanDoc{}, false, err
	}
	doc, ok := decodeDoc(string(data), string(id), path)
	return doc, ok, nil
}

// List returns all plan documents, oldest first.
func (s *FileStore) List(ctx context.Context) ([]PlanDoc, error) {
	if err := contextErr(ctx); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(s.dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	out := make([]PlanDoc, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".md")
		if !safeFileRe.MatchString(id) {
			continue
		}
		doc, ok := decodeDoc("", id, filepath.Join(s.dir, entry.Name()))
		if ok {
			out = append(out, doc)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

// MarkApproved sets a plan document's status to approved and persists it.
func (s *FileStore) MarkApproved(ctx context.Context, id PlanID) (PlanDoc, error) {
	doc, ok, err := s.Get(ctx, id)
	if err != nil {
		return PlanDoc{}, err
	}
	if !ok {
		return PlanDoc{}, errPlanNotFound
	}
	doc.Status = PlanApproved
	if err := s.Update(ctx, doc); err != nil {
		return PlanDoc{}, err
	}
	return doc, nil
}

const frontMatterPrefix = "<!-- paw-plan:"

func encodeDoc(doc PlanDoc) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s id=%s status=%s", frontMatterPrefix, doc.ID, doc.Status)
	if doc.SessionID != "" {
		fmt.Fprintf(&b, " session_id=%s", strings.ReplaceAll(doc.SessionID, " ", ""))
	}
	fmt.Fprintf(&b, " title=%s -->\n", strings.ReplaceAll(doc.Title, "-->", "—"))
	b.WriteString(doc.Content)
	if !strings.HasSuffix(doc.Content, "\n") && doc.Content != "" {
		b.WriteByte('\n')
	}
	return b.String()
}

func decodeDoc(overrideBody, id, path string) (PlanDoc, bool) {
	doc := PlanDoc{ID: PlanID(id), Path: path, Status: PlanDraft}
	body := overrideBody
	fileData := ""
	if overrideBody == "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return PlanDoc{}, false
		}
		fileData = string(data)
		body = fileData
	}
	if strings.HasPrefix(body, frontMatterPrefix) {
		end := strings.Index(body, "-->")
		if end > 0 {
			meta := body[len(frontMatterPrefix):end]
			// title 可能含空格，单独提取：取 title= 之后到 meta 结尾的剩余部分。
			if idx := strings.Index(meta, "title="); idx >= 0 {
				doc.Title = strings.TrimSpace(meta[idx+len("title="):])
			}
			for _, field := range strings.Fields(meta) {
				kv := strings.SplitN(field, "=", 2)
				if len(kv) != 2 {
					continue
				}
				switch kv[0] {
				case "id":
					if kv[1] != "" {
						doc.ID = PlanID(kv[1])
					}
				case "status":
					doc.Status = PlanStatus(kv[1])
				case "session_id":
					doc.SessionID = kv[1]
				}
			}
			body = body[end+3:]
			body = strings.TrimPrefix(body, "\n")
		}
	}
	doc.Content = body
	return doc, true
}

func contextErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}
