package bubble

import "testing"

func TestComputeActivityGeometryHiddenAndBreakpoint(t *testing.T) {
	hidden := computeActivityGeometry(120, false, 0)
	if hidden.mode != activityLayoutHidden || hidden.workspaceWidth != 120 || hidden.activityWidth != 0 || hidden.separatorWidth != 0 {
		t.Fatalf("hidden = %+v", hidden)
	}

	docked := computeActivityGeometry(85, true, 0)
	if docked.mode != activityLayoutDocked || docked.workspaceWidth != 52 || docked.activityWidth != 32 || docked.separatorWidth != 1 {
		t.Fatalf("85 columns = %+v, want 52+1+32 dock", docked)
	}

	fullscreen := computeActivityGeometry(84, true, 0)
	if fullscreen.mode != activityLayoutFullscreen || fullscreen.activityWidth != 84 || fullscreen.separatorWidth != 0 {
		t.Fatalf("84 columns = %+v, want fullscreen", fullscreen)
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
		{90, 52, 37},
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

func TestResizeActivityWidthUsesFourColumnStep(t *testing.T) {
	if got := resizeActivityWidth(40, -1); got != 36 {
		t.Fatalf("shrink = %d, want 36", got)
	}
	if got := resizeActivityWidth(40, 1); got != 44 {
		t.Fatalf("grow = %d, want 44", got)
	}
	if got := resizeActivityWidth(32, -1); got != 32 {
		t.Fatalf("min clamp = %d, want 32", got)
	}
	if got := resizeActivityWidth(52, 1); got != 52 {
		t.Fatalf("max clamp = %d, want 52", got)
	}
}
