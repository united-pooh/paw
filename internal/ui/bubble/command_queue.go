package bubble

import "strings"

// CommandQueue stores ordinary chat submissions that arrive while a model turn
// is already running.
type CommandQueue struct {
	items  []string
	drafts []inputDraft
}

// Enqueue appends a non-empty chat input to the queue.
func (q *CommandQueue) Enqueue(input string) bool {
	return q.EnqueueDraft(inputDraft{Text: input})
}

// EnqueueDraft preserves image token metadata while retaining the queue's
// existing string-oriented compatibility methods.
func (q *CommandQueue) EnqueueDraft(draft inputDraft) bool {
	draft = trimInputDraft(cloneInputDraft(draft))
	if strings.TrimSpace(draft.Text) == "" {
		return false
	}
	q.items = append(q.items, draft.Text)
	q.drafts = append(q.drafts, draft)
	return true
}

// Dequeue removes and returns the oldest queued chat input.
func (q *CommandQueue) Dequeue() (string, bool) {
	draft, ok := q.DequeueDraft()
	if !ok {
		return "", false
	}
	return draft.Text, true
}

// DequeueDraft removes the oldest queued input with its rich metadata.
func (q *CommandQueue) DequeueDraft() (inputDraft, bool) {
	if q == nil || len(q.drafts) == 0 {
		return inputDraft{}, false
	}
	draft := q.drafts[0]
	copy(q.drafts, q.drafts[1:])
	q.drafts[len(q.drafts)-1] = inputDraft{}
	q.drafts = q.drafts[:len(q.drafts)-1]
	if len(q.items) > 0 {
		copy(q.items, q.items[1:])
		q.items[len(q.items)-1] = ""
		q.items = q.items[:len(q.items)-1]
	}
	return draft, true
}

// Len returns the number of queued chat inputs.
func (q *CommandQueue) Len() int {
	if q == nil {
		return 0
	}
	return len(q.items)
}

// Clear discards all queued chat inputs.
func (q *CommandQueue) Clear() {
	if q == nil {
		return
	}
	for i := range q.items {
		q.items[i] = ""
	}
	q.items = nil
	q.drafts = nil
}
