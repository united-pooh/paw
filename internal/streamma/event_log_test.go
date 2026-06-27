package streamma

import "testing"

func TestEventLogAppendOrdersAndCopiesEvents(t *testing.T) {
	log := NewEventLog("run-log")

	first := log.Append(Event{Type: EventProblem, Problem: &ProblemPacket{Text: "p"}})
	second := log.Append(Event{Type: EventStepCommitted, ProducerAgentID: "a", Step: &StepPacket{StepID: "a:1"}})

	if first.Seq != 1 || second.Seq != 2 {
		t.Fatalf("unexpected sequences: %d %d", first.Seq, second.Seq)
	}
	if first.EventID != "run-log-000001" || second.EventID != "run-log-000002" {
		t.Fatalf("unexpected event ids: %q %q", first.EventID, second.EventID)
	}

	snapshot := log.Snapshot()
	snapshot[1].Step.StepID = "mutated"

	again := log.Snapshot()
	if again[1].Step.StepID != "a:1" {
		t.Fatalf("snapshot mutation leaked into event log: %q", again[1].Step.StepID)
	}
}

func TestEventLogReplayRetainsFailureEvent(t *testing.T) {
	log := NewEventLog("run-fail-log")
	log.Append(Event{Type: EventStepCommitted, Step: &StepPacket{StepID: "a:1"}})
	failed := log.Append(Event{Type: EventRunFailed, Error: "parser A: too large"})

	replayed := log.Replay()
	if replayed.Status != RunFailed {
		t.Fatalf("replay status = %s, want failed", replayed.Status)
	}
	if replayed.FailureEventID != failed.EventID || replayed.FailureSequence != failed.Seq {
		t.Fatalf("failure point mismatch: %#v vs %#v", replayed, failed)
	}
}
