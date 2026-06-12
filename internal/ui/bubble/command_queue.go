package bubble

import "strings"

// CommandQueue stores ordinary chat submissions that arrive while a model turn
// is already running.
type CommandQueue struct {
	items []string
}

// Enqueue appends a non-empty chat input to the queue.
func (q *CommandQueue) Enqueue(input string) bool {
	input = strings.TrimSpace(input)
	if input == "" {
		return false
	}
	q.items = append(q.items, input)
	return true
}

// Dequeue removes and returns the oldest queued chat input.
func (q *CommandQueue) Dequeue() (string, bool) {
	if q == nil || len(q.items) == 0 {
		return "", false
	}
	input := q.items[0]
	copy(q.items, q.items[1:])
	q.items[len(q.items)-1] = ""
	q.items = q.items[:len(q.items)-1]
	return input, true
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
}
