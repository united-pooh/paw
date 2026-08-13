package es

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

type pausedPayload struct {
	Reason    string `json:"reason"`
	TurnCount int    `json:"turn_count"`
}

func pausedSpec() TypeSpec {
	return TypeSpec{
		Type: "goal.paused",
		Decode: func(raw json.RawMessage) (Payload, error) {
			var p pausedPayload
			if err := json.Unmarshal(raw, &p); err != nil {
				return nil, err
			}
			return p, nil
		},
		Validate: func(p Payload) error {
			pp, ok := p.(pausedPayload)
			if !ok {
				return errors.New("payload type mismatch")
			}
			if pp.Reason == "" {
				return errors.New("reason is required")
			}
			if pp.TurnCount < 0 {
				return errors.New("turn_count must not be negative")
			}
			return nil
		},
	}
}

func testEnvelope(seq int64, typ string, payload string) Envelope {
	return Envelope{
		Seq:           seq,
		Type:          typ,
		OccurredAt:    time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC),
		SchemaVersion: 1,
		Payload:       json.RawMessage(payload),
	}
}

func TestRegistryDecodeOK(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(pausedSpec()); err != nil {
		t.Fatalf("register: %v", err)
	}
	p, err := r.Decode(testEnvelope(1, "goal.paused", `{"reason":"no_progress","turn_count":7}`))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	pp, ok := p.(pausedPayload)
	if !ok || pp.Reason != "no_progress" || pp.TurnCount != 7 {
		t.Fatalf("decoded payload mismatch: %#v", p)
	}
}

func TestRegistryRegisterDuplicate(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(pausedSpec()); err != nil {
		t.Fatalf("first register: %v", err)
	}
	if err := r.Register(pausedSpec()); err == nil {
		t.Fatal("duplicate register must fail")
	}
}

func TestRegistryDecodeUnknownType(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(pausedSpec()); err != nil {
		t.Fatalf("register: %v", err)
	}
	if _, err := r.Decode(testEnvelope(1, "goal.unknown", `{}`)); err == nil {
		t.Fatal("unknown type must be rejected")
	}
}

func TestRegistryDecodeMalformedPayload(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(pausedSpec()); err != nil {
		t.Fatalf("register: %v", err)
	}
	if _, err := r.Decode(testEnvelope(1, "goal.paused", `{not-json`)); err == nil {
		t.Fatal("malformed payload must be rejected")
	}
}

func TestRegistryDecodeValidateFail(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(pausedSpec()); err != nil {
		t.Fatalf("register: %v", err)
	}
	if _, err := r.Decode(testEnvelope(1, "goal.paused", `{"reason":"","turn_count":7}`)); err == nil {
		t.Fatal("empty reason must fail validation")
	}
	if _, err := r.Decode(testEnvelope(1, "goal.paused", `{"reason":"no_progress","turn_count":-1}`)); err == nil {
		t.Fatal("negative turn_count must fail validation")
	}
}

func TestDecodeEnvelopeInvalid(t *testing.T) {
	cases := []struct {
		name string
		env  Envelope
	}{
		{"empty type", Envelope{Seq: 1, Type: "", OccurredAt: time.Now(), SchemaVersion: 1, Payload: json.RawMessage(`{}`)}},
		{"zero seq", Envelope{Seq: 0, Type: "goal.paused", OccurredAt: time.Now(), SchemaVersion: 1, Payload: json.RawMessage(`{}`)}},
		{"negative seq", Envelope{Seq: -1, Type: "goal.paused", OccurredAt: time.Now(), SchemaVersion: 1, Payload: json.RawMessage(`{}`)}},
		{"missing occurred_at for v1", Envelope{Seq: 1, Type: "goal.paused", OccurredAt: time.Time{}, SchemaVersion: 1, Payload: json.RawMessage(`{}`)}},
		{"negative schema version", Envelope{Seq: 1, Type: "goal.paused", OccurredAt: time.Now(), SchemaVersion: -1, Payload: json.RawMessage(`{}`)}},
		{"payload not object", Envelope{Seq: 1, Type: "goal.paused", OccurredAt: time.Now(), SchemaVersion: 1, Payload: json.RawMessage(`[1,2]`)}},
		{"payload null", Envelope{Seq: 1, Type: "goal.paused", OccurredAt: time.Now(), SchemaVersion: 1, Payload: json.RawMessage(`null`)}},
		{"payload empty", Envelope{Seq: 1, Type: "goal.paused", OccurredAt: time.Now(), SchemaVersion: 1, Payload: nil}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.env.Validate(); err == nil {
				t.Fatalf("envelope must be rejected: %+v", tc.env)
			}
		})
	}
}

func TestDecodeEnvelopeLegacyZeroOccurredAt(t *testing.T) {
	env := Envelope{Seq: 1, Type: "message", OccurredAt: time.Time{}, SchemaVersion: 0, Payload: json.RawMessage(`{}`)}
	if err := env.Validate(); err != nil {
		t.Fatalf("legacy envelope (schema_version=0, zero occurred_at) must be accepted: %v", err)
	}
}

func TestDecodeEnvelopeAllowed(t *testing.T) {
	env := Envelope{Seq: 1, Type: "goal.paused", OccurredAt: time.Now(), SchemaVersion: 1, Payload: json.RawMessage(`{}`)}
	if err := env.Validate(); err != nil {
		t.Fatalf("valid envelope rejected: %v", err)
	}
}

func TestRegistryDecodeRejectsInvalidEnvelope(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(pausedSpec()); err != nil {
		t.Fatalf("register: %v", err)
	}
	env := testEnvelope(1, "goal.paused", `{"reason":"no_progress"}`)
	env.Seq = 0
	if _, err := r.Decode(env); err == nil {
		t.Fatal("invalid envelope must be rejected before payload decode")
	}
}

func TestRegistryErrorsAreActionable(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(pausedSpec()); err != nil {
		t.Fatalf("register: %v", err)
	}
	_, err := r.Decode(testEnvelope(1, "goal.nope", `{}`))
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "goal.nope") {
		t.Fatalf("unknown type error must name the type: %v", err)
	}
}
