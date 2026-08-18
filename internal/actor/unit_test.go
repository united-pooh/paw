package actor

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"paw/internal/es"
)

// testEnvelope 构造一条运行时事件（账本单测用）。
func testEnvelope(typ string, payload any) es.Envelope {
	return es.Envelope{Seq: 1, Type: typ, OccurredAt: time.Unix(0, 1), SchemaVersion: 1,
		Kind: es.KindRuntime, Payload: mustJSON(payload)}
}

func TestActorIDValidate(t *testing.T) {
	ok := []ActorID{{Type: "task", Key: "abc"}, {Type: "t", Key: "k-1_2"}}
	for _, id := range ok {
		if err := id.Validate(); err != nil {
			t.Fatalf("valid id rejected: %v", err)
		}
	}
	bad := []ActorID{
		{}, {Type: "t"}, {Key: "k"},
		{Type: "a/b", Key: "k"}, {Type: "a", Key: "../etc"}, {Type: "..", Key: "x"},
		{Type: string(make([]byte, 300)), Key: "k"},
	}
	for _, id := range bad {
		if err := id.Validate(); err == nil {
			t.Fatalf("unsafe id accepted: %+v", id)
		}
	}
}

func TestMailboxBound(t *testing.T) {
	m := newMailbox(2)
	if err := m.push(delivery{}); err != nil {
		t.Fatal(err)
	}
	if err := m.push(delivery{}); err != nil {
		t.Fatal(err)
	}
	if err := m.push(delivery{}); err != ErrMailboxFull {
		t.Fatalf("want ErrMailboxFull, got %v", err)
	}
	if m.len() != 2 {
		t.Fatalf("len = %d", m.len())
	}
	if _, ok := m.pop(); !ok {
		t.Fatal("pop failed")
	}
	m.close()
	if err := m.push(delivery{}); err != ErrStopped {
		t.Fatalf("closed mailbox push = %v", err)
	}
}

func TestVirtualClockOrdering(t *testing.T) {
	vc := NewVirtualClock()
	var fired []string
	vc.After(30*time.Millisecond, func() { fired = append(fired, "30") })
	vc.After(10*time.Millisecond, func() { fired = append(fired, "10") })
	cancel := vc.After(10*time.Millisecond, func() { fired = append(fired, "10-cancelled") })
	cancel()
	vc.After(10*time.Millisecond, func() { fired = append(fired, "10-late-reg") }) // 同刻按注册序：在 "10" 后
	vc.Advance(100 * time.Millisecond)
	want := []string{"10", "10-late-reg", "30"}
	if len(fired) != len(want) {
		t.Fatalf("fired = %v", fired)
	}
	for i := range want {
		if fired[i] != want[i] {
			t.Fatalf("fired = %v, want %v", fired, want)
		}
	}
	if len(vc.PendingTimers()) != 0 {
		t.Fatalf("pending = %v", vc.PendingTimers())
	}
}

func TestVirtualClockNow(t *testing.T) {
	vc := NewVirtualClock()
	if !vc.Now().Equal(time.Unix(0, 0)) {
		t.Fatal("epoch start expected")
	}
	vc.Advance(5 * time.Second)
	if vc.Now().Sub(time.Unix(0, 0)) != 5*time.Second {
		t.Fatalf("now = %v", vc.Now())
	}
}

func TestMsgMarshalRoundtrip(t *testing.T) {
	encoded, err := marshalMsg(Msg{MsgID: "m1", Kind: "k", Payload: map[string]int{"n": 3}, Durability: Buffered})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeMsgJSON(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.MsgID != "m1" || decoded.Kind != "k" || decoded.Durability != Buffered {
		t.Fatalf("decoded = %+v", decoded)
	}
	var p map[string]int
	if err := json.Unmarshal(decoded.Payload.(json.RawMessage), &p); err != nil || p["n"] != 3 {
		t.Fatalf("payload = %v err=%v", decoded.Payload, err)
	}
}

func TestLedgerFoldRoundtrip(t *testing.T) {
	l := newRuntimeLedger()
	encoded, _ := marshalMsg(Msg{MsgID: "m1", Kind: "inc"})
	_ = l.foldRuntime(testEnvelope(sysInboxReceived, inboxReceivedPayload{MsgID: "m1", Msg: encoded}))
	_ = l.foldRuntime(testEnvelope(sysOutboxSent, outboxSentPayload{MsgID: "o1", Target: "sink/s1", Msg: encoded}))
	_ = l.foldRuntime(testEnvelope(sysOutboxDelivered, outboxDeliveredPayload{MsgID: "o1"}))
	_ = l.foldRuntime(testEnvelope(sysTimerRegistered, timerRegisteredPayload{TimerID: "t1", AfterMs: 5, Msg: encoded}))
	_ = l.foldRuntime(testEnvelope(sysSuspended, map[string]string{"reason": "r"}))
	if len(l.pendingInbox()) != 1 {
		t.Fatalf("pending = %v", l.pendingInbox())
	}
	if len(l.outbox) != 0 {
		t.Fatalf("outbox = %v", l.outbox)
	}
	if len(l.timers) != 1 {
		t.Fatalf("timers = %v", l.timers)
	}
	if !l.suspended || l.suspendReason != "r" {
		t.Fatalf("suspended = %v %q", l.suspended, l.suspendReason)
	}
	_ = l.foldRuntime(testEnvelope(sysInboxDone, inboxDonePayload{MsgID: "m1"}))
	_ = l.foldRuntime(testEnvelope(sysTimerFired, timerFiredPayload{TimerID: "t1"}))
	_ = l.foldRuntime(testEnvelope(sysResumed, map[string]string{}))
	if !l.isDone("m1") || len(l.pendingInbox()) != 0 {
		t.Fatal("done/dedup state wrong")
	}
	if len(l.timers) != 0 || l.suspended {
		t.Fatal("timer/resume state wrong")
	}
}

func TestLedgerUnknownSysTypeIgnored(t *testing.T) {
	l := newRuntimeLedger()
	if err := l.foldRuntime(testEnvelope("sys.future.event", map[string]string{})); err != nil {
		t.Fatalf("unknown sys event: %v", err)
	}
	if l.lastSeq == 0 {
		t.Fatal("seq should advance")
	}
}

func TestParseActorID(t *testing.T) {
	if got := parseActorID("sink/s1"); got.Key != "s1" || got.Type != "sink" {
		t.Fatalf("parse = %+v", got)
	}
	if got := parseActorID("bare"); got.Type != "bare" || got.Key != "" {
		t.Fatalf("parse = %+v", got)
	}
}

func TestJournalRejectsInvalidID(t *testing.T) {
	j := newJournal(t.TempDir(), RealClock{})
	if _, err := j.appendSys(context.Background(), ActorID{Type: "../x", Key: "k"}, sysSuspended, map[string]string{}); err == nil {
		t.Fatal("unsafe id accepted")
	}
}
