package streamma

import (
	"context"
	"fmt"
	"paw/internal/model"
	"strconv"
	"strings"
	"time"
)

// defaultMaxInvocationsPerAgent 是每个 agent 的默认调用次数上限。
// 每次调用都会启动一个新的子智能体会话；模型单次 invocation 可以产出
// 任意多个 step，经 arrival 策略链式放大（每个 step 触发下游一次新
// 调用）会变成无界的子智能体数量。达到上限后该 agent 以最后一次输出
// 收尾（complete），多余输入记录在 agent.iteration_limit 事件中。
const defaultMaxInvocationsPerAgent = 4

type RuntimeConfig struct {
	Graph     GraphSpec
	Model     ModelStreamer
	Agent     AgentStreamer
	EventLog  *EventLog
	EventSink func(Event)
}

type Runtime struct {
	graph  *compiledGraph
	agent  AgentStreamer
	log    *EventLog
	broker *Broker
	sink   func(Event)

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
	invocations   int
	started       bool
	active        bool
	completed     bool

	activeAttempt       int
	activeInputEvents   []string
	activeInboundFrom   string
	activeInboundStep   *StepPacket
	activeProducedSteps int
	pendingInputEvents  []string
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
	agent := config.Agent
	if agent == nil && config.Model != nil {
		agent = modelAgentStreamer{model: config.Model}
	}
	if agent == nil {
		return nil, fmt.Errorf("streamma runtime requires an agent streamer")
	}
	log := config.EventLog
	if log == nil {
		log = NewEventLog(graph.runID)
	}
	return &Runtime{
		graph:  graph,
		agent:  agent,
		log:    log,
		broker: NewBroker(graph),
		sink:   config.EventSink,
		states: map[string]*agentRuntimeState{},
	}, nil
}

func RunGraph(ctx context.Context, spec GraphSpec, model ModelStreamer, problem string) (RunResult, error) {
	return RunGraphWithEventSink(ctx, spec, model, problem, nil)
}

func RunGraphWithEventSink(ctx context.Context, spec GraphSpec, model ModelStreamer, problem string, sink func(Event)) (RunResult, error) {
	runtime, err := NewRuntime(RuntimeConfig{Graph: spec, Model: model, EventSink: sink})
	if err != nil {
		return RunResult{}, err
	}
	return runtime.Run(ctx, problem)
}

func RunGraphWithAgent(ctx context.Context, spec GraphSpec, agent AgentStreamer, problem string) (RunResult, error) {
	return RunGraphWithAgentEventSink(ctx, spec, agent, problem, nil)
}

func RunGraphWithAgentEventSink(ctx context.Context, spec GraphSpec, agent AgentStreamer, problem string, sink func(Event)) (RunResult, error) {
	runtime, err := NewRuntime(RuntimeConfig{Graph: spec, Agent: agent, EventSink: sink})
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

	problemEvent := r.appendEvent(Event{
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
				r.handleSignal(runCtx, signals, signal)
			case <-runCtx.Done():
				r.fail("", runCtx.Err())
			}
			continue
		}
		r.fail("", fmt.Errorf("runtime stalled before all agents completed"))
		break
	}
	cancel()
	r.drainAfterCancel(signals, 5*time.Second)

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
			if !state.completed && !state.active && state.invocations >= r.graph.maxInvocationsPerAgent && len(state.pendingInputEvents) > 0 {
				// 达到调用上限：丢弃剩余输入并以最后一次输出收尾，
				// 防止 EOF 策略下 pending 输入导致无法 complete。
				dropped := len(state.pendingInputEvents)
				state.pendingInputEvents = nil
				r.emitIterationLimit(agentID, dropped)
				progress = true
				madeProgress = true
				r.completeAgent(agentID)
			}
			if !state.completed && r.canInvokeEOFTriggered(agentID) {
				progress = true
				madeProgress = true
				inputEvents := append([]string(nil), state.pendingInputEvents...)
				if err := r.invokeAgent(ctx, agentID, inputEvents, "", nil, 1, signals); err != nil {
					r.fail(agentID, err)
					return true
				}
				state.pendingInputEvents = nil
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
		return r.invokeAgent(ctx, agentID, []string{event.EventID}, "", nil, 1, signals)
	case EventStepCommitted:
		if event.Step == nil {
			return fmt.Errorf("step delivery to %s missing step packet", agentID)
		}
		inbound := cloneStepPacket(*event.Step)
		state.transcript.AppendInbound(event.ProducerAgentID, inbound)
		state.started = true
		if r.graph.invokePolicy(agentID) == InvokeOnEOF {
			state.pendingInputEvents = append(state.pendingInputEvents, event.EventID)
			return nil
		}
		return r.invokeAgent(ctx, agentID, []string{event.EventID}, event.ProducerAgentID, &inbound, 1, signals)
	case EventUpstreamEOF:
		r.broker.MarkEOF(agentID, event.ProducerAgentID)
		r.appendEvent(event)
		return nil
	default:
		return fmt.Errorf("unsupported delivery type %s for %s", event.Type, agentID)
	}
}

