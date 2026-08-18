package bubble

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

var wheelFilterBenchmarkSink tea.Msg

func BenchmarkProgramEventFilterQueuedWheelBurst(b *testing.B) {
	model := wheelFilterTestModel()
	var current tea.Model = model
	up := wheelFilterMouse(tea.MouseButtonWheelUp)
	down := wheelFilterMouse(tea.MouseButtonWheelDown)
	var upMsg tea.Msg = up
	var downMsg tea.Msg = down
	for _, burst := range []int{3_000, 10_000} {
		b.Run(benchmarkBurstName(burst), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				filter := programEventFilter{}
				wheelFilterBenchmarkSink = filter.Filter(current, upMsg)
				for range burst {
					wheelFilterBenchmarkSink = filter.Filter(current, upMsg)
				}
				wheelFilterBenchmarkSink = filter.Filter(current, downMsg)
			}
			b.ReportMetric(float64(burst+2), "raw-events/op")
			b.ReportMetric(2, "batch-updates/op")
		})
	}
}

func BenchmarkProgramEventFilterMergedWheel(b *testing.B) {
	model := wheelFilterTestModel()
	var current tea.Model = model
	var up tea.Msg = wheelFilterMouse(tea.MouseButtonWheelUp)
	filter := programEventFilter{}
	filter.Filter(current, up)
	b.ReportAllocs()
	for b.Loop() {
		wheelFilterBenchmarkSink = filter.Filter(current, up)
	}
}

func benchmarkBurstName(burst int) string {
	if burst == 3_000 {
		return "3k"
	}
	return "10k"
}
