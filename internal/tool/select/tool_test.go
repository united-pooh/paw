package selecttool

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func validSingleInput() json.RawMessage {
	return json.RawMessage(`{"questions":[{"prompt":"Pick","mode":"single","options":[{"id":"a","label":"A"}]}]}`)
}
func TestToolMetadata(t *testing.T) {
	x := New(NewBroker())
	if x.Name() != "question" {
		t.Fatalf("Name() = %q", x.Name())
	}
	for _, phrase := range []string{
		"two or more concrete options",
		"prefer this tool",
		"A/B/C list",
		"open-ended questions",
		"determine yourself",
		"questions array",
	} {
		if !strings.Contains(x.Description(), phrase) {
			t.Fatalf("Description() missing %q: %q", phrase, x.Description())
		}
	}
	var schema map[string]any
	if e := json.Unmarshal(x.InputSchema(), &schema); e != nil {
		t.Fatal(e)
	}
}
func TestToolRunReturnsStableSubmittedJSON(t *testing.T) {
	b := NewBroker()
	x := New(b)
	go func() {
		e, _ := b.NextEvent(context.Background())
		b.Complete(e.Request.ID, Result{Results: []Result{{SelectedOptions: []SelectedOption{
			{ID: "metrics", Label: "Metrics"},
			{ID: "logs", Label: "Logs"},
		}}}})
	}()
	got, err := x.Run(context.Background(), json.RawMessage(`{"questions":[{"prompt":"Choose","mode":"multiple","options":[{"id":"logs","label":"Logs"},{"id":"metrics","label":"Metrics"}]}]}`))
	if err != nil || got != `{"results":[{"cancelled":false,"selected_options":[{"id":"metrics","label":"Metrics"},{"id":"logs","label":"Logs"}]}]}` {
		t.Fatalf("got=%s err=%v", got, err)
	}
	if strings.Contains(got, "selected_ids") {
		t.Fatalf("legacy field leaked: %s", got)
	}
}
func TestToolRunReturnsCancellationJSON(t *testing.T) {
	b := NewBroker()
	x := New(b)
	go func() {
		e, _ := b.NextEvent(context.Background())
		b.Complete(e.Request.ID, Result{Results: []Result{{Cancelled: true}}})
	}()
	got, err := x.Run(context.Background(), validSingleInput())
	if err != nil || got != `{"results":[{"cancelled":true,"selected_options":[]}]}` {
		t.Fatalf("got=%s err=%v", got, err)
	}
}
func TestToolRunBatchAnswersInOrder(t *testing.T) {
	b := NewBroker()
	x := New(b)
	go func() {
		e, _ := b.NextEvent(context.Background())
		if len(e.Request.Questions) != 2 {
			t.Errorf("question count=%d", len(e.Request.Questions))
		}
		b.Complete(e.Request.ID, Result{Results: []Result{
			{SelectedOptions: []SelectedOption{{ID: "a", Label: "A"}}},
			{SelectedOptions: []SelectedOption{{ID: "x", Label: "X"}, {ID: "y", Label: "Y"}}},
		}})
	}()
	got, err := x.Run(context.Background(), json.RawMessage(`{"questions":[{"prompt":"First","mode":"single","options":[{"id":"a","label":"A"}]},{"prompt":"Second","mode":"multiple","options":[{"id":"x","label":"X"},{"id":"y","label":"Y"}]}]}`))
	if err != nil || got != `{"results":[{"cancelled":false,"selected_options":[{"id":"a","label":"A"}]},{"cancelled":false,"selected_options":[{"id":"x","label":"X"},{"id":"y","label":"Y"}]}]}` {
		t.Fatalf("got=%s err=%v", got, err)
	}
}
func TestToolRunBatchCancelIsAtomic(t *testing.T) {
	b := NewBroker()
	x := New(b)
	go func() {
		e, _ := b.NextEvent(context.Background())
		b.Complete(e.Request.ID, Result{Results: []Result{
			{SelectedOptions: []SelectedOption{{ID: "a", Label: "A"}}},
			{Cancelled: true, SelectedOptions: []SelectedOption{}},
		}})
	}()
	got, err := x.Run(context.Background(), json.RawMessage(`{"questions":[{"prompt":"First","mode":"single","options":[{"id":"a","label":"A"}]},{"prompt":"Second","mode":"single","options":[{"id":"b","label":"B"}]}]}`))
	if err != nil || got != `{"results":[{"cancelled":true,"selected_options":[]},{"cancelled":true,"selected_options":[]}]}` {
		t.Fatalf("got=%s err=%v", got, err)
	}
}
func TestToolRunDoesNotPublishInvalidInput(t *testing.T) {
	b := NewBroker()
	_, e := New(b).Run(context.Background(), json.RawMessage(`{"questions":[{"prompt":" ","mode":"single","options":[{"id":"a","label":"A"}]}]}`))
	if e == nil || e.Error() != "questions[0]: prompt is required" {
		t.Fatal(e)
	}
	ctx, c := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer c()
	if _, e = b.NextEvent(ctx); !errors.Is(e, context.DeadlineExceeded) {
		t.Fatal(e)
	}
}
func TestToolRunReturnsContextCancellation(t *testing.T) {
	b := NewBroker()
	ctx, c := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, e := New(b).Run(ctx, validSingleInput()); done <- e }()
	_, _ = b.NextEvent(context.Background())
	c()
	if e := <-done; !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
}

