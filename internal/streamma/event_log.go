package streamma

import (
	"fmt"
	"sync"
	"time"
)

type EventLog struct {
	mu     sync.Mutex
	runID  string
	events []Event
}

func NewEventLog(runID string) *EventLog {
	return &EventLog{runID: runID}
}

func (l *EventLog) Append(event Event) Event {
	l.mu.Lock()
	defer l.mu.Unlock()

	if event.RunID == "" {
		event.RunID = l.runID
	}
	if event.Seq == 0 {
		event.Seq = len(l.events) + 1
	}
	if event.EventID == "" {
		event.EventID = fmt.Sprintf("%s-%06d", event.RunID, event.Seq)
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}
	copied := cloneEvent(event)
	l.events = append(l.events, copied)
	return copied
}

func (l *EventLog) Snapshot() []Event {
	l.mu.Lock()
	defer l.mu.Unlock()

	out := make([]Event, len(l.events))
	for i, event := range l.events {
		out[i] = cloneEvent(event)
	}
	return out
}

func (l *EventLog) Replay() ReplaySummary {
	return Replay(l.Snapshot())
}

func cloneEvent(event Event) Event {
	if event.Trace != nil {
		trace := make(map[string]string, len(event.Trace))
		for k, v := range event.Trace {
			trace[k] = v
		}
		event.Trace = trace
	}
	if event.Problem != nil {
		problem := *event.Problem
		event.Problem = &problem
	}
	if event.Step != nil {
		step := cloneStepPacket(*event.Step)
		event.Step = &step
	}
	if event.Control != nil {
		control := *event.Control
		event.Control = &control
	}
	if event.Final != nil {
		final := cloneFinalAnswerPacket(*event.Final)
		event.Final = &final
	}
	return event
}

func cloneStepPacket(step StepPacket) StepPacket {
	if step.Dependencies.InputEvents != nil {
		step.Dependencies.InputEvents = append([]string(nil), step.Dependencies.InputEvents...)
	}
	return step
}

func cloneFinalAnswerPacket(final FinalAnswerPacket) FinalAnswerPacket {
	if final.Support.UsedSteps != nil {
		final.Support.UsedSteps = append([]string(nil), final.Support.UsedSteps...)
	}
	return final
}