func (r *Runtime) invokeAgent(ctx context.Context, agentID string, inputEvents []string, inboundFrom string, inboundStep *StepPacket, attempt int, signals chan<- runtimeSignal) error {
	state := r.states[agentID]
	if state.active {
		return fmt.Errorf("agent %s already has an active invocation", agentID)
	}
	if attempt < 1 {
		attempt = 1
	}
	if state.invocations >= r.graph.maxInvocationsPerAgent {
		// 达到调用上限：不再启动新的子智能体会话，以最后一次输出收尾。
		// 超限后到达的输入不再处理（事件中记录数量）。
		dropped := len(inputEvents)
		if len(state.pendingInputEvents) > 0 {
			dropped += len(state.pendingInputEvents)
			state.pendingInputEvents = nil
		}
		r.emitIterationLimit(agentID, dropped)
		r.completeAgent(agentID)
		return nil
	}
	state.invocations++
	agent := r.graph.agents[agentID]
	invocation := AgentInvocation{
		RunID:           r.graph.runID,
		AgentID:         agentID,
		Role:            agent.Role,
		SystemPrompt:    agent.SystemPrompt,
		Problem:         state.transcript.Problem,
		InvocationIndex: state.invocations,
		Attempt:         attempt,
		StepCountHint:   agent.StepCountHint,
		StartStepIndex:  state.nextStepIndex,
		Boundary:        r.graph.boundary,
		RequireBoundary: r.graph.requireBoundary,
		InputEvents:     append([]string(nil), inputEvents...),
		InboundFrom:     inboundFrom,
		Transcript:      state.transcript.Entries(),
	}
	if inboundStep != nil {
		step := cloneStepPacket(*inboundStep)
		invocation.InboundStep = &step
	}
	config := ParserConfig{
		RunID:           r.graph.runID,
		AgentID:         agentID,
		Boundary:        r.graph.boundary,
		MaxStepBytes:    r.graph.maxStepBytes,
		RequireBoundary: r.graph.requireBoundary,
		Attempt:         attempt,
		StartIndex:      state.nextStepIndex,
		InputEvents:     append([]string(nil), inputEvents...),
	}
	state.active = true
	state.activeAttempt = attempt
	state.activeInputEvents = append([]string(nil), inputEvents...)
	state.activeInboundFrom = inboundFrom
	state.activeProducedSteps = 0
	if inboundStep != nil {
		step := cloneStepPacket(*inboundStep)
		state.activeInboundStep = &step
	} else {
		state.activeInboundStep = nil
	}
	r.activeCount++

	go func() {
		stream, err := r.agent.StreamAgent(ctx, invocation)
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

func (r *Runtime) handleSignal(ctx context.Context, signals chan<- runtimeSignal, signal runtimeSignal) {
	if signal.err != nil {
		state := r.states[signal.agentID]
		if state != nil {
			state.active = false
			r.activeCount--
			if signals != nil && (ctx == nil || ctx.Err() == nil) && r.shouldRetry(state, signal.err) {
				retryAttempt := state.activeAttempt + 1
				r.appendEvent(Event{
					RunID:           r.graph.runID,
					Type:            EventAgentRetry,
					ProducerAgentID: signal.agentID,
					Trace: map[string]string{
						"attempt":      strconv.Itoa(retryAttempt),
						"max_attempts": strconv.Itoa(r.graph.maxAttempts),
					},
					Error: signal.err.Error(),
				})
				if err := r.invokeAgent(ctx, signal.agentID, state.activeInputEvents, state.activeInboundFrom, state.activeInboundStep, retryAttempt, signals); err != nil {
					r.fail(signal.agentID, err)
					return
				}
				return
			}
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
		committed := r.appendEvent(Event{
			RunID:           r.graph.runID,
			Type:            EventStepCommitted,
			ProducerAgentID: signal.agentID,
			Step:            &step,
		})
		state.lastStepIndex = step.StepIndex
		state.lastStepText = step.Content.Text
		state.activeProducedSteps++
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
	if state.completed || state.active || r.broker.QueueLen(agentID) > 0 || len(state.pendingInputEvents) > 0 {
		return false
	}
	if len(r.graph.predecessors[agentID]) == 0 {
		return state.started
	}
	return r.broker.AllPredecessorsEOF(agentID)
}

func (r *Runtime) canInvokeEOFTriggered(agentID string) bool {
	state := r.states[agentID]
	if state == nil || state.completed || state.active {
		return false
	}
	if state.invocations >= r.graph.maxInvocationsPerAgent {
		return false
	}
	if r.graph.invokePolicy(agentID) != InvokeOnEOF {
		return false
	}
	if len(state.pendingInputEvents) == 0 {
		return false
	}
	return r.broker.QueueLen(agentID) == 0 && r.broker.AllPredecessorsEOF(agentID)
}

func (r *Runtime) emitIterationLimit(agentID string, dropped int) {
	state := r.states[agentID]
	if state == nil {
		return
	}
	r.appendEvent(Event{
		RunID:           r.graph.runID,
		Type:            EventIterationLimit,
		ProducerAgentID: agentID,
		Trace: map[string]string{
			"limit":          strconv.Itoa(r.graph.maxInvocationsPerAgent),
			"invocations":    strconv.Itoa(state.invocations),
			"dropped_inputs": strconv.Itoa(dropped),
		},
	})
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
	committed := r.appendEvent(Event{
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
	r.appendEvent(Event{
		RunID:           r.graph.runID,
		Type:            EventRunFailed,
		ProducerAgentID: agentID,
		Error:           err.Error(),
	})
}

func (r *Runtime) shouldRetry(state *agentRuntimeState, err error) bool {
	if r == nil || state == nil || err == nil {
		return false
	}
	if r.graph.maxAttempts <= 1 || state.activeAttempt >= r.graph.maxAttempts {
		return false
	}
	if state.activeProducedSteps > 0 {
		return false
	}
	return !isContextCanceledError(err)
}

func isContextCanceledError(err error) bool {
	return err == context.Canceled || err == context.DeadlineExceeded || strings.Contains(strings.ToLower(err.Error()), "context canceled")
}

func (r *Runtime) appendEvent(event Event) Event {
	committed := r.log.Append(event)
	if r.sink != nil {
		r.sink(cloneEvent(committed))
	}
	return committed
}

func (r *Runtime) allCompleted() bool {
	return r.completedCount == len(r.states)
}

func (r *Runtime) activeInvocationCount() int {
	return r.activeCount
}

func (r *Runtime) drainAfterCancel(signals <-chan runtimeSignal, timeout time.Duration) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for r.activeInvocationCount() > 0 {
		select {
		case signal := <-signals:
			r.handleSignal(context.Background(), nil, signal)
		case <-timer.C:
			return
		}
	}
	for {
		select {
		case signal := <-signals:
			r.handleSignal(context.Background(), nil, signal)
		default:
			return
		}
	}
}

type modelAgentStreamer struct {
	model ModelStreamer
}

func (m modelAgentStreamer) StreamAgent(ctx context.Context, invocation AgentInvocation) (<-chan model.StreamEvent, error) {
	transcript := &Transcript{
		AgentID: invocation.AgentID,
		System:  invocation.SystemPrompt,
		Problem: invocation.Problem,
		entries: append([]TranscriptEntry(nil), invocation.Transcript...),
	}
	return m.model.StreamMessage(ctx, BuildPrompt(transcript), nil)
}
