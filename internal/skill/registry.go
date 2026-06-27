package skill

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"unicode"
)

const SkillFileName = "SKILL.md"

// Skill describes one local Codex/Claude-style skill.
type Skill struct {
	Name        string
	DisplayName string
	Description string
	Path        string
	Root        string
}

// Registry discovers local skills from a small set of filesystem roots.
type Registry struct {
	mu      sync.RWMutex
	roots   []string
	loaded  bool
	skills  []Skill
	lastErr error
}

// NewRegistry returns a skill registry backed by roots that contain
// skill-name/SKILL.md entries.
func NewRegistry(roots []string) *Registry {
	return &Registry{roots: cleanRoots(roots)}
}

// DefaultRoots returns the project and user skill directories used by Codex and
// Claude-style local installs. The directories do not need to exist.
func DefaultRoots(workRoot string) []string {
	var roots []string
	if strings.TrimSpace(workRoot) != "" {
		roots = append(roots,
			filepath.Join(workRoot, ".codex", "skills"),
			filepath.Join(workRoot, ".claude", "skills"),
		)
	}
	if codexHome := strings.TrimSpace(os.Getenv("CODEX_HOME")); codexHome != "" {
		roots = append(roots, filepath.Join(codexHome, "skills"))
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		roots = append(roots,
			filepath.Join(home, ".codex", "skills"),
			filepath.Join(home, ".claude", "skills"),
		)
	}
	return cleanRoots(roots)
}

// Roots returns a copy of the configured skill roots.
func (r *Registry) Roots() []string {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]string(nil), r.roots...)
}

// Skills returns the discovered skills, sorted by name.
func (r *Registry) Skills() []Skill {
	if r == nil {
		return nil
	}
	r.ensureLoaded()
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]Skill(nil), r.skills...)
}

// LastErr reports the last discovery error. Discovery is best-effort, so callers
// can usually continue even when this is non-nil.
func (r *Registry) LastErr() error {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.lastErr
}

// Refresh forces the next lookup to rescan the filesystem.
func (r *Registry) Refresh() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.loaded = false
	r.skills = nil
	r.lastErr = nil
}

// Find returns skills whose name or display name starts with prefix, ignoring
// case. Empty prefix returns the first batch of skills.
func (r *Registry) Find(prefix string) []Skill {
	prefix = strings.ToLower(strings.TrimSpace(prefix))
	var matches []Skill
	for _, sk := range r.Skills() {
		if prefix == "" ||
			strings.HasPrefix(strings.ToLower(sk.Name), prefix) ||
			strings.HasPrefix(strings.ToLower(sk.DisplayName), prefix) {
			matches = append(matches, sk)
		}
	}
	return matches
}

// Resolve finds a skill by name, display name, SKILL.md path, or directory path.
func (r *Registry) Resolve(ref string) (Skill, bool) {
	ref = normalizeSkillRef(ref)
	if ref == "" {
		return Skill{}, false
	}
	if sk, ok := skillFromPath(ref); ok {
		return sk, true
	}
	lower := strings.ToLower(ref)
	for _, sk := range r.Skills() {
		if strings.ToLower(sk.Name) == lower ||
			strings.ToLower(sk.DisplayName) == lower ||
			strings.EqualFold(sk.Path, ref) ||
			strings.EqualFold(sk.Root, ref) {
			return sk, true
		}
	}
	return Skill{}, false
}

// MentionedSkills resolves all skill references found in text. Supported forms:
// [$name](/path/to/SKILL.md) and bare $name at a word boundary.
func (r *Registry) MentionedSkills(text string) []Skill {
	var resolved []Skill
	seen := map[string]bool{}
	add := func(sk Skill) {
		key := sk.Path
		if key == "" {
			key = sk.Name
		}
		if seen[key] {
			return
		}
		seen[key] = true
		resolved = append(resolved, sk)
	}

	for _, mention := range parseMarkdownMentions(text) {
		if mention.Path != "" {
			if sk, ok := skillFromPath(mention.Path); ok {
				add(sk)
				continue
			}
		}
		if sk, ok := r.Resolve(mention.Name); ok {
			add(sk)
		}
	}
	for _, name := range parseBareMentions(text) {
		if sk, ok := r.Resolve(name); ok {
			add(sk)
		}
	}
	return resolved
}

// InstructionContext loads all mentioned skill files and formats them for
// turn-scoped system prompt injection.
func (r *Registry) InstructionContext(text string) (string, []Skill, []error) {
	skills := r.MentionedSkills(text)
	if len(skills) == 0 {
		return "", nil, nil
	}
	var builder strings.Builder
	var loaded []Skill
	var errs []error
	builder.WriteString("Selected skills for this turn. Follow these local skill instructions after higher-priority system, developer, and project instructions. If a skill refers to relative files, resolve them relative to the skill directory.\n")
	for _, sk := range skills {
		content, err := os.ReadFile(sk.Path)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", sk.Name, err))
			continue
		}
		loaded = append(loaded, sk)
		builder.WriteString("\n## Skill: ")
		builder.WriteString(sk.Name)
		builder.WriteByte('\n')
		if desc := strings.TrimSpace(sk.Description); desc != "" {
			builder.WriteString("Description: ")
			builder.WriteString(desc)
			builder.WriteByte('\n')
		}
		builder.WriteString("Path: ")
		builder.WriteString(sk.Path)
		builder.WriteString("\n\n")
		builder.WriteString(strings.TrimSpace(string(content)))
		builder.WriteByte('\n')
	}
	if len(loaded) == 0 {
		return "", nil, errs
	}
	return strings.TrimSpace(builder.String()) + "\n", loaded, errs
}

