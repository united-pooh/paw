package goal

import "time"

type EventType string

const (
	EventStarted         EventType = "goal.started"
	EventResumed         EventType = "goal.resumed"
	EventTaskStarted     EventType = "goal.task.started"
	EventTurnDone        EventType = "goal.turn.completed"
	EventContinued       EventType = "goal.continued"
	EventCompacted       EventType = "goal.compacted"
	EventPaused          EventType = "goal.paused"
	EventBlocked         EventType = "goal.blocked"
	EventCompleted       EventType = "goal.completed"
	EventFailed          EventType = "goal.failed"
	EventCancelled       EventType = "goal.cancelled"
	EventInputReceived   EventType = "goal.input.received"
	EventCheckpointSaved EventType = "goal.checkpoint.saved"
	EventEvidenceAdded   EventType = "goal.evidence.added"
)

type Event struct {
	Type       EventType
	GoalID     GoalID
	TaskID     string
	TurnNumber int
	Decision   Decision
	Snapshot   GoalSnapshot
	Err        error
	At         time.Time
}

type EventSink func(Event)
