package model

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"paw/internal/message"
)

func TestResponsesCompletionMatchesRawReasoning(t *testing.T) {
	for _, tc := range []struct {
		name      string
		eventType string
		deltas    []string
		output    string
		want      string
	}{
		{
			name: "summary whitespace", eventType: "response.reasoning_summary_text.delta",
			deltas: []string{"\n  think", "\t\n"},
			output: `[{"type":"reasoning","summary":[{"type":"summary_text","text":"\n  think\t\n"}]}]`,
			want:   "\n  think\t\n",
		},
		{
			name: "summary parts", eventType: "response.reasoning_summary_text.delta",
			deltas: []string{"First", "Second"},
			output: `[{"type":"reasoning","summary":[{"type":"summary_text","text":"First"},{"type":"summary_text","text":"Second"}]}]`,
			want:   "FirstSecond",
		},
		{
			name: "string summary parts", eventType: "response.reasoning_summary_text.delta",
			deltas: []string{" First ", " Second "},
			output: `[{"type":"reasoning","summary":[" First "," Second "]}]`,
			want:   " First  Second ",
		},
		{
			name: "reasoning items", eventType: "response.reasoning_summary_text.delta",
			deltas: []string{"First", "Second"},
			output: `[{"type":"reasoning","summary":[{"type":"summary_text","text":"First"}]},{"type":"reasoning","summary":[{"type":"summary_text","text":"Second"}]}]`,
			want:   "FirstSecond",
		},
		{
			name: "content parts", eventType: "response.reasoning_text.delta",
			deltas: []string{" First ", " Second "},
			output: `[{"type":"reasoning","summary":[],"content":[{"type":"reasoning_text","text":" First "},{"type":"reasoning_text","text":" Second "}]}]`,
			want:   " First  Second ",
		},
		{
			name: "done fallback", eventType: "response.reasoning_text.done",
			deltas: []string{"\n think\n"},
			output: `[{"type":"reasoning","content":[{"type":"reasoning_text","text":"\n think\n"}]}]`,
			want:   "\n think\n",
		},
		{
			name: "missing suffix", eventType: "response.reasoning.delta",
			deltas: []string{" First "},
			output: `[{"type":"reasoning","content":[{"type":"reasoning_text","text":" First "},{"type":"reasoning_text","text":"Second"}]}]`,
			want:   " First Second",
		},
	} {
		for _, corrupt := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/corrupt=%v", tc.name, corrupt), func(t *testing.T) {
				var output []json.RawMessage
				if err := json.Unmarshal([]byte(tc.output), &output); err != nil {
					t.Fatal(err)
				}
				output = append(output, json.RawMessage(`{"type":"function_call","call_id":"call","name":"Read","arguments":"{\"file_path\":\"file.txt\"}"}`))
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "text/event-stream")
					for _, delta := range tc.deltas {
						event := responsesStreamEvent{Type: tc.eventType, Delta: delta}
						if strings.HasSuffix(tc.eventType, ".done") {
							event.Delta, event.Text = "", delta
						}
						data, _ := json.Marshal(event)
						fmt.Fprintf(w, "data: %s\n\n", data)
					}
					if corrupt {
						fmt.Fprint(w, "data: {broken\n\n")
					}
					data, _ := json.Marshal(responsesStreamEvent{Type: "response.completed", Response: &responsesAPIResponse{Status: "completed", Output: output}})
					fmt.Fprintf(w, "data: %s\n\n", data)
				}))
				defer server.Close()

				client := NewClient(Config{Transport: "openai-responses", APIBaseURL: server.URL, APIPath: "/responses", Model: "test", RetryCount: 0, RetryCountSet: true})
				events, err := client.StreamMessage(context.Background(), []message.Message{{Role: message.RoleUser, Content: "test"}}, nil)
				if err != nil {
					t.Fatal(err)
				}
				var thinking string
				done, tools, providerData := 0, 0, 0
				for event := range events {
					if event.Err != nil {
						t.Fatal(event.Err)
					}
					thinking += event.Thinking
					tools += len(event.ToolCalls)
					if len(event.ProviderData) > 0 {
						providerData++
					}
					if event.Done {
						done++
					} else if len(event.ToolCalls) > 0 || len(event.ProviderData) > 0 {
						t.Fatal("published completion state before Done")
					}
				}
				if thinking != tc.want || done != 1 || tools != 1 || providerData != 1 {
					t.Fatalf("thinking=%q want=%q done=%d tools=%d providerData=%d", thinking, tc.want, done, tools, providerData)
				}
			})
		}
	}
}

