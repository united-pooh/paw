package actor

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"paw/internal/es"
)

type memoryStreamStore struct {
	mu        sync.Mutex
	events    map[string][]es.Envelope
	snapshots map[string]es.Snapshot
}

func newMemoryStreamStore() *memoryStreamStore {
	return &memoryStreamStore{events: map[string][]es.Envelope{}, snapshots: map[string]es.Snapshot{}}
}

func (s *memoryStreamStore) Append(_ context.Context, id string, events []es.Envelope) (int64, int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	first := int64(len(s.events[id]) + 1)
	for i := range events {
		event := events[i]
		event.Seq = first + int64(i)
		s.events[id] = append(s.events[id], event)
	}
	return first, first + int64(len(events)) - 1, nil
}

func (s *memoryStreamStore) Load(_ context.Context, id string) ([]es.Envelope, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]es.Envelope(nil), s.events[id]...), false, nil
}

func (s *memoryStreamStore) WriteSnapshot(_ context.Context, id string, seq int64, state json.RawMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snapshots[id] = es.Snapshot{Seq: seq, State: append(json.RawMessage(nil), state...)}
	return nil
}

func (s *memoryStreamStore) ReadSnapshot(_ context.Context, id string) (es.Snapshot, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	snapshot, ok := s.snapshots[id]
	return snapshot, ok, nil
}

type streamStoreActor struct {
	id    ActorID
	count int
}

type suspendingStreamActor struct {
	id ActorID
}

func (a *suspendingStreamActor) ID() ActorID { return a.id }
func (a *suspendingStreamActor) Receive(ctx *Context, msg Msg) {
	if msg.Kind == "hold" {
		_ = ctx.Persist("approval.requested", map[string]string{"id": "d1"}, Durable)
		_ = ctx.Suspend("approval required")
	}
}
func (a *suspendingStreamActor) Fold(es.Envelope) error { return nil }
func (a *suspendingStreamActor) Snapshot() (json.RawMessage, error) {
	return json.RawMessage(`{}`), nil
}
func (a *suspendingStreamActor) Restore(json.RawMessage) error { return nil }

func (a *streamStoreActor) ID() ActorID { return a.id }
func (a *streamStoreActor) Receive(ctx *Context, msg Msg) {
	if msg.Kind == "increment" {
		a.count++
		_ = ctx.Persist("counter.incremented", map[string]int{"count": a.count}, Durable)
	}
}
func (a *streamStoreActor) Fold(env es.Envelope) error {
	if env.Type == "counter.incremented" {
		var payload map[string]int
		if err := json.Unmarshal(env.Payload, &payload); err != nil {
			return err
		}
		a.count = payload["count"]
	}
	return nil
}
func (a *streamStoreActor) Snapshot() (json.RawMessage, error) { return json.Marshal(a.count) }
func (a *streamStoreActor) Restore(raw json.RawMessage) error  { return json.Unmarshal(raw, &a.count) }

func TestSystemUsesInjectedStreamStoreForActorType(t *testing.T) {
	store := newMemoryStreamStore()
	newSystem := func() *System {
		system := NewSystem(t.TempDir(), WithShards(1), WithStreamStore("session", store))
		system.Register("session", func(id ActorID) Actor { return &streamStoreActor{id: id} })
		return system
	}

	id := ActorID{Type: "session", Key: "s1"}
	system := newSystem()
	if err := system.Tell(context.Background(), id, Msg{MsgID: "m1", Kind: "increment", Durability: Durable}); err != nil {
		t.Fatalf("Tell: %v", err)
	}
	system.Drain()
	system.Stop()

	events, _, err := store.Load(context.Background(), "s1")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	var foundRuntime, foundDomain bool
	for _, event := range events {
		foundRuntime = foundRuntime || event.Kind == es.KindRuntime
		foundDomain = foundDomain || event.Type == "counter.incremented" && event.Kind == es.KindDomain
	}
	if !foundRuntime || !foundDomain {
		t.Fatalf("events missing runtime/domain entries: %+v", events)
	}

	restarted := newSystem()
	if err := restarted.Tell(context.Background(), id, Msg{MsgID: "m2", Kind: "increment", Durability: Durable}); err != nil {
		t.Fatalf("Tell after restart: %v", err)
	}
	restarted.Drain()
	restarted.Stop()
	events, _, _ = store.Load(context.Background(), "s1")
	if got := len(events); got < 6 {
		t.Fatalf("event count after restart = %d, want runtime and domain continuation", got)
	}
}

func TestResumeActivatesPersistedSuspendedActor(t *testing.T) {
	store := newMemoryStreamStore()
	newSystem := func() *System {
		system := NewSystem(t.TempDir(), WithShards(1), WithStreamStore("session", store))
		system.Register("session", func(id ActorID) Actor { return &suspendingStreamActor{id: id} })
		return system
	}
	id := ActorID{Type: "session", Key: "s1"}
	system := newSystem()
	if err := system.Tell(context.Background(), id, Msg{MsgID: "hold", Kind: "hold", Durability: Durable}); err != nil {
		t.Fatalf("Tell: %v", err)
	}
	system.Drain()
	system.Stop()

	restarted := newSystem()
	if err := restarted.Resume(id); err != nil {
		t.Fatalf("Resume inactive persisted actor: %v", err)
	}
	restarted.Drain()
	restarted.Stop()
	events, _, _ := store.Load(context.Background(), "s1")
	if events[len(events)-1].Type != sysResumed {
		t.Fatalf("last event = %s, want %s", events[len(events)-1].Type, sysResumed)
	}
}
