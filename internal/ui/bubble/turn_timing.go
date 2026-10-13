package bubble

import (
	"fmt"
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
