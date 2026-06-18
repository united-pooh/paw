package streamma

import (
	"context"
	"fmt"
	"strings"
)

type RuntimeConfig struct {
	Graph    GraphSpec
	Model    ModelStreamer
	EventLog *EventLog
}

type Runtime struct {
	graph  *compiledGraph
	model  ModelStreamer
	log    *EventLog
	broker *Broker

	states         map[string]*agentRuntimeState
	activeCount    int
	completedCount int
	final          *FinalAnswerPacket
	failed         bool
	err            error
}

type agentRuntimeState struct {
	transcript    *Transcript
	nextStepIndex int
	lastStepIndex int
	lastStepText  string
	usedSteps     []string
	started       bool
	active        bool
	completed     bool
}

type runtimeSignal struct {
	agentID string
	step    *StepPacket
	done    bool
	err     error
}

func NewRuntime(config RuntimeConfig) (*Runtime, error) {
	graph, err := compileGraph(config.Graph)
	if err != nil {
		return nil, err
	}
	if config.Model == nil {
		return nil, fmt.Errorf("streamma runtime requires a model")
	}
	log := config.EventLog
	if log == nil {
		log = NewEventLog(graph.runID)
	}
	return &Runtime{
		graph:  graph,
		model:  config.Model,
		log:    log,
		broker: NewBroker(graph),
		states: map[string]*agentRuntimeState{},
	}, nil
}

func RunGraph(ctx context.Context, spec GraphSpec, model ModelStreamer, problem string) (RunResult, error) {
	runtime, err := NewRuntime(RuntimeConfig{Graph: spec, Model: model})
	if err != nil {
		return RunResult{}, err
	}
	return runtime.Run(ctx, problem)
}

func (r *Runtime) EventLog() *EventLog {
	return r.log
}

func (r *Runtime) Run(ctx context.Context, problem string) (RunResult, error) {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	r.reset(problem)
	signals := make(chan runtimeSignal)

	problemEvent := r.log.Append(Event{
		RunID: r.graph.runID,
		Type:  EventProblem,
		Problem: &ProblemPacket{
			RunID: r.graph.runID,
			Text:  problem,
		},
	})
	r.broker.BroadcastProblem(problemEvent)

	for {
		if r.failed {
			break
		}
		progress := r.advance(runCtx, signals)
		if r.failed || r.allCompleted() {
			break
		}
		if progress {
			continue
		}
		if r.activeInvocationCount() > 0 {
			select {
			case signal := <-signals:
				r.handleSignal(signal)
			case <-runCtx.Done():
				r.fail("", runCtx.Err())
			}
			continue
		}
		r.fail("", fmt.Errorf("runtime stalled before all agents completed"))
		break
	}
	cancel()

	// Drain signals that arrived before goroutines observed cancellation.
drain:
	for {
		select {
		case signal := <-signals:
			r.handleSignal(signal)
		default:
			break drain
		}
	}

	events := r.log.Snapshot()
	if r.failed {
		return RunResult{
			RunID:  r.graph.runID,
			Status: RunFailed,
			Final:  r.final,
			Events: events,
			Error:  r.err.Error(),
		}, r.err
	}
	return RunResult{
		RunID:  r.graph.runID,
		Status: RunCompleted,
		Final:  r.final,
		Events: events,
	}, nil
}

func (r *Runtime) reset(problem string) {
	r.broker = NewBroker(r.graph)
	r.states = map[string]*agentRuntimeState{}
	r.activeCount = 0
	r.completedCount = 0
	r.final = nil
	r.failed = false
	r.err = nil
	for _, agentID := range r.graph.agentOrder {
		r.states[agentID] = &agentRuntimeState{
			transcript:    NewTranscript(r.graph.agents[agentID], problem),
			nextStepIndex: 1,
		}
	}
}

func (r *Runtime) advance(ctx context.Context, signals chan<- runtimeSignal) bool {
	progress := false
	for {
		madeProgress := false
		for _, agentID := range r.graph.agentOrder {
			if r.failed {
				return true
			}
			state := r.states[agentID]
			if state.completed {
				continue
			}
			if !state.active {
				if delivery, ok := r.broker.Dequeue(agentID); ok {
					progress = true
					madeProgress = true
					if err := r.handleDelivery(ctx, agentID, delivery, signals); err != nil {
						r.fail(agentID, err)
						return true
					}
				}
			}
			if !state.completed && r.canComplete(agentID) {
				progress = true
				madeProgress = true
				r.completeAgent(agentID)
			}
		}
		if !madeProgress {
			return progress
		}
	}
}

func (r *Runtime) handleDelivery(ctx context.Context, agentID string, delivery BrokerDelivery, signals chan<- runtimeSignal) error {
	event := delivery.Event
	state := r.states[agentID]
	switch event.Type {
	case EventProblem:
		state.started = true
		return r.invokeAgent(ctx, agentID, []string{event.EventID}, signals)
	case EventStepCommitted:
		if event.Step == nil {
			return fmt.Errorf("step delivery to %s missing step packet", agentID)
		}
		state.transcript.AppendInbound(event.ProducerAgentID, *event.Step)
		state.started = true
		return r.invokeAgent(ctx, agentID, []string{event.EventID}, signals)
	case EventUpstreamEOF:
		r.broker.MarkEOF(agentID, event.ProducerAgentID)
		r.log.Append(event)
		return nil
	default:
		return fmt.Errorf("unsupported delivery type %s for %s", event.Type, agentID)
	}
}

