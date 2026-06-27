package streamma

import "testing"

func TestBrokerBroadcastFanoutEOFAndDrain(t *testing.T) {
	graph, err := compileGraph(GraphSpec{
		RunID:  "run-broker",
		Agents: []AgentSpec{{ID: "a"}, {ID: "b"}, {ID: "c"}},
		Edges:  []EdgeSpec{{From: "a", To: "b"}, {From: "a", To: "c"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	broker := NewBroker(graph)

	problem := Event{RunID: graph.runID, Type: EventProblem, Problem: &ProblemPacket{Text: "p"}}
	deliveries := broker.BroadcastProblem(problem)
	if len(deliveries) != 1 || deliveries[0].Event.TargetAgentID != "a" {
		t.Fatalf("source broadcast = %#v", deliveries)
	}
	if broker.QueueLen("a") != 1 {
		t.Fatalf("source queue length = %d, want 1", broker.QueueLen("a"))
	}
	if _, ok := broker.Dequeue("a"); !ok {
		t.Fatal("expected source problem delivery")
	}

	stepEvent := Event{RunID: graph.runID, Type: EventStepCommitted, ProducerAgentID: "a", Step: &StepPacket{AgentID: "a", StepID: "a:1"}}
	fanout := broker.FanoutStep(stepEvent)
	if len(fanout) != 2 {
		t.Fatalf("fanout count = %d, want 2", len(fanout))
	}
	if broker.QueueLen("b") != 1 || broker.QueueLen("c") != 1 {
		t.Fatalf("fanout queue lengths b=%d c=%d", broker.QueueLen("b"), broker.QueueLen("c"))
	}

	eofs := broker.PropagateEOF(graph.runID, "a", 1, "done")
	if len(eofs) != 2 {
		t.Fatalf("eof fanout count = %d, want 2", len(eofs))
	}
	if broker.QueueLen("b") != 2 || broker.QueueLen("c") != 2 {
		t.Fatalf("eof queue lengths b=%d c=%d", broker.QueueLen("b"), broker.QueueLen("c"))
	}
}
