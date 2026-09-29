package goal

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strings"

	"paw/internal/message"
)

type MetricTool struct {
	controller *SessionController
	record     bool
}

func NewSetMetricTool(controller *SessionController) *MetricTool {
	return &MetricTool{controller: controller}
}
func NewRecordMetricTool(controller *SessionController) *MetricTool {
	return &MetricTool{controller: controller, record: true}
}
func (t *MetricTool) Name() string {
	if t.record {
		return "goal_record_metric"
	}
	return "goal_set_metric"
}
func (t *MetricTool) Description() string {
	if t.record {
		return "Record a quantitative goal measurement from an already completed Bash tool_call_id. The host extracts the number from its real JSON output using the frozen metric definition; never provide a value or passed flag. Run measurement and this tool in separate tool batches. Historical, duplicate, failed, or mismatched results are rejected. This tool never runs commands."
	}
	return "Freeze the active goal's user-requested measurable acceptance criterion and exact Bash measurement protocol. First investigate and construct a valid evaluation method if necessary. Ask the user when the target or unit is unspecified; never invent, lower, or change their target. measurement fixes the exact command, cwd (empty for workspace root), and JSON pointer of a finite numeric output. Definition cannot be changed afterward. Run the measurement only after this succeeds, through the ordinary Bash tool and its permissions."
}
func (t *MetricTool) InputSchema() json.RawMessage {
	if t.record {
		return json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"tool_call_id":{"type":"string"}},"required":["tool_call_id"]}`)
	}
	return json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"name":{"type":"string"},"comparison":{"type":"string","enum":["gte","lte"]},"target":{"type":"number"},"unit":{"type":"string"},"measurement":{"type":"object","additionalProperties":false,"properties":{"tool":{"type":"string","enum":["Bash"]},"command":{"type":"string"},"cwd":{"type":"string"},"json_pointer":{"type":"string"}},"required":["tool","command","cwd","json_pointer"]}},"required":["name","comparison","target","unit","measurement"]}`)
}
func (*MetricTool) IsConcurrencySafe(json.RawMessage) bool { return false }
func (t *MetricTool) Run(ctx context.Context, raw json.RawMessage) (string, error) {
	if err := contextErr(ctx); err != nil {
		return "", err
	}
	if t == nil || t.controller == nil {
		return "", fmt.Errorf("active goal controller is unavailable")
	}
	snapshot, err := t.controller.current()
	if err != nil {
		return "", err
	}
	if id, ok := ctx.Value(goalExecutionContextKey{}).(GoalID); !ok || id != snapshot.ID {
		return "", fmt.Errorf("metric tools are available only inside the active Goal execution, not Chat or Plan")
	}
	t.controller.mu.Lock()
	runtime := t.controller.runtime
	t.controller.mu.Unlock()
	var result any
	if t.record {
		var input struct {
			ToolCallID string `json:"tool_call_id"`
		}
		if err := decodeMetricInput(raw, &input); err != nil {
			return "", err
		}
		result, err = runtime.RecordMetric(ctx, snapshot.ID, input.ToolCallID)
	} else {
		var input MetricSpec
		if err := decodeMetricInput(raw, &input); err != nil {
			return "", err
		}
		if err := requireMetricFields(raw); err != nil {
			return "", err
		}
		err = runtime.ConfigureMetric(ctx, snapshot.ID, input)
		result = input
	}
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(result)
	return string(data), err
}

func requireMetricFields(raw json.RawMessage) error {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return err
	}
	for _, field := range []string{"name", "comparison", "target", "unit", "measurement"} {
		value, ok := object[field]
		if !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return fmt.Errorf("metric %s is required and must not be null", field)
		}
	}
	var measurement map[string]json.RawMessage
	if err := json.Unmarshal(object["measurement"], &measurement); err != nil {
		return err
	}
	for _, field := range []string{"tool", "command", "cwd", "json_pointer"} {
		value, ok := measurement[field]
		if !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return fmt.Errorf("measurement %s is required and must not be null", field)
		}
		var text string
		if err := json.Unmarshal(value, &text); err != nil {
			return fmt.Errorf("measurement %s must be a string", field)
		}
	}
	return nil
}

func decodeMetricInput(raw json.RawMessage, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("metric input must contain one JSON object")
	}
	return nil
}

func (r *Runtime) ConfigureMetric(ctx context.Context, id GoalID, spec MetricSpec) error {
	if err := spec.Validate(); err != nil {
		return err
	}
	history, err := r.metricHistory(ctx)
	if err != nil {
		return err
	}
	g, ok, err := r.store.Get(ctx, id)
	if err != nil {
		return err
	}
	if !ok || g.Status != GoalRunning {
		return fmt.Errorf("metric configuration requires a running goal")
	}
	if g.Metric != nil {
		if reflect.DeepEqual(g.Metric, &spec) {
			return nil
		}
		return fmt.Errorf("goal metric is frozen; create a new goal to change acceptance or measurement")
	}
	g.Metric = cloneMetric(&spec)
	g.UpdatedAt = r.now()
	if err := r.updateGoal(ctx, g); err != nil {
		return err
	}
	r.mu.Lock()
	r.metricExcluded = historyCalls(history)
	r.mu.Unlock()
	return nil
}

func (r *Runtime) RecordMetric(ctx context.Context, id GoalID, toolCallID string) (MetricResult, error) {
	if strings.TrimSpace(toolCallID) == "" {
		return MetricResult{}, fmt.Errorf("tool_call_id is required")
	}
	g, ok, err := r.store.Get(ctx, id)
	if err != nil {
		return MetricResult{}, err
	}
	if !ok || g.Status != GoalRunning || g.Metric == nil {
		return MetricResult{}, fmt.Errorf("recording requires a running goal with a frozen metric")
	}
	r.mu.Lock()
	excluded, active := r.metricExcluded[toolCallID], r.activeID == id && r.active
	r.mu.Unlock()
	if excluded || !active {
		return MetricResult{}, fmt.Errorf("measurement must execute after this goal run and metric definition began")
	}
	for _, previous := range g.Measurements {
		if previous.ToolCallID == toolCallID {
			return MetricResult{}, fmt.Errorf("measurement tool call was already recorded")
		}
	}
	history, err := r.metricHistory(ctx)
	if err != nil {
		return MetricResult{}, err
	}
	if !measurementFresh(toolCallID, history) {
		return MetricResult{}, fmt.Errorf("measurement is stale after a later work tool; measure again")
	}
	value, digest, err := extractMetric(*g.Metric, toolCallID, history)
	if err != nil {
		return MetricResult{}, err
	}
	result := MetricResult{ToolCallID: toolCallID, Value: value, Digest: digest, RecordedAt: r.now()}
	g.Measurements = append(g.Measurements, result)
	g.UpdatedAt = r.now()
	if err := r.updateGoal(ctx, g); err != nil {
		return MetricResult{}, err
	}
	return result, nil
}

func (r *Runtime) metricHistory(ctx context.Context) ([]message.Message, error) {
	r.mu.Lock()
	source := r.history
	r.mu.Unlock()
	if source == nil {
		return nil, fmt.Errorf("trusted tool history is unavailable; metric evidence cannot be verified")
	}
	return source(ctx)
}