func TestInputSchemaExcludesReservedCustomOptionID(t *testing.T) {
	var schema map[string]any
	if err := json.Unmarshal(New(NewBroker()).InputSchema(), &schema); err != nil {
		t.Fatal(err)
	}
	questions := schema["properties"].(map[string]any)["questions"].(map[string]any)
	items := questions["items"].(map[string]any)
	options := items["properties"].(map[string]any)["options"].(map[string]any)
	optionItems := options["items"].(map[string]any)
	id := optionItems["properties"].(map[string]any)["id"].(map[string]any)
	not := id["not"].(map[string]any)
	if not["const"] != CustomOptionID {
		t.Fatalf("id schema = %#v", id)
	}
}

func TestNormalizeResult(t *testing.T) {
	single := Request{Mode: ModeSingle, Options: []Option{{ID: "a", Label: "Canonical A"}}, MinSelect: 1, MaxSelect: 1}
	multiple := Request{Mode: ModeMultiple, Options: []Option{{ID: "a", Label: "A"}, {ID: "b", Label: "B"}}, MinSelect: 0, MaxSelect: 2}

	got, err := normalizeResult(single, Result{SelectedOptions: []SelectedOption{{ID: " a ", Label: "wrong"}}})
	if err != nil || got.SelectedOptions[0] != (SelectedOption{ID: "a", Label: "Canonical A"}) {
		t.Fatalf("canonical result=%#v err=%v", got, err)
	}
	got, err = normalizeResult(multiple, Result{})
	if err != nil || got.SelectedOptions == nil || len(got.SelectedOptions) != 0 {
		t.Fatalf("empty multiple result=%#v err=%v", got, err)
	}
	got, err = normalizeResult(single, Result{Cancelled: true, SelectedOptions: []SelectedOption{{ID: "a", Label: "A"}}})
	if err != nil || !got.Cancelled || got.SelectedOptions == nil || len(got.SelectedOptions) != 0 {
		t.Fatalf("cancelled result=%#v err=%v", got, err)
	}

	tests := []struct {
		name    string
		request Request
		result  Result
		want    string
	}{
		{"single empty", single, Result{}, "single mode requires exactly one"},
		{"single many", single, Result{SelectedOptions: []SelectedOption{{ID: "a"}, {ID: CustomOptionID, Label: "Other"}}}, "single mode requires exactly one"},
		{"unknown", single, Result{SelectedOptions: []SelectedOption{{ID: "missing", Label: "Missing"}}}, "not in the request"},
		{"duplicate", multiple, Result{SelectedOptions: []SelectedOption{{ID: "a"}, {ID: "a"}}}, "duplicate selected option id"},
		{"empty custom", multiple, Result{SelectedOptions: []SelectedOption{{ID: CustomOptionID, Label: "  "}}}, "custom option label is required"},
		{"too many", Request{Mode: ModeMultiple, Options: multiple.Options, MinSelect: 0, MaxSelect: 1}, Result{SelectedOptions: []SelectedOption{{ID: "a"}, {ID: "b"}}}, "outside allowed range"},
		{"too few", Request{Mode: ModeMultiple, Options: multiple.Options, MinSelect: 1, MaxSelect: 2}, Result{}, "outside allowed range"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := normalizeResult(tt.request, tt.result)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error=%v want substring %q", err, tt.want)
			}
		})
	}
}

func TestToolRunRejectsInvalidBrokerResult(t *testing.T) {
	b := NewBroker()
	go func() {
		e, _ := b.NextEvent(context.Background())
		b.Complete(e.Request.ID, Result{})
	}()
	_, err := New(b).Run(context.Background(), validSingleInput())
	if err == nil || !strings.Contains(err.Error(), "invalid question broker result") {
		t.Fatalf("error=%v", err)
	}
}
