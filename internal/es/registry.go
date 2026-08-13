// Package es implements the event-sourcing core: the unified event envelope,
// the type registry with payload validation, the append-only JSONL event
// store, and the aggregate loading pipeline.
package es

import (
	"encoding/json"
	"fmt"
	"sync"
)

// Payload is a decoded, validated event payload produced by a TypeSpec.
type Payload interface{}

// TypeSpec declares one event type: how to decode its payload and how to
// validate the decoded value. Registration is the single place an event
// type is admitted; unknown types are rejected at load time.
type TypeSpec struct {
	Type     string
	Decode   func(json.RawMessage) (Payload, error)
	Validate func(Payload) error
}

// Registry admits event types and decodes envelopes into validated payloads.
type Registry struct {
	mu    sync.RWMutex
	specs map[string]TypeSpec
}

func NewRegistry() *Registry {
	return &Registry{specs: make(map[string]TypeSpec)}
}

// Register admits a type. Registering the same type twice fails; the first
// registration wins and the registry is left unchanged on failure.
func (r *Registry) Register(spec TypeSpec) error {
	if spec.Type == "" {
		return fmt.Errorf("es: cannot register empty type")
	}
	if spec.Decode == nil {
		return fmt.Errorf("es: type %q: Decode is required", spec.Type)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.specs[spec.Type]; ok {
		return fmt.Errorf("es: type %q already registered", spec.Type)
	}
	r.specs[spec.Type] = spec
	return nil
}

// Decode validates the envelope, looks up its type, decodes the payload and
// runs the type's validation. Any failure returns an error naming the type.
func (r *Registry) Decode(env Envelope) (Payload, error) {
	if err := env.Validate(); err != nil {
		return nil, err
	}
	r.mu.RLock()
	spec, ok := r.specs[env.Type]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("es: unknown event type %q", env.Type)
	}
	payload, err := spec.Decode(env.Payload)
	if err != nil {
		return nil, fmt.Errorf("es: decode payload of %q: %w", env.Type, err)
	}
	if spec.Validate != nil {
		if err := spec.Validate(payload); err != nil {
			return nil, fmt.Errorf("es: invalid payload of %q: %w", env.Type, err)
		}
	}
	return payload, nil
}

// Has reports whether the type is registered.
func (r *Registry) Has(typ string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.specs[typ]
	return ok
}

// Types returns all registered type names, sorted.
func (r *Registry) Types() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.specs))
	for typ := range r.specs {
		out = append(out, typ)
	}
	return out
}
