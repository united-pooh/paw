package goal

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"reflect"
	"strconv"
	"strings"
	"time"

	"paw/internal/message"
)

type MetricMeasurement struct {
	Tool        string `json:"tool"`
	Command     string `json:"command"`
	CWD         string `json:"cwd"`
	JSONPointer string `json:"json_pointer"`
}

type MetricSpec struct {
	Name        string            `json:"name"`
	Comparison  string            `json:"comparison"`
	Target      float64           `json:"target"`
	Unit        string            `json:"unit"`
	Measurement MetricMeasurement `json:"measurement"`
}

type MetricResult struct {
	ToolCallID string    `json:"tool_call_id"`
	Value      float64   `json:"value"`
	Digest     string    `json:"digest"`
	RecordedAt time.Time `json:"recorded_at"`
}

type HistorySource func(context.Context) ([]message.Message, error)

func (m MetricSpec) Validate() error {
	if strings.TrimSpace(m.Name) == "" || strings.TrimSpace(m.Unit) == "" {
		return fmt.Errorf("metric name and unit are required")
	}
	if m.Comparison != "gte" && m.Comparison != "lte" {
		return fmt.Errorf("metric comparison must be gte or lte")
	}
	if math.IsNaN(m.Target) || math.IsInf(m.Target, 0) {
		return fmt.Errorf("metric target must be finite")
	}
	if m.Measurement.Tool != "Bash" || strings.TrimSpace(m.Measurement.Command) == "" {
		return fmt.Errorf("measurement requires tool Bash and an exact command")
	}
	if !strings.HasPrefix(m.Measurement.JSONPointer, "/") {
		return fmt.Errorf("measurement json_pointer must identify a numeric JSON field")
	}
	for i := 0; i < len(m.Measurement.JSONPointer); i++ {
		if m.Measurement.JSONPointer[i] == '~' {
			if i+1 == len(m.Measurement.JSONPointer) || (m.Measurement.JSONPointer[i+1] != '0' && m.Measurement.JSONPointer[i+1] != '1') {
				return fmt.Errorf("measurement json_pointer contains an invalid RFC 6901 escape")
			}
			i++
		}
	}
	return nil
}

func (m MetricSpec) Meets(value float64) bool {
	if m.Validate() != nil || math.IsNaN(value) || math.IsInf(value, 0) {
		return false
	}
	return (m.Comparison == "gte" && value >= m.Target) || (m.Comparison == "lte" && value <= m.Target)
}

func cloneMetric(m *MetricSpec) *MetricSpec {
	if m == nil {
		return nil
	}
	copy := *m
	return &copy
}

func validateMetricChange(current, next Goal) error {
	if next.Metric == nil && len(next.Measurements) > 0 {
		return fmt.Errorf("measurement requires a frozen metric")
	}
	seen := make(map[string]bool, len(next.Measurements))
	for _, result := range next.Measurements {
		if result.ToolCallID == "" || result.Digest == "" || result.RecordedAt.IsZero() || math.IsNaN(result.Value) || math.IsInf(result.Value, 0) {
			return fmt.Errorf("measurement must contain finite verified tool evidence")
		}
		if seen[result.ToolCallID] {
			return fmt.Errorf("measurement tool call was already recorded")
		}
		seen[result.ToolCallID] = true
	}
	if current.Metric != nil && !reflect.DeepEqual(current.Metric, next.Metric) {
		return fmt.Errorf("goal metric is frozen; create a new goal to change acceptance or measurement")
	}
	if next.Metric != nil {
		if err := next.Metric.Validate(); err != nil {
			return err
		}
	}
	if len(next.Measurements) < len(current.Measurements) || len(next.Measurements) > len(current.Measurements)+1 {
		return fmt.Errorf("metric measurements must append one verified result at a time")
	}
	for i := range current.Measurements {
		if current.Measurements[i] != next.Measurements[i] {
			return fmt.Errorf("metric measurement history is immutable")
		}
	}
	return nil
}

