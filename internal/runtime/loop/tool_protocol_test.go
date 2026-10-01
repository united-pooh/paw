package loop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"paw/internal/capability/model"
	"paw/internal/capability/tool"
	"paw/internal/message"
	"paw/internal/storage/session"
	"strings"
	"testing"
	"time"
)

const malformedToolProtocolFixture = "{\"type\":\"tool_use\">\n{\"command\":\"printf fixture\",\"cwd\":\".\",\"timeout_seconds\":20}\n\n</｜｜DSML｜｜ parameter>\n</｜｜DSML｜｜ invoke>\n</｜｜DSML｜｜ calls>"

func TestMalformedToolProtocolNeverBecomesAssistantText(t *testing.T) {
	for _, tc := range []struct {
		name      string
		prefix    string
		payload   string
		chunkSize int
	}{
		{name: "screenshot shape", payload: malformedToolProtocolFixture},
		{name: "split JSON", payload: malformedToolProtocolFixture, chunkSize: 1},
		{name: "after visible prose", prefix: "Preparing the next step.\n", payload: malformedToolProtocolFixture, chunkSize: 3},
		{name: "mixed chunk", prefix: "Preparing the next step.\n", payload: "One more check.\n" + malformedToolProtocolFixture},
		{name: "DSML invoke", payload: `<｜DSML｜invoke name="Bash"><｜DSML｜parameter name="command">printf fixture</｜DSML｜parameter></｜DSML｜invoke>`, chunkSize: 1},
		{name: "DSML suffix", payload: `</｜｜DSML｜｜ calls>`, chunkSize: 1},
		{name: "unfinished DSML tag", payload: `<｜DSML｜inv`, chunkSize: 1},
		{name: "valid JSON then malformed", payload: `{"type":"tool_use","name":"Bash","input":{}}` + "\n" + malformedToolProtocolFixture},
		{name: "valid JSON then DSML same line", payload: `{"type":"tool_use","name":"Bash","input":{}}</｜DSML｜calls>`},
		{name: "partly valid array", payload: `[{"type":"tool_use","name":"Bash","input":{}},` + malformedToolProtocolFixture + "]"},
		{name: "partly valid DSML array", payload: `[{"type":"tool_use","name":"Bash","input":{}},</｜DSML｜calls>]`},
		{name: "indented partly valid array", payload: "[\n    {\"type\":\"tool_use\",\"name\":\"Bash\",\"input\":{}},\n    {\"type\":\"tool_use\">\n]"},
		{name: "valid invoke then malformed", payload: `<invoke name="Bash">{"command":"printf fixture"}</invoke>` + "\n" + malformedToolProtocolFixture},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var events []model.StreamEvent
			if tc.prefix != "" {
				events = append(events, model.StreamEvent{Delta: tc.prefix})
			}
			step := tc.chunkSize
			if step == 0 {
				step = len(tc.payload)
			}
			for start := 0; start < len(tc.payload); start += step {
				events = append(events, model.StreamEvent{Delta: tc.payload[start:min(start+step, len(tc.payload))]})
			}
			events = append(events, model.StreamEvent{Done: true})
			client := &fakeModel{rounds: []fakeRound{{events: events}}}
			for range maxToolProtocolRecoveries {
				client.rounds = append(client.rounds, protocolFailureRound())
			}
			output := &fakeUI{}
			registry := tool.NewRegistry()
			registry.Register(&fakeTool{name: "Bash", output: "must not execute"})
			runner := NewEngine(client, output, registry, nil, "")
			_, err := runner.RunTurn(context.Background(), "synthetic tool protocol fixture")
			var protocolError *ToolProtocolError
			if !errors.As(err, &protocolError) {
				t.Fatalf("RunTurn error = %v, want tool protocol error", err)
			}
			wantVisible := tc.prefix
			if tc.name == "mixed chunk" {
				wantVisible += "One more check.\n"
			}
			if got := strings.Join(output.deltas, ""); got != wantVisible {
				t.Fatalf("visible = %q, want %q", got, wantVisible)
			}
			if len(output.toolCalls) != 0 || len(client.calls) != 1+maxToolProtocolRecoveries {
				t.Fatalf("tool calls = %v; model requests = %d", output.toolCalls, len(client.calls))
			}
		})
	}
}

func TestMalformedToolProtocolResponsesExhaustsWithoutJournalSuccess(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		t.Run(fmt.Sprintf("text_deltas_%t", streaming), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer r.Body.Close()
				w.Header().Set("Content-Type", "text/event-stream")
				if streaming {
					for _, text := range []string{"{\"type\":", strings.TrimPrefix(malformedToolProtocolFixture, "{\"type\":")} {
						data, _ := json.Marshal(map[string]any{"type": "response.output_text.delta", "delta": text})
						fmt.Fprintf(w, "data: %s\n\n", data)
					}
				}
				data, _ := json.Marshal(map[string]any{"type": "response.completed", "response": map[string]any{
					"status": "completed", "output": []map[string]any{{"type": "message", "role": "assistant", "content": []map[string]any{{"type": "output_text", "text": malformedToolProtocolFixture}}}},
				}})
				fmt.Fprintf(w, "data: %s\n\n", data)
			}))
			defer server.Close()
			client := model.NewClient(model.Config{Provider: "DeepSeek", Transport: "openai-responses", Model: "synthetic", APIBaseURL: server.URL, APIPath: "/responses", Stream: true, Timeout: time.Second})
			store, err := session.NewJSONLStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			output := &fakeUI{}
			runner := NewEngine(client, output, tool.NewRegistry(), store, "protocol-fixture")
			_, err = runner.RunTurn(context.Background(), "synthetic protocol test")
			var protocolError *ToolProtocolError
			if !errors.As(err, &protocolError) {
				t.Fatalf("error = %v", err)
			}
			if len(output.deltas) != 0 || len(output.toolCalls) != 0 {
				t.Fatalf("published protocol: %#v", output)
			}
			snapshot, err := store.LoadSnapshot(context.Background(), "protocol-fixture")
			if err != nil {
				t.Fatal(err)
			}
			if snapshot.Recovery == nil {
				t.Fatal("failed protocol recorded as completed turn")
			}
			for _, msg := range snapshot.ActiveHistory {
				if msg.Role == message.RoleAssistant {
					t.Fatalf("bad protocol persisted as successful assistant: %#v", msg)
				}
			}
		})
	}
}