func TestResponsesCompletionRejectsRawReasoningConflict(t *testing.T) {
	for _, tc := range []struct {
		name     string
		thinking string
		text     string
		output   string
	}{
		{"different reasoning", "FirstSecond", "", `[{"type":"reasoning","summary":["First","Changed"]}]`},
		{"significant whitespace", "think", "", `[{"type":"reasoning","summary":[" think"]}]`},
		{"shorter reasoning", "think longer", "", `[{"type":"reasoning","summary":["think"]}]`},
		{"different text", "FirstSecond", "prefix", `[{"type":"reasoning","summary":["First","Second"]},{"type":"message","content":[{"type":"output_text","text":"different"}]}]`},
	} {
		for _, corrupt := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/corrupt=%v", tc.name, corrupt), func(t *testing.T) {
				var requests atomic.Int32
				var output []json.RawMessage
				if err := json.Unmarshal([]byte(tc.output), &output); err != nil {
					t.Fatal(err)
				}
				output = append(output, json.RawMessage(`{"type":"function_call","call_id":"call","name":"Read","arguments":"{\"file_path\":\"file.txt\"}"}`))
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests.Add(1)
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprintf(w, "data: {\"type\":\"response.reasoning_summary_text.delta\",\"delta\":%q}\n\n", tc.thinking)
					fmt.Fprintf(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":%q}\n\n", tc.text)
					if corrupt {
						fmt.Fprint(w, "data: {broken\n\n")
					}
					data, _ := json.Marshal(responsesStreamEvent{Type: "response.completed", Response: &responsesAPIResponse{Output: output, Usage: &Usage{OutputTokens: 1}}})
					fmt.Fprintf(w, "data: %s\n\n", data)
				}))
				defer server.Close()
				client := NewClient(Config{Transport: "openai-responses", APIBaseURL: server.URL, APIPath: "/responses", Model: "test", RetryCount: 1, RetryCountSet: true})
				events, err := client.StreamMessage(context.Background(), []message.Message{{Role: message.RoleUser, Content: "test"}}, nil)
				if err != nil {
					t.Fatal(err)
				}
				var thinking, text string
				failed := false
				for event := range events {
					thinking += event.Thinking
					text += event.Delta
					if event.Done || len(event.ToolCalls) > 0 || len(event.ProviderData) > 0 || event.Usage != nil {
						t.Fatal("published rejected completion state")
					}
					if event.Err != nil {
						var failure *responsesStreamFailure
						if !errors.As(event.Err, &failure) || failure.EventType != "completion_mismatch" {
							t.Fatalf("unexpected error: %v", event.Err)
						}
						failed = true
					}
				}
				if !failed || thinking != tc.thinking || text != tc.text || requests.Load() != 1 {
					t.Fatalf("failed=%v thinking=%q text=%q requests=%d", failed, thinking, text, requests.Load())
				}
			})
		}
	}
}

func TestResponsesCompletedOnlyReasoningKeepsFormatting(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%v", stream), func(t *testing.T) {
			response := `{"status":"completed","output":[{"type":"reasoning","summary":[" First "," "]},{"type":"reasoning","summary":[{"type":"summary_text","text":" Second\n"}]},{"type":"reasoning","summary":[],"content":[{"type":"reasoning_text","text":" Third "},{"type":"reasoning_text","text":" Fourth "}]},{"type":"message","content":[{"type":"output_text","text":"answer"}]}]}`
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if stream {
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprintf(w, "data: {\"type\":\"response.completed\",\"response\":%s}\n\n", response)
				} else {
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprint(w, response)
				}
			}))
			defer server.Close()
			client := NewClient(Config{Transport: "openai-responses", APIBaseURL: server.URL, APIPath: "/responses", Model: "test", Stream: stream, streamSet: true})
			events, err := client.StreamMessage(context.Background(), []message.Message{{Role: message.RoleUser, Content: "test"}}, nil)
			if err != nil {
				t.Fatal(err)
			}
			var thinking, text string
			done := 0
			for event := range events {
				if event.Err != nil {
					t.Fatal(event.Err)
				}
				thinking += event.Thinking
				text += event.Delta
				if event.Done {
					done++
				}
			}
			if thinking != "First\n\nSecond\n\nThird\n\nFourth" || text != "answer" || done != 1 {
				t.Fatalf("thinking=%q text=%q done=%d", thinking, text, done)
			}
		})
	}
}

func TestResponsesReasoningCompletionUsesRawSnapshot(t *testing.T) {
	for _, tc := range []struct {
		name      string
		published string
		summaries []string
		wantErr   bool
	}{
		{"whitespace", "\n think\n", []string{"\n think\n"}, false},
		{"parts", "FirstSecond", []string{"First", "Second"}, false},
		{"suffix", " First ", []string{" First ", "Second"}, false},
		{"mismatch", "\n think\n", []string{"\n changed\n"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			summary, _ := json.Marshal(tc.summaries)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				requests.Add(1)
				data, _ := json.Marshal(responsesStreamEvent{Type: "response.reasoning_summary_text.delta", Delta: tc.published})
				fmt.Fprintf(w, "data: %s\n\n", data)
				fmt.Fprintf(w, `data: {"type":"response.completed","response":{"output":[{"type":"reasoning","summary":%s},{"type":"function_call","call_id":"call","name":"Read","arguments":"{}"}]}}`+"\n\n", summary)
			}))
			defer server.Close()
			client := NewClient(Config{Transport: "openai-responses", APIBaseURL: server.URL, APIPath: "/responses", Model: "test", RetryCount: 1, RetryCountSet: true})
			events, err := client.StreamMessage(context.Background(), []message.Message{{Role: message.RoleUser, Content: "test"}}, nil)
			if err != nil {
				t.Fatal(err)
			}
			var thinking string
			var gotErr error
			done, tools, providerData := 0, 0, 0
			for event := range events {
				thinking += event.Thinking
				tools += len(event.ToolCalls)
				if len(event.ProviderData) > 0 {
					providerData++
				}
				if event.Done {
					done++
				}
				if event.Err != nil {
					gotErr = event.Err
				}
			}
			if requests.Load() != 1 {
				t.Fatalf("requests=%d want=1", requests.Load())
			}
			if tc.wantErr {
				var failure *responsesStreamFailure
				if !errors.As(gotErr, &failure) || failure.EventType != "completion_mismatch" || thinking != tc.published || done != 0 || tools != 0 || providerData != 0 {
					t.Fatalf("unsafe completion: thinking=%q done=%d tools=%d providerData=%d err=%v", thinking, done, tools, providerData, gotErr)
				}
			} else if gotErr != nil || thinking != strings.Join(tc.summaries, "") || done != 1 || tools != 1 || providerData != 1 {
				t.Fatalf("thinking=%q done=%d tools=%d providerData=%d err=%v", thinking, done, tools, providerData, gotErr)
			}
		})
	}
}
