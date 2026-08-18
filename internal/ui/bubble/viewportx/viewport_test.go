package viewportx

import (
	"fmt"
	"strings"
	"testing"
)

func TestReplaceLinesKeepsStablePrefixWidth(t *testing.T) {
	m := New(5, 2)
	m.SetLines([]string{"abcdefghij", "old suffix"})
	m.ReplaceLines(1, []string{"x"})
	m.ScrollRight(5)

	if got := strings.TrimRight(m.View(), " \n"); got != "fghij" {
		t.Fatalf("view after suffix replacement = %q, want stable prefix to remain horizontally scrollable", got)
	}
}

func TestReplaceLinesClampsHorizontalOffsetAfterLongestSuffixShrinks(t *testing.T) {
	m := New(8, 2)
	m.SetLines([]string{"short", "0123456789abcdef"})
	m.ScrollRight(8)
	m.ReplaceLines(1, []string{"tiny"})

	view := m.View()
	if !strings.Contains(view, "short") || !strings.Contains(view, "tiny") {
		t.Fatalf("view after longest suffix shrank = %q, want unshifted replacement content", view)
	}
}

func TestReplaceLinesSupportsGrowingShrinkingAndClearingSuffix(t *testing.T) {
	m := New(20, 5)
	m.SetLines([]string{"prefix", "old"})

	m.ReplaceLines(1, []string{"one", "two", "three"})
	if got := m.TotalLineCount(); got != 4 {
		t.Fatalf("line count after growth = %d, want 4", got)
	}
	for _, want := range []string{"prefix", "one", "two", "three"} {
		if !strings.Contains(m.View(), want) {
			t.Fatalf("view after growth = %q, missing %q", m.View(), want)
		}
	}

	m.ReplaceLines(2, []string{"last"})
	if got := m.TotalLineCount(); got != 3 {
		t.Fatalf("line count after shrink = %d, want 3", got)
	}
	if view := m.View(); !strings.Contains(view, "last") || strings.Contains(view, "three") {
		t.Fatalf("view after shrink = %q", view)
	}

	m.ReplaceLines(1, nil)
	if got := m.TotalLineCount(); got != 1 {
		t.Fatalf("line count after clear = %d, want 1", got)
	}
	if view := m.View(); !strings.Contains(view, "prefix") || strings.Contains(view, "last") {
		t.Fatalf("view after clear = %q", view)
	}
}

func TestReplaceLinesVisitsOnlyReplacementSuffix(t *testing.T) {
	lines := make([]string, 10_000)
	for index := range lines {
		lines[index] = "stable history"
	}
	m := New(20, 5)
	m.SetLines(lines)

	m.ReplaceLines(len(lines)-2, []string{"updated", ""})

	if got := m.replaceWidthVisits; got != 2 {
		t.Fatalf("suffix replacement measured %d line widths, want 2", got)
	}
}

func TestReplaceLinesSupportsFullReplacement(t *testing.T) {
	m := New(20, 5)
	m.SetLines([]string{"old prefix", "old suffix"})

	m.ReplaceLines(0, []string{"new content"})

	if got := m.TotalLineCount(); got != 1 {
		t.Fatalf("line count after full replacement = %d, want 1", got)
	}
	if view := m.View(); !strings.Contains(view, "new content") || strings.Contains(view, "old") {
		t.Fatalf("view after full replacement = %q", view)
	}
}

func BenchmarkViewportReplaceTail(b *testing.B) {
	for _, count := range []int{1_000, 10_000} {
		b.Run(fmt.Sprintf("n=%d", count), func(b *testing.B) {
			lines := make([]string, count)
			for i := range lines {
				lines[i] = fmt.Sprintf("history line %05d", i)
			}
			m := New(120, 30)
			m.SetLines(lines)
			tail := []string{"updated tail", ""}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				m.ReplaceLines(count-2, tail)
			}
		})
	}
}
