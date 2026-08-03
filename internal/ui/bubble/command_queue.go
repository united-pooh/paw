package bubble

import (
	"fmt"
	"strings"
	"time"
)

// queuedChatItem is an addressable rich chat draft waiting for a later turn.
// The ID remains stable while the item is selected, reordered, edited, or
// restored; Draft is cloned at every queue boundary.
type queuedChatItem struct {
	ID        string
	Draft     inputDraft
	CreatedAt time.Time
}

// CommandQueue stores ordinary chat submissions that arrive while a model turn
// is already running. It is intentionally a small, synchronous FIFO store;
// Bubble Tea owns all mutations on its event loop.
type CommandQueue struct {
	items  []queuedChatItem
	nextID uint64
}

func cloneQueuedChatItem(item queuedChatItem) queuedChatItem {
	item.Draft = cloneInputDraft(item.Draft)
	return item
}

// Items returns a defensive snapshot of the pending queue.
func (q *CommandQueue) Items() []queuedChatItem {
	if q == nil || len(q.items) == 0 {
		return nil
	}
	items := make([]queuedChatItem, len(q.items))
	for i, item := range q.items {
		items[i] = cloneQueuedChatItem(item)
	}
	return items
}

// Enqueue appends a non-empty plain-text chat input and returns its stable ID.
func (q *CommandQueue) Enqueue(input string) (string, bool) {
	return q.EnqueueDraft(inputDraft{Text: input})
}

// EnqueueDraft preserves image/token metadata while trimming and cloning the
// submitted draft before it enters the queue.
func (q *CommandQueue) EnqueueDraft(draft inputDraft) (string, bool) {
	if q == nil {
		return "", false
	}
	draft = trimInputDraft(cloneInputDraft(draft))
	if strings.TrimSpace(draft.Text) == "" {
		return "", false
	}
	q.nextID++
	item := queuedChatItem{
		ID:        fmt.Sprintf("queue-%d", q.nextID),
		Draft:     draft,
		CreatedAt: time.Now(),
	}
	q.items = append(q.items, item)
	return item.ID, true
}

// Dequeue removes and returns the oldest queued chat input.
func (q *CommandQueue) Dequeue() (string, bool) {
	item, ok := q.DequeueDraft()
	if !ok {
		return "", false
	}
	return item.Draft.Text, true
}

// DequeueDraft removes the oldest queued input with its stable identity and
// rich metadata.
func (q *CommandQueue) DequeueDraft() (queuedChatItem, bool) {
	if q == nil || len(q.items) == 0 {
		return queuedChatItem{}, false
	}
	item := q.items[0]
	copy(q.items, q.items[1:])
	q.items[len(q.items)-1] = queuedChatItem{}
	q.items = q.items[:len(q.items)-1]
	return cloneQueuedChatItem(item), true
}

// RemoveAt removes and returns the item at index.
func (q *CommandQueue) RemoveAt(index int) (queuedChatItem, bool) {
	if q == nil || index < 0 || index >= len(q.items) {
		return queuedChatItem{}, false
	}
	item := q.items[index]
	copy(q.items[index:], q.items[index+1:])
	q.items[len(q.items)-1] = queuedChatItem{}
	q.items = q.items[:len(q.items)-1]
	return cloneQueuedChatItem(item), true
}

// Remove removes an item by stable ID.
func (q *CommandQueue) Remove(id string) (queuedChatItem, bool) {
	for i, item := range q.items {
		if item.ID == id {
			return q.RemoveAt(i)
		}
	}
	return queuedChatItem{}, false
}

// Move shifts an item by delta positions. Out-of-range moves are rejected.
func (q *CommandQueue) Move(id string, delta int) bool {
	if q == nil || delta == 0 {
		return false
	}
	index := -1
	for i, item := range q.items {
		if item.ID == id {
			index = i
			break
		}
	}
	if index < 0 {
		return false
	}
	target := index + delta
	if target < 0 || target >= len(q.items) {
		return false
	}
	item := q.items[index]
	if delta < 0 {
		copy(q.items[target+1:index+1], q.items[target:index])
	} else {
		copy(q.items[index:target], q.items[index+1:target+1])
	}
	q.items[target] = item
	return true
}

// InsertAt inserts a cloned item at a clamped position. Empty/invalid IDs are
// rejected so restored queue items remain addressable.
func (q *CommandQueue) InsertAt(index int, item queuedChatItem) bool {
	if q == nil || strings.TrimSpace(item.ID) == "" || strings.TrimSpace(item.Draft.Text) == "" {
		return false
	}
	for _, existing := range q.items {
		if existing.ID == item.ID {
			return false
		}
	}
	if index < 0 {
		index = 0
	}
	if index > len(q.items) {
		index = len(q.items)
	}
	item = cloneQueuedChatItem(item)
	q.items = append(q.items, queuedChatItem{})
	copy(q.items[index+1:], q.items[index:])
	q.items[index] = item
	return true
}

// Len returns the number of queued chat inputs.
func (q *CommandQueue) Len() int {
	if q == nil {
		return 0
	}
	return len(q.items)
}

// Clear discards all queued chat inputs while retaining the monotonic ID
// counter, preventing stale UI references from becoming valid again.
func (q *CommandQueue) Clear() {
	if q == nil {
		return
	}
	for i := range q.items {
		q.items[i] = queuedChatItem{}
	}
	q.items = nil
}
