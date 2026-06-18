package streamma

import (
	"context"
	"fmt"
	"gocode/internal/message"
	"gocode/internal/model"
	"sync"
)

type fakeResponse struct {
	events []model.StreamEvent
	err    error
}

type fakeCall struct {
	Messages []message.Message
	Tools    []model.ToolDefinition
}

type fakeModel struct {
	mu        sync.Mutex
	responses []fakeResponse
	calls     []fakeCall
}

func newFakeModel(responses ...fakeResponse) *fakeModel {
	return &fakeModel{responses: responses}
}

func fakeTextResponse(text string) fakeResponse {
	return fakeResponse{
		events: []model.StreamEvent{
			{Delta: text},
			{Done: true},
		},
	}
}

func (f *fakeModel) StreamMessage(ctx context.Context, messages []message.Message, tools []model.ToolDefinition) (<-chan model.StreamEvent, error) {
	f.mu.Lock()
	callIndex := len(f.calls)
	f.calls = append(f.calls, fakeCall{
		Messages: append([]message.Message(nil), messages...),
		Tools:    append([]model.ToolDefinition(nil), tools...),
	})
	var response fakeResponse
	if callIndex < len(f.responses) {
		response = f.responses[callIndex]
	} else {
		response = fakeTextResponse(fmt.Sprintf("default response %d\nEND_STEP\n", callIndex+1))
	}
	f.mu.Unlock()

	if response.err != nil {
		return nil, response.err
	}
	ch := make(chan model.StreamEvent, len(response.events))
	for _, event := range response.events {
		select {
		case <-ctx.Done():
			close(ch)
			return ch, nil
		case ch <- event:
		}
	}
	close(ch)
	return ch, nil
}

func (f *fakeModel) Calls() []fakeCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]fakeCall, len(f.calls))
	for i, call := range f.calls {
		out[i] = fakeCall{
			Messages: append([]message.Message(nil), call.Messages...),
			Tools:    append([]model.ToolDefinition(nil), call.Tools...),
		}
	}
	return out
}

func promptText(messages []message.Message) string {
	var text string
	for _, msg := range messages {
		text += string(msg.Role) + ":\n" + msg.Content + "\n"
	}
	return text
}
