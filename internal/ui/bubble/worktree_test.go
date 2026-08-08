package bubble

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestReadWorktreeStatusHandlesNonGitDirectory(t *testing.T) {
	dir := t.TempDir()
	snapshot, err := readWorktreeStatus(context.Background(), dir)
	if err != nil {
		t.Fatalf("readWorktreeStatus() error = %v", err)
	}
	if snapshot.isGit {
		t.Fatalf("non-Git snapshot = %#v, want isGit=false", snapshot)
	}
	if snapshot.name != filepath.Base(dir) || snapshot.ref != "" {
		t.Fatalf("non-Git snapshot = %#v", snapshot)
	}
}

func TestReadWorktreeStatusDetectsCleanAndDirty(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init", "-q")
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "Bubble Test")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("ready\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", "README.md")
	runGit(t, dir, "commit", "-qm", "initial")

	clean, err := readWorktreeStatus(context.Background(), dir)
	if err != nil {
		t.Fatalf("clean read error = %v", err)
	}
	if !clean.isGit || clean.state != worktreeClean || clean.ref == "" {
		t.Fatalf("clean snapshot = %#v", clean)
	}

	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dirty, err := readWorktreeStatus(context.Background(), dir)
	if err != nil {
		t.Fatalf("dirty read error = %v", err)
	}
	if dirty.state != worktreeDirty {
		t.Fatalf("dirty snapshot = %#v, want dirty", dirty)
	}
}

func TestConflictStatusRecognition(t *testing.T) {
	for _, status := range []string{"AA", "AU", "DD", "DU", "UA", "UD", "UU"} {
		if !isConflictStatus(status) {
			t.Errorf("isConflictStatus(%q) = false", status)
		}
	}
	if isConflictStatus(" M") || isConflictStatus("??") {
		t.Fatal("ordinary dirty statuses must not be classified as conflicts")
	}
}

func TestWorktreeLabelUsesOnlyBranchColorForGitState(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.worktree = worktreeSnapshot{name: "paw", ref: "dev", state: worktreeDirty, isGit: true}
	rendered := model.renderWorktreeLine(80)
	plain := ansi.Strip(rendered)
	if !strings.Contains(plain, "paw  dev") {
		t.Fatalf("worktree label = %q, want name and branch", plain)
	}
	for _, forbidden := range []string{"●", "⌂", "⎇"} {
		if strings.Contains(plain, forbidden) {
			t.Fatalf("worktree label = %q contains forbidden Git marker %q", plain, forbidden)
		}
	}
	if got := worktreeRefStyle(worktreeDirty).GetForeground(); got != colorManager.LipglossColor(colorWorktreeDirty) {
		t.Fatalf("dirty branch color = %q, want %q", got, colorManager.Hex(colorWorktreeDirty))
	}
	if got := worktreeSeparatorStyle.GetBackground(); got != colorManager.LipglossColor(colorWorktreeBackground) {
		t.Fatalf("worktree separator background = %q, want %q", got, colorManager.Hex(colorWorktreeBackground))
	}

	model.worktree = worktreeSnapshot{name: "paw", ref: "abc1234", state: worktreeClean, isGit: true, detached: true}
	plain = ansi.Strip(model.renderWorktreeLine(80))
	if !strings.Contains(plain, "paw  abc1234") {
		t.Fatalf("detached label = %q", plain)
	}
}

func TestWorktreeLabelHidesAtNarrowWidth(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.worktree = worktreeSnapshot{name: "paw", ref: "dev", state: worktreeClean, isGit: true}
	if got := ansi.Strip(model.renderWorktreeLine(worktreeMinimumWidth - 1)); strings.TrimSpace(got) != "" {
		t.Fatalf("narrow worktree label = %q, want hidden", got)
	}
}

func TestWorktreeRefreshKeepsLastSnapshotOnError(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.worktree = worktreeSnapshot{name: "paw", ref: "dev", state: worktreeClean, isGit: true}
	next, cmd := model.Update(worktreeRefreshMsg{err: errors.New("git unavailable")})
	model = next.(appModel)
	if model.worktree.ref != "dev" || model.worktree.state != worktreeClean {
		t.Fatalf("snapshot changed on refresh error: %#v", model.worktree)
	}
	if cmd != nil {
		t.Fatal("refresh error scheduled an idle redraw tick that can disturb IME preedit")
	}
}

func TestWorktreeRefreshUsesInjectedReader(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.worktreeCWD = "/tmp/project"
	model.worktreeReader = func(_ context.Context, cwd string) (worktreeSnapshot, error) {
		if cwd != "/tmp/project" {
			t.Fatalf("reader cwd = %q", cwd)
		}
		return worktreeSnapshot{name: "project", ref: "feature", state: worktreeConflict, isGit: true}, nil
	}
	msg := worktreeRefreshCmd(model.ctx, model.worktreeCWD, model.worktreeReader)()
	refresh, ok := msg.(worktreeRefreshMsg)
	if !ok || refresh.err != nil {
		t.Fatalf("refresh message = %#v", msg)
	}
	next, _ := model.Update(refresh)
	model = next.(appModel)
	if model.worktree.ref != "feature" || model.worktree.state != worktreeConflict {
		t.Fatalf("injected snapshot = %#v", model.worktree)
	}
}

func TestNewModelStartsWithEmptyTranscript(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	if len(model.transcript) != 0 {
		t.Fatalf("startup transcript = %#v, want empty until the first real event", model.transcript)
	}
	model.ready = true
	model.width = 80
	model.height = 20
	model.relayout()
	view := ansi.Strip(model.View())
	for _, want := range []string{"アトリ高性能ですから!", "› Ask anything…"} {
		if !strings.Contains(view, want) {
			t.Fatalf("empty view = %q, missing %q", view, want)
		}
	}
	for _, unwanted := range []string{"SIGNAL / READY", "│ ›"} {
		if strings.Contains(view, unwanted) {
			t.Fatalf("empty view = %q, should not contain %q", view, unwanted)
		}
	}
	if strings.Contains(view, "Interactive mode is running") || strings.Contains(view, "Token Tracer dashboard") {
		t.Fatalf("empty view contains startup noise: %q", view)
	}
}

func TestStatusLinePlacesWorktreeAfterTokenInfo(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.worktree = worktreeSnapshot{name: "paw", ref: "dev", state: worktreeDirty, isGit: true}
	bottom := ansi.Strip(model.renderBottomDockLine(100))
	count := strings.Index(bottom, " / ")
	worktree := strings.Index(bottom, "paw  dev")
	if count < 0 || worktree < 0 || !(count < worktree) {
		t.Fatalf("bottom border order = %q", bottom)
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
}
