package loop

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const projectInstructionFile = "AGENTS.md"

// InstructionManager loads project instructions as inert text and caches them.
type InstructionManager struct {
	root     string
	readFile func(string) ([]byte, error)

	once    sync.Once
	content string
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

// ProjectInstructions returns cached AGENTS.md content, or an empty string when
// no file is found.
func (m *InstructionManager) ProjectInstructions() string {
	if m == nil {
		return ""
	}
	m.once.Do(m.load)
	return m.content
}

func (m *InstructionManager) load() {
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
	m.content = strings.TrimSpace(string(data))
}

func findProjectInstructionFile(root string) (string, bool) {
	root = filepath.Clean(root)
	info, err := os.Stat(root)
	if err == nil && !info.IsDir() {
		root = filepath.Dir(root)
	}
	for {
		path := filepath.Join(root, projectInstructionFile)
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path, true
		}
		parent := filepath.Dir(root)
		if parent == root {
			return "", false
		}
		root = parent
	}
}