func TestToolProtocolGuardPreservesOrdinaryDocuments(t *testing.T) {
	for _, content := range []string{
		`{"command":"printf fixture","cwd":"."}`,
		`{"example":{"type":"tool_use"},"description":"schema documentation"}`,
		"The token `<｜DSML｜invoke>` is an example, not a request.",
		"Example:\n```text\n" + malformedToolProtocolFixture + "\n```\nEnd of example.",
		"```xml\n<｜DSML｜invoke name=\"Bash\">example</｜DSML｜invoke>\n```",
		"````text\n```text\n" + malformedToolProtocolFixture + "\n```\n````",
	} {
		output := &fakeUI{}
		client := &fakeModel{rounds: []fakeRound{{events: []model.StreamEvent{{Delta: content}, {Done: true}}}}}
		runner := NewEngine(client, output, tool.NewRegistry(), nil, "")
		got, err := runner.RunTurn(context.Background(), "show documentation")
		if err != nil {
			t.Fatalf("content %q: %v", content, err)
		}
		if got.Content != content || strings.Join(output.deltas, "") != content {
			t.Fatalf("content changed: %#v, deltas %q", got, output.deltas)
		}
		if len(output.toolCalls) != 0 {
			t.Fatalf("executed document: %#v", output.toolCalls)
		}
	}
}

func TestToolProtocolGuardPreservesSplitFencedDocument(t *testing.T) {
	content := "Example:\n```text\n" + malformedToolProtocolFixture + "\n```\nEnd."
	var events []model.StreamEvent
	for _, r := range content {
		events = append(events, model.StreamEvent{Delta: string(r)})
	}
	events = append(events, model.StreamEvent{Done: true})
	output := &fakeUI{}
	client := &fakeModel{rounds: []fakeRound{{events: events}}}
	runner := NewEngine(client, output, tool.NewRegistry(), nil, "")
	got, err := runner.RunTurn(context.Background(), "show documentation")
	if err != nil {
		t.Fatal(err)
	}
	if got.Content != content || strings.Join(output.deltas, "") != content {
		t.Fatalf("document lost: %#v, visible %q", got, strings.Join(output.deltas, ""))
	}
}

func TestMalformedToolProtocolStructuredPartDoesNotLeakOnRestore(t *testing.T) {
	output := &fakeUI{}
	client := &fakeModel{rounds: []fakeRound{{events: []model.StreamEvent{
		{AssistantPart: &model.AssistantPartEvent{Lifecycle: model.AssistantPartLifecycleStart, Type: "text", BlockIndex: 0}},
		{AssistantPart: &model.AssistantPartEvent{Lifecycle: model.AssistantPartLifecycleDelta, Type: "text", BlockIndex: 0, Delta: malformedToolProtocolFixture}},
		{AssistantPart: &model.AssistantPartEvent{Lifecycle: model.AssistantPartLifecycleEnd, Type: "text", BlockIndex: 0}},
		{Done: true, ProviderData: json.RawMessage(`{"transport":"openai-responses","version":1}`)},
	}}}}
	runner := NewEngine(client, output, tool.NewRegistry(), nil, "")
	partial, err := runner.runModelTurn(context.Background(), []message.Message{{Role: message.RoleUser, Content: "synthetic"}}, nil)
	if err == nil {
		t.Fatal("expected tool protocol error")
	}
	if partial.Content != "" || len(output.deltas) != 0 {
		t.Fatalf("visible leaked: %#v, %q", partial, output.deltas)
	}
	for _, part := range partial.AssistantParts {
		if part.Text != nil && part.Text.Text != "" {
			t.Fatalf("restorable text leaked: %#v", part)
		}
	}
}

func TestMalformedToolProtocolInterruptedPreservesCause(t *testing.T) {
	wantErr := errors.New("synthetic transport failure")
	output := &fakeUI{}
	client := &fakeModel{rounds: []fakeRound{{events: []model.StreamEvent{
		{AssistantPart: &model.AssistantPartEvent{Lifecycle: model.AssistantPartLifecycleStart, Type: "text", BlockIndex: 0}},
		{AssistantPart: &model.AssistantPartEvent{Lifecycle: model.AssistantPartLifecycleDelta, Type: "text", BlockIndex: 0, Delta: malformedToolProtocolFixture}},
		{Err: wantErr},
	}}}}
	runner := NewEngine(client, output, tool.NewRegistry(), nil, "")
	partial, err := runner.runModelTurn(context.Background(), []message.Message{{Role: message.RoleUser, Content: "synthetic"}}, nil)
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want transport cause", err)
	}
	if partial.Content != "" || len(output.deltas) != 0 {
		t.Fatalf("visible protocol: %#v", partial)
	}
	for _, part := range partial.AssistantParts {
		if part.Text != nil && part.Text.Text != "" {
			t.Fatalf("restorable protocol: %#v", part)
		}
	}
}
