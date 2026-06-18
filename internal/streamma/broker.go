package streamma

type BrokerDelivery struct {
	Event Event
}

type Broker struct {
	graph       *compiledGraph
	queues      map[string][]BrokerDelivery
	receivedEOF map[string]map[string]bool
}

func NewBroker(graph *compiledGraph) *Broker {
	b := &Broker{
		graph:       graph,
		queues:      map[string][]BrokerDelivery{},
		receivedEOF: map[string]map[string]bool{},
	}
	for _, agentID := range graph.agentOrder {
		b.queues[agentID] = nil
		b.receivedEOF[agentID] = map[string]bool{}
	}
	return b
}

func (b *Broker) BroadcastProblem(event Event) []BrokerDelivery {
	var deliveries []BrokerDelivery
	for _, source := range b.graph.sources {
		delivered := cloneEvent(event)
		delivered.TargetAgentID = source
		delivery := BrokerDelivery{Event: delivered}
		b.enqueue(source, delivery)
		deliveries = append(deliveries, delivery)
	}
	return deliveries
}

func (b *Broker) FanoutStep(event Event) []BrokerDelivery {
	from := event.ProducerAgentID
	if from == "" && event.Step != nil {
		from = event.Step.AgentID
	}
	var deliveries []BrokerDelivery
	for _, successor := range b.graph.successors[from] {
		delivered := cloneEvent(event)
		delivered.ProducerAgentID = from
		delivered.TargetAgentID = successor
		delivered.EdgeID = b.graph.edgeIDs[edgeKey(from, successor)]
		delivery := BrokerDelivery{Event: delivered}
		b.enqueue(successor, delivery)
		deliveries = append(deliveries, delivery)
	}
	return deliveries
}

func (b *Broker) PropagateEOF(runID, from string, finalStep int, reason string) []BrokerDelivery {
	var deliveries []BrokerDelivery
	for _, successor := range b.graph.successors[from] {
		event := Event{
			RunID:           runID,
			Type:            EventUpstreamEOF,
			ProducerAgentID: from,
			TargetAgentID:   successor,
			EdgeID:          b.graph.edgeIDs[edgeKey(from, successor)],
			Control: &ControlPacket{
				SchemaVersion: "streamma.control.v1",
				RunID:         runID,
				ControlType:   "upstream_eof",
				AgentID:       from,
				TargetAgentID: successor,
				EdgeID:        b.graph.edgeIDs[edgeKey(from, successor)],
				FinalStep:     finalStep,
				Reason:        reason,
			},
		}
		delivery := BrokerDelivery{Event: event}
		b.enqueue(successor, delivery)
		deliveries = append(deliveries, delivery)
	}
	return deliveries
}

func (b *Broker) Dequeue(agentID string) (BrokerDelivery, bool) {
	queue := b.queues[agentID]
	if len(queue) == 0 {
		return BrokerDelivery{}, false
	}
	delivery := queue[0]
	copy(queue, queue[1:])
	queue = queue[:len(queue)-1]
	b.queues[agentID] = queue
	return delivery, true
}

func (b *Broker) QueueLen(agentID string) int {
	return len(b.queues[agentID])
}

func (b *Broker) MarkEOF(targetAgentID, predecessorID string) {
	if _, ok := b.receivedEOF[targetAgentID]; !ok {
		b.receivedEOF[targetAgentID] = map[string]bool{}
	}
	b.receivedEOF[targetAgentID][predecessorID] = true
}

func (b *Broker) AllPredecessorsEOF(agentID string) bool {
	for _, predecessor := range b.graph.predecessors[agentID] {
		if !b.receivedEOF[agentID][predecessor] {
			return false
		}
	}
	return true
}

func (b *Broker) enqueue(agentID string, delivery BrokerDelivery) {
	b.queues[agentID] = append(b.queues[agentID], delivery)
}
