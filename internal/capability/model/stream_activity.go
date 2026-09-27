package model

import "time"

// StreamActivity contains only bytes observed in an incremental provider event.
// Completion snapshots and opaque reasoning are deliberately excluded.
type StreamActivity struct {
	At            time.Time
	Text          string
	Reasoning     string
	ToolArguments string
}

func withStreamActivity(event StreamEvent, text, reasoning, arguments string) StreamEvent {
	if text == "" && reasoning == "" && arguments == "" {
		return event
	}
	at := time.Now()
	event.ReceivedAt = at
	event.Activity = &StreamActivity{At: at, Text: text, Reasoning: reasoning, ToolArguments: arguments}
	return event
}
