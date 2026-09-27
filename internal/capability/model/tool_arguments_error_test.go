package model

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"paw/internal/message"
)

func TestDecodeToolArgumentsFailureHasStructuredType(t *testing.T) {
	for _, raw := range []string{"", "[]", "null", `{"key":`, `{"key":"value"}garbage`} {
		t.Run(raw, func(t *testing.T) {
			input, err := decodeToolArguments("Provider", "call-1", "Lookup", []byte(raw))
			if input != nil || err == nil {
				t.Fatalf("input=%s err=%v, want rejected arguments", input, err)
			}
			var argsErr *ToolArgumentsError
			if !errors.As(fmt.Errorf("wrapped: %w", err), &argsErr) || argsErr.Provider != "Provider" || argsErr.CallID != "call-1" || argsErr.Tool != "Lookup" {
				t.Fatalf("error=%#v, want structured provider, call ID and tool", err)
			}
			if got := err.Error(); got != `Provider returned invalid JSON object arguments for tool "Lookup"` {
				t.Fatalf("error=%q, want existing diagnostic unchanged", got)
			}
		})
	}
}

func TestNativeToolArgumentsFailureRejectsWholeBatch(t *testing.T) {
	for _, protocol := range []string{"Chat Completions", "Anthropic"} {
		t.Run(protocol, func(t *testing.T) {
			var calls []message.ToolCall
			var err error
			if protocol == "Chat Completions" {
				valid := &activeOpenAIToolCall{id: "valid", name: "Write"}
				valid.args.WriteString(`{"key":"valid"}`)
				invalid := &activeOpenAIToolCall{id: "invalid", name: "Lookup"}
				invalid.args.WriteString(`{"key":`)
				calls, err = openAIToolCallsFromAccumulated(map[int]*activeOpenAIToolCall{0: valid, 1: invalid})
			} else {
				calls, err = resolveAnthropicToolCalls([]*activeAnthropicToolCall{
					{id: "valid", name: "Write", initialInput: json.RawMessage(`{"key":"valid"}`)},
					{id: "invalid", name: "Lookup", initialInput: json.RawMessage(`{"key":`)},
				})
			}
			if len(calls) != 0 || err == nil {
				t.Fatalf("calls=%#v err=%v, want no executable part of rejected batch", calls, err)
			}
			var argsErr *ToolArgumentsError
			if !errors.As(err, &argsErr) || argsErr.CallID != "invalid" || !strings.HasPrefix(err.Error(), protocol) {
				t.Fatalf("error=%T %v, want structured native argument failure", err, err)
			}
		})
	}
}