func (r *Runtime) invokeAgent(ctx context.Context, agentID string, inputEvents []string, signals chan<- runtimeSignal) error {
	state := r.states[agentID]
	if state.active {
		return fmt.Errorf("agent %s already has an active invocation", agentID)
	}
	prompt := BuildPrompt(state.transcript)
	config := ParserConfig{
		RunID:        r.graph.runID,
		AgentID:      agentID,
		Boundary:     r.graph.boundary,
		MaxStepBytes: r.graph.maxStepBytes,
		StartIndex:   state.nextStepIndex,
		InputEvents:  append([]string(nil), inputEvents...),
	}
	state.active = true
	r.activeCount++

	go func() {
		stream, err := r.model.StreamMessage(ctx, prompt, nil)
		if err != nil {
			sendRuntimeSignal(ctx, signals, runtimeSignal{agentID: agentID, err: fmt.Errorf("model %s: %w", agentID, err)})
			return
		}

		err = StreamSteps(ctx, stream, config, func(step StepPacket) error {
			return sendRuntimeSignal(ctx, signals, runtimeSignal{agentID: agentID, step: &step})
		})
		if err != nil {
			if IsParserFatal(err) {
				sendRuntimeSignal(ctx, signals, runtimeSignal{agentID: agentID, err: fmt.Errorf("parser %s: %w", agentID, err)})
				return
			}
			sendRuntimeSignal(ctx, signals, runtimeSignal{agentID: agentID, err: fmt.Errorf("model %s: %w", agentID, err)})
			return
		}
		sendRuntimeSignal(ctx, signals, runtimeSignal{agentID: agentID, done: true})
	}()

	return nil
}

func sendRuntimeSignal(ctx context.Context, signals chan<- runtimeSignal, signal runtimeSignal) error {
	select {
	case signals <- signal:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *Runtime) handleSignal(signal runtimeSignal) {
	if signal.err != nil {
		state := r.states[signal.agentID]
		if state != nil {
			state.active = false
			r.activeCount--
		}
		r.fail(signal.agentID, signal.err)
		return
	}
	state := r.states[signal.agentID]
	if state == nil {
		r.fail(signal.agentID, fmt.Errorf("runtime signal for unknown agent %s", signal.agentID))
		return
	}
	if signal.step != nil {
		step := cloneStepPacket(*signal.step)
		step.AgentID = signal.agentID
		step.RunID = r.graph.runID
		state.transcript.AppendOwn(step)
		committed := r.log.Append(Event{
			RunID:           r.graph.runID,
			Type:            EventStepCommitted,
			ProducerAgentID: signal.agentID,
			Step:            &step,
		})
		state.lastStepIndex = step.StepIndex
		state.lastStepText = step.Content.Text
		if step.StepIndex >= state.nextStepIndex {
			state.nextStepIndex = step.StepIndex + 1
		}
		state.usedSteps = append(state.usedSteps, committed.Step.StepID)
		r.broker.FanoutStep(committed)
	}
	if signal.done {
		state.active = false
		r.activeCount--
	}
}

func (r *Runtime) canComplete(agentID string) bool {
	state := r.states[agentID]
	if state.completed || state.active || r.broker.QueueLen(agentID) > 0 {
		return false
	}
	if len(r.graph.predecessors[agentID]) == 0 {
		return state.started
	}
	return r.broker.AllPredecessorsEOF(agentID)
}

func (r *Runtime) completeAgent(agentID string) {
	state := r.states[agentID]
	state.completed = true
	r.completedCount++
	if len(r.graph.successors[agentID]) == 0 && !r.failed {
		r.emitFinal(agentID)
	}
	if len(r.graph.successors[agentID]) > 0 {
		r.broker.PropagateEOF(r.graph.runID, agentID, state.lastStepIndex, "agent_drained")
	}
}

func (r *Runtime) emitFinal(agentID string) {
	state := r.states[agentID]
	text := strings.TrimSpace(state.lastStepText)
	if text == "" {
		text = "No final step produced."
	}
	final := FinalAnswerPacket{
		SchemaVersion: "streamma.final.v1",
		RunID:         r.graph.runID,
		AgentID:       agentID,
		Answer: FinalAnswerContent{
			Format: "text",
			Text:   text,
		},
		Support: FinalAnswerSupport{
			UsedSteps: append([]string(nil), state.usedSteps...),
		},
	}
	committed := r.log.Append(Event{
		RunID:           r.graph.runID,
		Type:            EventFinalAnswer,
		ProducerAgentID: agentID,
		Final:           &final,
	})
	r.final = committed.Final
}

func (r *Runtime) fail(agentID string, err error) {
	if r.failed {
		return
	}
	r.failed = true
	if err == nil {
		err = fmt.Errorf("unknown streamma failure")
	}
	if agentID != "" && !strings.Contains(err.Error(), agentID) {
		err = fmt.Errorf("%s: %w", agentID, err)
	}
	r.err = err
	r.log.Append(Event{
		RunID: r.graph.runID,
		Type:  EventRunFailed,
		Error: err.Error(),
	})
}

func (r *Runtime) allCompleted() bool {
	return r.completedCount == len(r.states)
}

func (r *Runtime) activeInvocationCount() int {
	return r.activeCount
}
