package bubble

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestWrapCompactFillsTrailingSpaceWithWideWord(t *testing.T) {
	// 行尾剩余 2 列时（丢弃词前空格），"宽度"（4 列）应拆出"宽"（2 列）填满。
	got := wrapCompact("abc 宽度", 5)
	lines := strings.Split(got, "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 lines, got %q", got)
	}
	if lines[0] != "abc宽" {
		t.Fatalf("first line = %q, want %q", lines[0], "abc宽")
	}
	if lines[1] != "度" {
		t.Fatalf("second line = %q, want %q", lines[1], "度")
	}
}

func TestWrapCompactKeepsSpaceWhenItFits(t *testing.T) {
	// 行尾剩余 3 列（空格 1 + 宽字符 2）时保留空格："abc 宽"。
	got := wrapCompact("abc 宽度", 6)
	lines := strings.Split(got, "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 lines, got %q", got)
	}
	if lines[0] != "abc 宽" {
		t.Fatalf("first line = %q, want %q", lines[0], "abc 宽")
	}
	if lines[1] != "度" {
		t.Fatalf("second line = %q, want %q", lines[1], "度")
	}
}

func TestWrapCompactKeepsAsciiWordIntact(t *testing.T) {
	// 英文单词放不下行尾空间时保持完整下移（不拆词）。
	got := wrapCompact("ab viewport", 8)
	lines := strings.Split(got, "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 lines, got %q", got)
	}
	if lines[0] != "ab" {
		t.Fatalf("first line = %q, want %q", lines[0], "ab")
	}
	if lines[1] != "viewport" {
		t.Fatalf("second line = %q, want %q", lines[1], "viewport")
	}
}

func TestWrapCompactPreservesANSIWithoutSplitting(t *testing.T) {
	// 带样式（ANSI）的词不拆词，避免切断样式序列。
	styled := ansi.SetHyperlink("https://example.com") + "宽度" + ansi.ResetHyperlink()
	got := wrapCompact("abc "+styled, 5)
	lines := strings.Split(got, "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 lines, got %q", got)
	}
	if ansi.Strip(lines[1]) != "宽度" {
		t.Fatalf("second line visible = %q, want %q", ansi.Strip(lines[1]), "宽度")
	}
}

func TestWrapCompactEveryLineFitsLimit(t *testing.T) {
	texts := []string{
		"这只是布局设计上的留白，不是 bug——之前的调查确认所有消息行都恰好填满 viewport 宽度",
		"word wrap test: the quick brown fox jumps over the lazy dog width",
		"中文测试：这段文字没有空格，应该逐字填满每一行直到结束为止不再留白",
		"混合 text 中文 english 混排 viewport 宽度 word 测试",
	}
	for _, limit := range []int{10, 20, 30, 40, 60, 80} {
		for _, text := range texts {
			got := wrapCompact(text, limit)
			for i, line := range strings.Split(got, "\n") {
				if w := ansi.StringWidth(ansi.Strip(line)); w > limit {
					t.Fatalf("limit=%d text=%q line %d width=%d > %d: %q", limit, text, i, w, limit, line)
				}
			}
		}
	}
}

func TestWrapCompactFillsUserScenario(t *testing.T) {
	// 复现用户场景：窗口 84 列 → 正文 wrap 宽度 80。
	// 修复前第一行 "……viewport  "（77 列正文）后 "宽度" 换行；
	// 修复后 "viewport 宽" 填满整行（80 列），只把 "度" 留给下一行。
	text := "这只是布局设计上的留白，不是 bug——之前的调查确认所有消息行都恰好填满 viewport 宽度"
	got := wrapCompact(text, 80)
	lines := strings.Split(got, "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 lines, got %d: %q", len(lines), got)
	}
	if w := ansi.StringWidth(lines[0]); w != 80 {
		t.Fatalf("first line width = %d, want 80: %q", w, lines[0])
	}
	if !strings.HasSuffix(lines[0], "宽") {
		t.Fatalf("first line should end with 拆出的 宽, got %q", lines[0])
	}
	if !strings.HasPrefix(lines[1], "度") {
		t.Fatalf("second line should start with 度, got %q", lines[1])
	}
}
