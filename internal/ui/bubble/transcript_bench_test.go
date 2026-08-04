package bubble

import (
	"fmt"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
)

// benchTranscriptEntries 构造一条固定宽度下的真实 transcript：交替 user /
// assistant（带 markdown 格式文本）与 tool 条目。
func benchTranscriptEntries(count int) []transcriptEntry {
	entries := make([]transcriptEntry, 0, count)
	for i := 0; i < count; i++ {
		switch i % 3 {
		case 0:
			entries = append(entries, transcriptEntry{
				kind:  entryUser,
				title: "you",
				body:  fmt.Sprintf("用户消息 %d：\n\n- 列表项一\n- 列表项二\n\n```go\nfunc main() {}\n```", i),
			})
		case 1:
			entries = append(entries, transcriptEntry{
				kind:       entryAssistant,
				title:      "assistant",
				body:       fmt.Sprintf("**回答 %d**：这里是一段 markdown 正文，包含 `code`、[链接](https://example.com) 以及 *强调*。\n\n1. 第一点\n2. 第二点", i),
				renderMode: transcriptRenderFormatted,
			})
		default:
			entries = append(entries, transcriptEntry{
				kind:       entryTool,
				title:      "tool",
				body:       "✓ Read  internal/file.go  12ms",
				toolName:   "Read",
				toolTarget: "internal/file.go",
				toolStatus: "ok",
			})
		}
	}
	return entries
}

// BenchmarkTranscriptRenderAt 度量纯 transcript 内容渲染（含逐条 ANSI 样式生成）。
// 这是 View 内部最重的区域，对应 docs P0：transcript View/render benchmark。
func BenchmarkTranscriptRenderAt(b *testing.B) {
	now := time.Now()
	for _, count := range []int{100, 1_000, 5_000} {
		for _, width := range []int{80, 120, 200} {
			b.Run(fmt.Sprintf("n=%d/w=%d", count, width), func(b *testing.B) {
				entries := benchTranscriptEntries(count)
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					renderTranscriptAt(entries, width, true, now)
				}
			})
		}
	}
}

// BenchmarkTranscriptRegionCacheHit 度量条目级缓存命中时的内容重建成本：
// 缓存已填充，每次重建只做 key 比较与拼接，不做重复 markdown/ANSI 渲染。
func BenchmarkTranscriptRegionCacheHit(b *testing.B) {
	entries := benchTranscriptEntries(1_000)
	model := appModel{
		transcript:   entries,
		viewport:     benchViewport(120, 30),
		width:        120,
		height:       30,
		showThinking: true,
	}
	// 预填充逐条渲染缓存。
	_ = model.renderTranscriptContent()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		model.renderTranscriptContent()
	}
}

func benchViewport(width, height int) viewport.Model {
	vp := viewport.New(width, height)
	return vp
}
