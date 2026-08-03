package bubble

import "testing"

func TestCommandQueueFIFOAndSkipsEmpty(t *testing.T) {
	var queue CommandQueue
	if _, ok := queue.Enqueue("  "); ok {
		t.Fatalf("empty input was enqueued")
	}
	queue.Enqueue("first")
	queue.Enqueue("second")

	if got := queue.Len(); got != 2 {
		t.Fatalf("Len() = %d, want 2", got)
	}
	first, ok := queue.Dequeue()
	if !ok || first != "first" {
		t.Fatalf("first Dequeue() = %q/%v", first, ok)
	}
	second, ok := queue.Dequeue()
	if !ok || second != "second" {
		t.Fatalf("second Dequeue() = %q/%v", second, ok)
	}
	if _, ok := queue.Dequeue(); ok {
		t.Fatalf("empty Dequeue() ok = true")
	}
}

func TestCommandQueueClear(t *testing.T) {
	var queue CommandQueue
	queue.Enqueue("first")
	queue.Enqueue("second")
	queue.Clear()
	if got := queue.Len(); got != 0 {
		t.Fatalf("Len() after Clear = %d, want 0", got)
	}
}