func goalHistoryToolCalls(entry message.Message) []message.ToolCall {
	if entry.Role != message.RoleAssistant {
		return nil
	}
	if len(entry.AssistantParts) > 0 {
		var calls []message.ToolCall
		for _, part := range entry.AssistantParts {
			if part.ToolCall != nil {
				calls = append(calls, *part.ToolCall)
			}
		}
		return calls
	}
	if len(entry.ToolUses) > 0 {
		return entry.ToolUses
	}
	if entry.ToolUse != nil {
		return []message.ToolCall{*entry.ToolUse}
	}
	return nil
}

func goalHistoryToolResults(entry message.Message) []message.ToolResult {
	if len(entry.ToolResults) > 0 {
		return entry.ToolResults
	}
	if entry.ToolResult != nil {
		return []message.ToolResult{*entry.ToolResult}
	}
	return nil
}

func historyCalls(history []message.Message) map[string]bool {
	ids := map[string]bool{}
	for _, entry := range history {
		for _, call := range goalHistoryToolCalls(entry) {
			ids[call.ID] = true
		}
	}
	return ids
}

func extractMetric(spec MetricSpec, id string, history []message.Message) (float64, string, error) {
	var call *message.ToolCall
	var result *message.ToolResult
	callIndex, resultIndex := -1, -1
	for index, entry := range history {
		for _, candidate := range goalHistoryToolCalls(entry) {
			if candidate.ID == id {
				if call != nil {
					return 0, "", fmt.Errorf("ambiguous measurement tool call %q", id)
				}
				copy := candidate
				call, callIndex = &copy, index
			}
		}
		for _, candidate := range goalHistoryToolResults(entry) {
			if candidate.ToolUseID == id {
				if result != nil {
					return 0, "", fmt.Errorf("ambiguous measurement tool result %q", id)
				}
				copy := candidate
				result, resultIndex = &copy, index
			}
		}
	}
	if call == nil || result == nil || resultIndex <= callIndex || result.IsError || call.InputError != "" {
		return 0, "", fmt.Errorf("measurement requires a completed successful tool call and result from this goal")
	}
	var input struct {
		Command string `json:"command"`
		CWD     string `json:"cwd"`
	}
	if err := json.Unmarshal(call.Input, &input); err != nil {
		return 0, "", fmt.Errorf("invalid measurement tool arguments: %w", err)
	}
	if call.Name != spec.Measurement.Tool || input.Command != spec.Measurement.Command || input.CWD != spec.Measurement.CWD {
		return 0, "", fmt.Errorf("measurement call does not match frozen tool, command and cwd")
	}
	var value any
	decoder := json.NewDecoder(strings.NewReader(result.Content))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return 0, "", fmt.Errorf("measurement output must be one complete JSON document: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return 0, "", fmt.Errorf("measurement output contains trailing data, command failure, or truncation")
	}
	for _, segment := range strings.Split(spec.Measurement.JSONPointer[1:], "/") {
		for i := 0; i < len(segment); i++ {
			if segment[i] == '~' && (i+1 == len(segment) || (segment[i+1] != '0' && segment[i+1] != '1')) {
				return 0, "", fmt.Errorf("invalid JSON pointer escape")
			}
			if segment[i] == '~' {
				i++
			}
		}
		key := strings.ReplaceAll(strings.ReplaceAll(segment, "~1", "/"), "~0", "~")
		switch object := value.(type) {
		case map[string]any:
			var ok bool
			value, ok = object[key]
			if !ok {
				return 0, "", fmt.Errorf("measurement JSON pointer field %q is missing", key)
			}
		case []any:
			i, err := strconv.Atoi(key)
			if err != nil || i < 0 || i >= len(object) || strconv.Itoa(i) != key {
				return 0, "", fmt.Errorf("invalid measurement array index %q", key)
			}
			value = object[i]
		default:
			return 0, "", fmt.Errorf("measurement JSON pointer traverses a scalar")
		}
	}
	number, ok := value.(json.Number)
	if !ok {
		return 0, "", fmt.Errorf("measurement must be a JSON number, not a string or passed flag")
	}
	measured, err := number.Float64()
	if err != nil || math.IsNaN(measured) || math.IsInf(measured, 0) {
		return 0, "", fmt.Errorf("measurement must be a finite number")
	}
	digest := sha256.Sum256(bytes.Join([][]byte{[]byte(call.ID), call.Input, []byte(result.Content)}, []byte{0}))
	return measured, hex.EncodeToString(digest[:]), nil
}
