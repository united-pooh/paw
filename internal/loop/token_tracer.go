package loop

import (
	"fmt"
	"strings"

	"paw/internal/model"
	"paw/internal/streamma"
	"paw/internal/tokentracer"
)

type traceContext struct {
	stageID string
	agentID string
	mode    string
}

type modelConfigReader interface {
	CurrentModelConfig() model.Config
}

func (runner *Runner) SetTokenTracer(tracer *tokentracer.Tracer) {
	if runner == nil {
		return
	}
	runner.mu.Lock()
	defer runner.mu.Unlock()
	runner.tokenTracer = tracer
}

func (runner *Runner) TokenTracerURL() string {
	tracer := runner.currentTokenTracer()
	if tracer == nil {
		return ""
	}
	return tracer.ServerURL()
}

func (runner *Runner) currentTokenTracer() *tokentracer.Tracer {
	if runner == nil {
		return nil
	}
	runner.mu.RLock()
	defer runner.mu.RUnlock()
	return runner.tokenTracer
}

func (runner *Runner) beginTraceTurn(input, mode string) traceContext {
	tracer := runner.currentTokenTracer()
	if tracer == nil {
		return traceContext{}
	}
	stageID, agentID := tracer.StartTurn(input, mode)
	ctx := traceContext{stageID: stageID, agentID: agentID, mode: mode}
	runner.mu.Lock()
	runner.traceStageID = stageID
	runner.traceAgentID = agentID
	runner.mu.Unlock()
	return ctx
}

func (runner *Runner) finishTraceTurn(ctx traceContext, err error) {
	if ctx.stageID == "" {
		return
	}
	tracer := runner.currentTokenTracer()
	if tracer != nil {
		tracer.FinishTurn(ctx.stageID, ctx.agentID, err)
	}
	runner.mu.Lock()
	if runner.traceStageID == ctx.stageID && runner.traceAgentID == ctx.agentID {
		runner.traceStageID = ""
		runner.traceAgentID = ""
	}
	runner.mu.Unlock()
}

func (runner *Runner) currentTraceIDs() (string, string) {
	if runner == nil {
		return "", ""
	}
	runner.mu.RLock()
	defer runner.mu.RUnlock()
	return runner.traceStageID, runner.traceAgentID
}

func (runner *Runner) recordTraceUsage(stageID, agentID string, usage tokentracer.Usage, data map[string]any) {
	tracer := runner.currentTokenTracer()
	if tracer == nil || usage.Empty() {
		return
	}
	provider, modelName := runner.currentModelLabels()
	tracer.RecordAPICall(stageID, "", agentID, "", provider, modelName, usage, data)
}

func (runner *Runner) currentModelLabels() (string, string) {
	if runner == nil || runner.model == nil {
		return "", ""
	}
	reader, ok := runner.model.(modelConfigReader)
	if !ok {
		return "", ""
	}
	cfg := reader.CurrentModelConfig()
	return strings.TrimSpace(cfg.Provider), strings.TrimSpace(cfg.Model)
}

func (runner *Runner) recordTraceEvent(eventType string, data map[string]any) {
	tracer := runner.currentTokenTracer()
	if tracer == nil {
		return
	}
	stageID, agentID := runner.currentTraceIDs()
	if data == nil {
		data = map[string]any{}
	}
	if stageID != "" {
		data["stage_id"] = stageID
	}
	if agentID != "" {
		data["agent_id"] = agentID
	}
	tracer.RecordEvent(eventType, data)
}

func (runner *Runner) streamMATokenTraceSink(stageID string) func(streamma.Event) {
	tracer := runner.currentTokenTracer()
	if tracer == nil || strings.TrimSpace(stageID) == "" {
		return nil
	}
	return func(event streamma.Event) {
		data := map[string]any{
			"stage_id":   stageID,
			"run_id":     event.RunID,
			"event_id":   event.EventID,
			"event_type": string(event.Type),
			"seq":        event.Seq,
		}
		if event.ProducerAgentID != "" {
			data["producer_agent_id"] = event.ProducerAgentID
		}
		if event.TargetAgentID != "" {
			data["target_agent_id"] = event.TargetAgentID
		}
		if event.EdgeID != "" {
			data["edge_id"] = event.EdgeID
		}
		if event.Step != nil {
			data["agent_id"] = event.Step.AgentID
			data["step_id"] = event.Step.StepID
			data["step_index"] = event.Step.StepIndex
			data["text"] = event.Step.Content.Text
			data["boundary_closed"] = event.Step.Boundary.Closed
			data["boundary_recovered"] = event.Step.Boundary.BoundaryRecovered
		}
		if event.Control != nil {
			data["control_type"] = event.Control.ControlType
			data["control_agent_id"] = event.Control.AgentID
			data["target_agent_id"] = event.Control.TargetAgentID
			data["reason"] = event.Control.Reason
		}
		if event.Final != nil {
			data["agent_id"] = event.Final.AgentID
			data["answer"] = event.Final.Answer.Text
		}
		if event.Error != "" {
			data["error"] = event.Error
		}
		tracer.RecordEvent("streamma."+string(event.Type), data)
	}
}

func mergeStreamMASinks(sinks ...func(streamma.Event)) func(streamma.Event) {
	var active []func(streamma.Event)
	for _, sink := range sinks {
		if sink != nil {
			active = append(active, sink)
		}
	}
	if len(active) == 0 {
		return nil
	}
	return func(event streamma.Event) {
		for _, sink := range active {
			sink(event)
		}
	}
}

func streamMATraceAgentName(agentID, role string) string {
	agentID = strings.TrimSpace(agentID)
	role = strings.TrimSpace(role)
	if agentID == "" {
		agentID = "agent"
	}
	if role == "" || role == agentID {
		return agentID
	}
	return fmt.Sprintf("%s (%s)", agentID, role)
}
