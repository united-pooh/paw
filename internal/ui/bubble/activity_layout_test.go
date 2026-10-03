package bubble

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestComputeActivityGeometryHiddenAndBreakpoint(t *testing.T) {
	hidden := computeActivityGeometry(120, false, 0)
	if hidden.mode != activityLayoutHidden || hidden.workspaceWidth != 120 || hidden.activityWidth != 0 || hidden.separatorWidth != 0 {
		t.Fatalf("hidden = %+v", hidden)
	}

	docked := computeActivityGeometry(65, true, 0)
	if docked.mode != activityLayoutDocked || docked.workspaceWidth != 32 || docked.activityWidth != 32 || docked.separatorWidth != 1 {
		t.Fatalf("65 columns = %+v, want 32+1+32 dock", docked)
	}

	hiddenNarrow := computeActivityGeometry(64, true, 0)
	if hiddenNarrow.mode != activityLayoutHidden || hiddenNarrow.workspaceWidth != 64 || hiddenNarrow.activityWidth != 0 {
		t.Fatalf("64 columns = %+v, want input workspace", hiddenNarrow)
	}
}

func TestComputeActivityGeometryDefaultAndClamp(t *testing.T) {
	cases := []struct {
		width, requested, wantActivity int
	}{
		{100, 0, 36},
		{120, 0, 43},
		{200, 0, 52},
		{120, 12, 32},
		{120, 90, 52},
		{90, 52, 52},
	}
	for _, tc := range cases {
		got := computeActivityGeometry(tc.width, true, tc.requested)
		if got.activityWidth != tc.wantActivity {
			t.Fatalf("width=%d requested=%d activity=%d, want %d: %+v", tc.width, tc.requested, got.activityWidth, tc.wantActivity, got)
		}
		if got.workspaceWidth+got.separatorWidth+got.activityWidth != tc.width {
			t.Fatalf("geometry does not fill width: %+v", got)
		}
	}
}

func TestJoinActivityColumnsKeepsExactCellGeometry(t *testing.T) {
	left := "中文  \nleft  "
	right := "👩‍💻 \nright"
	got := joinActivityColumns(left, right, 6, 5, 2, "│")
	lines := strings.Split(got, "\n")
	if len(lines) != 2 {
		t.Fatalf("height=%d, want 2", len(lines))
	}
	for row, line := range lines {
		if terminalCellWidth(line) != 12 {
			t.Fatalf("row=%d width=%d: %q", row, terminalCellWidth(line), ansi.Strip(line))
		}
		if !strings.Contains(ansi.Strip(line), "│") {
			t.Fatalf("row=%d missing separator: %q", row, ansi.Strip(line))
		}
	}
}

func TestRenderSplitHairlineUsesJointAtWorkspaceBoundary(t *testing.T) {
	line := renderSplitHairline("main", "Activity", 52, 32, "┬", "")
	if terminalCellWidth(line) != 85 {
		t.Fatalf("width=%d, want 85: %q", terminalCellWidth(line), ansi.Strip(line))
	}
	joint := ansi.Strip(cutStyledCellsExact(line, 52, 53))
	if joint != "┬" {
		t.Fatalf("joint=%q, want ┬: %q", joint, ansi.Strip(line))
	}
}