func (r *Registry) ensureLoaded() {
	r.mu.RLock()
	loaded := r.loaded
	r.mu.RUnlock()
	if loaded {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.loaded {
		return
	}
	skills, err := discover(r.roots)
	r.skills = skills
	r.lastErr = err
	r.loaded = true
}

func discover(roots []string) ([]Skill, error) {
	seen := map[string]bool{}
	var skills []Skill
	var errs []string
	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			errs = append(errs, err.Error())
			continue
		}
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), ".") || !entry.IsDir() {
				continue
			}
			path := filepath.Join(root, entry.Name(), SkillFileName)
			info, err := os.Stat(path)
			if err != nil || info.IsDir() {
				continue
			}
			abs, err := filepath.Abs(path)
			if err != nil {
				abs = path
			}
			if seen[abs] {
				continue
			}
			seen[abs] = true
			content, err := os.ReadFile(abs)
			if err != nil {
				errs = append(errs, err.Error())
				continue
			}
			meta := parseMetadata(string(content))
			name := entry.Name()
			skills = append(skills, Skill{
				Name:        name,
				DisplayName: meta.Name,
				Description: meta.Description,
				Path:        abs,
				Root:        filepath.Dir(abs),
			})
		}
	}
	sort.SliceStable(skills, func(i, j int) bool {
		return strings.ToLower(skills[i].Name) < strings.ToLower(skills[j].Name)
	})
	if len(errs) > 0 {
		return skills, fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return skills, nil
}

type metadata struct {
	Name        string
	Description string
}

func parseMetadata(content string) metadata {
	var meta metadata
	body := content
	if strings.HasPrefix(content, "---") {
		lines := strings.Split(content, "\n")
		end := -1
		for i := 1; i < len(lines); i++ {
			if strings.TrimSpace(lines[i]) == "---" {
				end = i
				break
			}
		}
		if end > 0 {
			for _, line := range lines[1:end] {
				key, value, ok := strings.Cut(line, ":")
				if !ok {
					continue
				}
				value = strings.Trim(strings.TrimSpace(value), `"'`)
				switch strings.ToLower(strings.TrimSpace(key)) {
				case "name":
					meta.Name = value
				case "description":
					meta.Description = value
				case "when_to_use", "when-to-use":
					if meta.Description == "" {
						meta.Description = value
					}
				}
			}
			body = strings.Join(lines[end+1:], "\n")
		}
	}
	if meta.Description == "" {
		meta.Description = firstBodySentence(body)
	}
	return meta
}

func firstBodySentence(body string) string {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(strings.TrimLeft(line, "#"))
		if line != "" {
			return line
		}
	}
	return ""
}

func cleanRoots(roots []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, root := range roots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		expanded := expandHome(root)
		abs, err := filepath.Abs(expanded)
		if err == nil {
			expanded = abs
		}
		if seen[expanded] {
			continue
		}
		seen[expanded] = true
		out = append(out, expanded)
	}
	return out
}

func expandHome(path string) string {
	if path == "~" {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			return home
		}
	}
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			return filepath.Join(home, strings.TrimPrefix(path, "~/"))
		}
	}
	return path
}

func normalizeSkillRef(ref string) string {
	ref = strings.TrimSpace(ref)
	ref = strings.TrimPrefix(ref, "$")
	ref = strings.Trim(ref, "`\"'")
	return ref
}

func skillFromPath(path string) (Skill, bool) {
	path = strings.Trim(strings.TrimSpace(path), "`\"'")
	if path == "" {
		return Skill{}, false
	}
	if strings.HasPrefix(path, "file://") {
		path = strings.TrimPrefix(path, "file://")
	}
	if !filepath.IsAbs(path) {
		abs, err := filepath.Abs(path)
		if err == nil {
			path = abs
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		return Skill{}, false
	}
	if info.IsDir() {
		path = filepath.Join(path, SkillFileName)
		info, err = os.Stat(path)
		if err != nil || info.IsDir() {
			return Skill{}, false
		}
	}
	if !strings.EqualFold(filepath.Base(path), SkillFileName) {
		return Skill{}, false
	}
	abs, err := filepath.Abs(path)
	if err == nil {
		path = abs
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return Skill{}, false
	}
	meta := parseMetadata(string(content))
	return Skill{
		Name:        filepath.Base(filepath.Dir(path)),
		DisplayName: meta.Name,
		Description: meta.Description,
		Path:        path,
		Root:        filepath.Dir(path),
	}, true
}

type markdownMention struct {
	Name string
	Path string
}

var markdownSkillRE = regexp.MustCompile(`\[\$?([A-Za-z0-9][A-Za-z0-9_.:-]*)\]\(([^)\s]+)\)`)

func parseMarkdownMentions(text string) []markdownMention {
	matches := markdownSkillRE.FindAllStringSubmatch(text, -1)
	out := make([]markdownMention, 0, len(matches))
	for _, match := range matches {
		out = append(out, markdownMention{Name: match[1], Path: match[2]})
	}
	return out
}

func parseBareMentions(text string) []string {
	var names []string
	runes := []rune(text)
	for i := 0; i < len(runes); i++ {
		if runes[i] != '$' {
			continue
		}
		if i > 0 && !unicode.IsSpace(runes[i-1]) && runes[i-1] != '(' && runes[i-1] != '[' {
			continue
		}
		start := i + 1
		j := start
		for j < len(runes) && isSkillNameRune(runes[j]) {
			j++
		}
		if j > start {
			names = append(names, string(runes[start:j]))
			i = j - 1
		}
	}
	return names
}

func isSkillNameRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' || r == ':' || r == '.'
}
