package bubble

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type worktreeState uint8

const (
	worktreeClean worktreeState = iota
	worktreeDirty
	worktreeConflict
)

type worktreeSnapshot struct {
	name     string
	ref      string
	state    worktreeState
	isGit    bool
	detached bool
}

type worktreeStatusReader func(context.Context, string) (worktreeSnapshot, error)

type worktreeRefreshMsg struct {
	snapshot worktreeSnapshot
	err      error
}

type worktreeRefreshTickMsg struct{}

const worktreeRefreshInterval = time.Second

func worktreeRefreshTickCmd() tea.Cmd {
	return tea.Tick(worktreeRefreshInterval, func(time.Time) tea.Msg {
		return worktreeRefreshTickMsg{}
	})
}

func worktreeRefreshCmd(ctx context.Context, cwd string, reader worktreeStatusReader) tea.Cmd {
	return func() tea.Msg {
		if ctx == nil {
			ctx = context.Background()
		}
		if reader == nil {
			reader = readWorktreeStatus
		}
		snapshot, err := reader(ctx, cwd)
		return worktreeRefreshMsg{snapshot: snapshot, err: err}
	}
}

func readWorktreeStatus(ctx context.Context, cwd string) (worktreeSnapshot, error) {
	snapshot := worktreeSnapshot{name: filepath.Base(filepath.Clean(cwd))}
	if snapshot.name == "." || snapshot.name == string(filepath.Separator) {
		snapshot.name = "workspace"
	}

	root, err := gitOutput(ctx, cwd, "rev-parse", "--show-toplevel")
	if err != nil {
		// A non-Git directory is a normal state for the UI, not an error to display.
		return snapshot, nil
	}
	root = strings.TrimSpace(root)
	if root != "" {
		snapshot.name = filepath.Base(filepath.Clean(root))
	}
	snapshot.isGit = true

	branch, branchErr := gitOutput(ctx, cwd, "branch", "--show-current")
	branch = strings.TrimSpace(branch)
	if branchErr != nil {
		return snapshot, branchErr
	}
	if branch == "" {
		sha, shaErr := gitOutput(ctx, cwd, "rev-parse", "--short", "HEAD")
		if shaErr != nil {
			return snapshot, shaErr
		}
		snapshot.ref = strings.TrimSpace(sha)
		snapshot.detached = true
	} else {
		snapshot.ref = branch
	}

	status, statusErr := gitOutput(ctx, cwd, "status", "--porcelain=v1", "--untracked-files=normal")
	if statusErr != nil {
		return snapshot, statusErr
	}
	for _, line := range strings.Split(status, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		if len(line) >= 2 && isConflictStatus(line[:2]) {
			snapshot.state = worktreeConflict
			break
		}
		snapshot.state = worktreeDirty
	}
	return snapshot, nil
}

func gitOutput(ctx context.Context, cwd string, args ...string) (string, error) {
	cmdArgs := append([]string{"-C", cwd}, args...)
	output, err := exec.CommandContext(ctx, "git", cmdArgs...).Output()
	return string(output), err
}

func isConflictStatus(status string) bool {
	if len(status) < 2 {
		return false
	}
	switch status[:2] {
	case "AA", "AU", "DD", "DU", "UA", "UD", "UU":
		return true
	default:
		return false
	}
}

func (s worktreeSnapshot) visible() bool {
	return strings.TrimSpace(s.name) != ""
}

func worktreeRefStyle(state worktreeState) lipgloss.Style {
	style := lipgloss.NewStyle().Background(colorManager.LipglossColor(colorWorktreeBackground))
	switch state {
	case worktreeDirty:
		return style.Foreground(colorManager.LipglossColor(colorWorktreeDirty))
	case worktreeConflict:
		return style.Foreground(colorManager.LipglossColor(colorWorktreeConflict))
	default:
		return style.Foreground(colorManager.LipglossColor(colorWorktreeClean))
	}
}

// renderWorktreeChip renders only the metadata chip. The caller decides where
// it belongs so the status row can keep it inline with token information.
func (m appModel) renderWorktreeChip(width int) string {
	if width <= 0 || !m.worktree.visible() {
		return ""
	}
	name := strings.Join(strings.Fields(sanitizeTerminalText(m.worktree.name)), " ")
	ref := strings.Join(strings.Fields(sanitizeTerminalText(m.worktree.ref)), " ")
	if name == "" {
		return ""
	}

	label := name
	if ref != "" {
		label += "  " + ref
	}
	labelBudget := maxInt(1, width-worktreeChipStyle.GetHorizontalPadding())
	label = truncateStyledCellLine(label, labelBudget)
	separator := ""
	if ref != "" && strings.Contains(label, "  ") {
		parts := strings.SplitN(label, "  ", 2)
		name = parts[0]
		ref = parts[1]
		separator = "  "
	} else {
		name = label
		ref = ""
	}

	content := worktreeNameStyle.Render(name)
	if ref != "" {
		content += worktreeSeparatorStyle.Render(separator) + worktreeRefStyle(m.worktree.state).Render(ref)
	}
	chip := worktreeChipStyle.Render(content)
	return truncateStyledCellLine(chip, width)
}

func (m appModel) renderWorktreeLine(width int) string {
	if width <= 0 || !m.worktree.visible() || width < worktreeMinimumWidth {
		return strings.Repeat(" ", maxInt(0, width))
	}
	chip := m.renderWorktreeChip(width)
	chipWidth := terminalCellWidth(chip)
	if chipWidth >= width {
		return chip
	}
	return strings.Repeat(" ", width-chipWidth) + chip
}

const worktreeMinimumWidth = 48

// Inline worktree metadata is intentionally omitted before the status row
// becomes cramped; token information remains the visual priority.
const worktreeInlineMinimumWidth = 72
