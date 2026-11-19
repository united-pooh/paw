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

// TestWrapCompactAnnotatedMarksAsciiWordWrapAsSpace 验证英文单词整体换行
// （原位置空格被丢弃）标记为 SPACE（U+200B），复制时应还原为一个空格。
func TestWrapCompactAnnotatedMarksAsciiWordWrapAsSpace(t *testing.T) {
	got := wrapCompactAnnotated("ab viewport", 8)
	lines := strings.Split(got, "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 lines, got %q", got)
	}
	if lines[0] != "ab" {
		t.Fatalf("first line = %q, want %q", lines[0], "ab")
	}
	if !strings.HasPrefix(lines[1], string(softBreakSpace)) {
		t.Fatalf("second line = %q, want prefix %U", lines[1], softBreakSpace)
	}
	if rest := strings.TrimPrefix(lines[1], string(softBreakSpace)); rest != "viewport" {
		t.Fatalf("second line content = %q, want %q", rest, "viewport")
	}
}

// TestWrapCompactAnnotatedMarksWideWordSplitAsGlue 验证中文长词被迫从中间
// 拆开（splitWord 真正执行拆词）标记为 GLUE（U+200C），复制时不应插入分隔符。
func TestWrapCompactAnnotatedMarksWideWordSplitAsGlue(t *testing.T) {
	got := wrapCompactAnnotated("abc 宽度", 5)
	lines := strings.Split(got, "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 lines, got %q", got)
	}
	if lines[0] != "abc宽" {
		t.Fatalf("first line = %q, want %q", lines[0], "abc宽")
	}
	if !strings.HasPrefix(lines[1], string(softBreakGlue)) {
		t.Fatalf("second line = %q, want prefix %U", lines[1], softBreakGlue)
	}
	if rest := strings.TrimPrefix(lines[1], string(softBreakGlue)); rest != "度" {
		t.Fatalf("second line content = %q, want %q", rest, "度")
	}
}

// TestWrapCompactAnnotatedKeepsHardNewlineUnmarked 验证原文真实换行不加任何标记。
func TestWrapCompactAnnotatedKeepsHardNewlineUnmarked(t *testing.T) {
	got := wrapCompactAnnotated("hello\nworld", 20)
	lines := strings.Split(got, "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 lines, got %q", got)
	}
	if lines[0] != "hello" || lines[1] != "world" {
		t.Fatalf("lines = %q, want [hello world]", lines)
	}
}

// TestWrapCompactAnnotatedMatchesPlainOutputOnceStripped 验证去掉标记字符后
// annotated 输出与未加标记的 wrapCompact 输出完全一致（标记只是附加信息，
// 不改变实际换行位置/内容）。
func TestWrapCompactAnnotatedMatchesPlainOutputOnceStripped(t *testing.T) {
	texts := []string{
		"ab viewport",
		"abc 宽度",
		"这只是布局设计上的留白，不是 bug——之前的调查确认所有消息行都恰好填满 viewport 宽度",
		"混合 text 中文 english 混排 viewport 宽度 word 测试",
	}
	for _, limit := range []int{5, 8, 20, 40} {
		for _, text := range texts {
			plain := wrapCompact(text, limit)
			annotated := wrapCompactAnnotated(text, limit)
			stripped := strings.ReplaceAll(annotated, string(softBreakSpace), "")
			stripped = strings.ReplaceAll(stripped, string(softBreakGlue), "")
			if stripped != plain {
				t.Fatalf("limit=%d text=%q: stripped annotated = %q, want %q", limit, text, stripped, plain)
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
