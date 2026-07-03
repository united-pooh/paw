package loop

import (
	"codex-agent-go/internal/message"
	"codex-agent-go/internal/model"
)

// ApplyHistoryProjection builds a []message.Message from a base snapshot
// (may be nil) and incremental events.
//
// Rules:
//   - Starts from base.History when base is non-nil, otherwise starts empty.
//   - EventKindHistoryMessage appends the event's Message to the slice.
//   - EventKindHistoryReset resets the slice to nil (empty history).
//   - All other event kinds are ignored.
func ApplyHistoryProjection(base *SessionSnapshot, events []SessionEvent) []message.Message {
	var history []message.Message
	if base != nil && len(base.History) > 0 {
		history = make([]message.Message, len(base.History))
		copy(history, base.History)
	}

	for _, ev := range events {
		switch ev.Kind {
		case EventKindHistoryMessage:
			if ev.Message != nil {
				history = append(history, *ev.Message)
			}
		case EventKindHistoryReset:
			history = nil
		}
	}

	return history
}

// UsageState is the materialised usage projection for a session.
type UsageState struct {
	Usage             model.Usage
	UsageKnown        bool
	SessionUsage      model.Usage
	SessionUsageKnown bool
}

// ApplyUsageProjection builds a UsageState from a base snapshot (may be nil)
// and incremental events.
//
// Rules:
//   - Starts from base usage when base is non-nil, otherwise starts zeroed.
//   - EventKindUsageUpdate with IsSession==true updates SessionUsage/SessionUsageKnown.
//   - EventKindUsageUpdate with IsSession==false updates Usage/UsageKnown.
//   - All other event kinds are ignored.
func ApplyUsageProjection(base *SessionSnapshot, events []SessionEvent) UsageState {
	var state UsageState
	if base != nil {
		state = UsageState{
			Usage:             base.Usage,
			UsageKnown:        base.UsageKnown,
			SessionUsage:      base.SessionUsage,
			SessionUsageKnown: base.SessionUsageKnown,
		}
	}

	for _, ev := range events {
		if ev.Kind != EventKindUsageUpdate || ev.Usage == nil {
			continue
		}
		if ev.Usage.IsSession {
			state.SessionUsage = ev.Usage.Usage
			state.SessionUsageKnown = true
		} else {
			state.Usage = ev.Usage.Usage
			state.UsageKnown = true
		}
	}

	return state
}
