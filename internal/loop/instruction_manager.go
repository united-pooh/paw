package loop

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// projectInstructionFileName is the project-level agent instruction file,
// matched case-insensitively so agent.md, Agent.md and AGENT.md are all
// recognized.
const projectInstructionFileName = "agent.md"

// globalInstructionDir is the global instruction directory under the user
// home directory (~/.paw/agent.md, case-insensitive).
const globalInstructionDir = ".paw"

// InstructionManager loads project and global agent instructions as inert
// text and caches them. Global instructions come from ~/.paw/agent.md;
// project instructions are looked up from root upward through parent
// directories. Both are matched case-insensitively.
type InstructionManager struct {
	root     string
	homeDir  string
	readFile func(string) ([]byte, error)

	once    sync.Once
	global  string
	project string
	err     error
}

// NewInstructionManager creates an instruction manager rooted at root. An
// empty root falls back to the current working directory.
func NewInstructionManager(root string) *InstructionManager {
	return &InstructionManager{
		root:     root,
		readFile: os.ReadFile,
	}
}

// GlobalInstructions returns cached ~/.paw/agent.md content, or an empty
// string when no file is found.
func (m *InstructionManager) GlobalInstructions() string {
	if m == nil {
		return ""
	}
	m.once.Do(m.load)
	return m.global
}

// ProjectInstructions returns cached agent.md content found from the root
// directory upward, or an empty string when no file is found.
func (m *InstructionManager) ProjectInstructions() string {
	if m == nil {
		return ""
	}
	m.once.Do(m.load)
	return m.project
}

func (m *InstructionManager) load() {
	if path, ok := findGlobalInstructionFile(m.userHomeDir()); ok {
		data, err := m.readFile(path)
		if err != nil {
			m.err = err
			return
		}
		m.global = strings.TrimSpace(string(data))
	}

	root := m.root
	if strings.TrimSpace(root) == "" {
		var err error
		root, err = os.Getwd()
		if err != nil {
			m.err = err
			return
		}
	}
	path, ok := findProjectInstructionFile(root)
	if !ok {
		return
	}
	data, err := m.readFile(path)
	if err != nil {
		m.err = err
		return
	}
	m.project = strings.TrimSpace(string(data))
}

func (m *InstructionManager) userHomeDir() string {
	if strings.TrimSpace(m.homeDir) != "" {
		return m.homeDir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return home
}

// findInstructionFileInDir returns the path of the first regular file whose
// lowercase name equals "agent.md", so Agent.md and AGENT.md match too.
func findInstructionFileInDir(dir string) (string, bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", false
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if strings.ToLower(entry.Name()) == projectInstructionFileName {
			return filepath.Join(dir, entry.Name()), true
		}
	}
	return "", false
}

// findGlobalInstructionFile looks for agent.md (case-insensitive) inside
// ~/.paw. An unresolvable home directory yields no match.
func findGlobalInstructionFile(home string) (string, bool) {
	if strings.TrimSpace(home) == "" {
		return "", false
	}
	return findInstructionFileInDir(filepath.Join(home, globalInstructionDir))
}

// findProjectInstructionFile walks root and its parent directories looking
// for agent.md (case-insensitive).
func findProjectInstructionFile(root string) (string, bool) {
	root = filepath.Clean(root)
	info, err := os.Stat(root)
	if err == nil && !info.IsDir() {
		root = filepath.Dir(root)
	}
	for {
		if path, ok := findInstructionFileInDir(root); ok {
			return path, true
		}
		parent := filepath.Dir(root)
		if parent == root {
			return "", false
		}
		root = parent
	}
}
