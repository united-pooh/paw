package bubble

import (
	"fmt"
	"github.com/charmbracelet/lipgloss"
	"strconv"
	"strings"
	"time"
)

func (m *appModel) applyCursorAnimation() {
	if m.cursorFrameAt.IsZero() {
		m.cursorFrameAt = time.Now()
	}
	intensity := cursorIntensityAt(cursorCycleOffset(m.cursorFrameAt))
	m.input.Cursor.Style = lipgloss.NewStyle().Foreground(lipgloss.Color(cursorColor(intensity, m.isTerminalInputActive())))
	m.input.Cursor.Blink = intensity <= cursorHiddenThreshold
}

func cursorCycleOffset(at time.Time) time.Duration {
	return time.Duration(at.UnixNano()) % cursorCycleDuration
}

func cursorIntensityAt(offset time.Duration) float64 {
	phase := float64(offset) / float64(cursorCycleDuration)
	switch {
	case phase < 1.0/6.0:
		return 1 - easeInOutSine(phase/(1.0/6.0))
	case phase < 0.5:
		return 0
	case phase < 2.0/3.0:
		return easeInOutSine((phase - 0.5) / (1.0 / 6.0))
	case phase < 5.0/6.0:
		return 1 - easeInOutSine((phase-2.0/3.0)/(1.0/6.0))
	default:
		return 0
	}
}

func cursorColor(intensity float64, terminal bool) string {
	dim := normalCursorDim
	bright := normalCursorBright
	if terminal {
		dim = terminalCursorDim
		bright = terminalCursorBright
	}
	return interpolateHexColor(dim, bright, clamp01(intensity))
}

func interpolateHexColor(from, to string, amount float64) string {
	fr, fg, fb := parseHexColor(from)
	tr, tg, tb := parseHexColor(to)
	return fmt.Sprintf(
		"#%02x%02x%02x",
		lerpInt(fr, tr, amount),
		lerpInt(fg, tg, amount),
		lerpInt(fb, tb, amount),
	)
}

func parseHexColor(color string) (int, int, int) {
	color = strings.TrimPrefix(color, "#")
	if len(color) != 6 {
		return 0, 0, 0
	}
	r, err := strconv.ParseInt(color[0:2], 16, 0)
	if err != nil {
		return 0, 0, 0
	}
	g, err := strconv.ParseInt(color[2:4], 16, 0)
	if err != nil {
		return 0, 0, 0
	}
	b, err := strconv.ParseInt(color[4:6], 16, 0)
	if err != nil {
		return 0, 0, 0
	}
	return int(r), int(g), int(b)
}

func lerpInt(from, to int, amount float64) int {
	return from + int(float64(to-from)*amount+0.5)
}

func clamp01(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}

func easeInOutSine(t float64) float64 {
	t = clamp01(t)
	return t * t * (3 - 2*t)
}
