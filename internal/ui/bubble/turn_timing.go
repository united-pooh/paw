package bubble

import (
	"fmt"

	"paw/internal/session"
)

func formatTurnSeconds(seconds int64) string {
	if seconds < 0 {
		seconds = 0
	}
	if seconds < 60 {
		return fmt.Sprintf("%ds", seconds)
	}
	if seconds < 60*60 {
		return fmt.Sprintf("%dm%02ds", seconds/60, seconds%60)
	}
	return fmt.Sprintf("%dh%02dm%02ds", seconds/(60*60), (seconds%(60*60))/60, seconds%60)
}

func formatTurnDuration(durationMS int64) string {
	if durationMS < 0 {
		durationMS = 0
	}
	if durationMS < 1000 {
		return fmt.Sprintf("%dms", durationMS)
	}
	return formatTurnSeconds(durationMS / 1000)
}

// formatTurnFooter is intentionally label-free because it is a transcript
// decoration, not assistant content: "1m35s  07:47:47 AM".
func formatTurnFooter(metadata session.TurnMetadata) string {
	if metadata.ResponseAt == nil || metadata.ResponseAt.IsZero() {
		return formatTurnDuration(metadata.DurationMS)
	}
	return formatTurnDuration(metadata.DurationMS) + "  " + metadata.ResponseAt.Local().Format("03:04:05 PM")
}
