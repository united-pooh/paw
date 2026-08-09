// Package config owns Paw's durable configuration, credentials, migration and
// hot-reload lifecycle. Runtime packages consume immutable snapshots produced
// here and never read configuration files directly.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"paw/internal/model"
)

const SchemaVersion = 2

const (
	TransportOpenAIResponses     = "openai-responses"
	TransportOpenAICompatible    = "openai-compatible"
	TransportAnthropicCompatible = "anthropic-compatible"
	AdapterGPT                   = "gpt"
	AdapterDeepSeek              = "deepseek"
	AdapterOpenAICompatible      = "openai-compatible"
)

var (
	ErrRevisionConflict           = errors.New("configuration revision conflict")
	ErrCredentialNotFound         = errors.New("credential not found")
	ErrCredentialStoreUnavailable = errors.New("credential store unavailable")
	ErrCredentialMigrationBlocked = errors.New("credential migration blocked")
)

// Document is the global config-v2 contract. Unknown fields and comments are
// retained by Manager.Raw rather than being discarded on targeted updates.
type Document struct {
	Schema        string              `json:"$schema,omitempty"`
	SchemaVersion int                 `json:"schemaVersion"`
	ActiveModel   string              `json:"activeModel,omitempty"`
	Providers     map[string]Provider `json:"providers"`
	Models        map[string]Model    `json:"models"`
}

type Provider struct {
	Preset         string            `json:"preset,omitempty"`
	Transport      string            `json:"transport,omitempty"`
	Endpoint       string            `json:"endpoint,omitempty"`
	APIPath        string            `json:"apiPath,omitempty"`
	Auth           Auth              `json:"auth,omitempty"`
	Headers        map[string]string `json:"headers,omitempty"`
	Body           map[string]any    `json:"body,omitempty"`
	TimeoutSeconds int               `json:"timeoutSeconds,omitempty"`
	Retries        *int              `json:"retries,omitempty"`
	Stream         *bool             `json:"stream,omitempty"`
}

type Auth struct {
	Credential string   `json:"credential,omitempty"`
	Env        []string `json:"env,omitempty"`
}

type Model struct {
	Provider      string         `json:"provider"`
	Name          string         `json:"name"`
	Adapter       string         `json:"adapter,omitempty"`
	ContextWindow int            `json:"contextWindow,omitempty"`
	Stream        *bool          `json:"stream,omitempty"`
	Capabilities  Capabilities   `json:"capabilities,omitempty"`
	Parameters    map[string]any `json:"parameters,omitempty"`
}

type Capabilities struct {
	Tools      *bool `json:"tools,omitempty"`
	Vision     *bool `json:"vision,omitempty"`
	Reasoning  *bool `json:"reasoning,omitempty"`
	Attachment *bool `json:"attachment,omitempty"`
}

// WorkspaceDocument is deliberately narrow. A workspace cannot override
// provider connectivity or credentials inherited from the user's config.
type WorkspaceDocument struct {
	SchemaVersion int                      `json:"schemaVersion,omitempty"`
	ActiveModel   string                   `json:"activeModel,omitempty"`
	Models        map[string]ModelOverride `json:"models,omitempty"`
}

type ModelOverride struct {
	Stream     *bool          `json:"stream,omitempty"`
	Parameters map[string]any `json:"parameters,omitempty"`
}

type Diagnostic struct {
	Severity string `json:"severity"`
	File     string `json:"file,omitempty"`
	Field    string `json:"field,omitempty"`
	Message  string `json:"message"`
}

type Snapshot struct {
	Document      Document
	Workspace     WorkspaceDocument
	Active        model.Config
	ActiveModelID string
	Revision      uint64
	ContentHash   string
	Diagnostics   []Diagnostic
	Ready         bool
	LoadedAt      time.Time
	Raw           []byte
}

func (s Snapshot) Clone() Snapshot {
	s.Document = cloneDocument(s.Document)
	s.Workspace = cloneWorkspace(s.Workspace)
	s.Active = model.CloneConfig(s.Active)
	s.Diagnostics = append([]Diagnostic(nil), s.Diagnostics...)
	s.Raw = append([]byte(nil), s.Raw...)
	return s
}

type OperationKind string

const (
	OperationSetActiveModel OperationKind = "set-active-model"
	OperationUpsertProvider OperationKind = "upsert-provider"
	OperationDeleteProvider OperationKind = "delete-provider"
	OperationUpsertModel    OperationKind = "upsert-model"
	OperationDeleteModel    OperationKind = "delete-model"
)

// Operation is a small optimistic-concurrency patch. Value must encode the
// type named by Kind; helpers below are preferred by callers.
type Operation struct {
	Kind  OperationKind
	ID    string
	Value json.RawMessage
}

func SetActiveModel(id string) Operation {
	raw, _ := json.Marshal(strings.TrimSpace(id))
	return Operation{Kind: OperationSetActiveModel, Value: raw}
}

func UpsertProvider(id string, provider Provider) Operation {
	raw, _ := json.Marshal(provider)
	return Operation{Kind: OperationUpsertProvider, ID: strings.TrimSpace(id), Value: raw}
}

func DeleteProvider(id string) Operation {
	return Operation{Kind: OperationDeleteProvider, ID: strings.TrimSpace(id)}
}

func UpsertModel(id string, value Model) Operation {
	raw, _ := json.Marshal(value)
	return Operation{Kind: OperationUpsertModel, ID: strings.TrimSpace(id), Value: raw}
}

func DeleteModel(id string) Operation {
	return Operation{Kind: OperationDeleteModel, ID: strings.TrimSpace(id)}
}

type SetupRequiredError struct {
	Path       string
	Reason     string
	Candidates []string
}

func (e *SetupRequiredError) Error() string {
	if e == nil {
		return "Paw setup required"
	}
	message := strings.TrimSpace(e.Reason)
	if message == "" {
		message = "model provider setup is incomplete"
	}
	return fmt.Sprintf("Paw setup required: %s (config: %s; run `paw`, then enter `/config`)", message, e.Path)
}

func cloneDocument(in Document) Document {
	out := in
	out.Providers = make(map[string]Provider, len(in.Providers))
	for id, value := range in.Providers {
		value.Headers = cloneStringMap(value.Headers)
		value.Body = cloneAnyMap(value.Body)
		value.Auth.Env = append([]string(nil), value.Auth.Env...)
		if value.Retries != nil {
			retries := *value.Retries
			value.Retries = &retries
		}
		value.Stream = cloneBoolPointer(value.Stream)
		out.Providers[id] = value
	}
	out.Models = make(map[string]Model, len(in.Models))
	for id, value := range in.Models {
		value.Parameters = cloneAnyMap(value.Parameters)
		value.Stream = cloneBoolPointer(value.Stream)
		value.Capabilities.Tools = cloneBoolPointer(value.Capabilities.Tools)
		value.Capabilities.Vision = cloneBoolPointer(value.Capabilities.Vision)
		value.Capabilities.Reasoning = cloneBoolPointer(value.Capabilities.Reasoning)
		value.Capabilities.Attachment = cloneBoolPointer(value.Capabilities.Attachment)
		out.Models[id] = value
	}
	return out
}

func cloneWorkspace(in WorkspaceDocument) WorkspaceDocument {
	out := in
	out.Models = make(map[string]ModelOverride, len(in.Models))
	for id, value := range in.Models {
		value.Parameters = cloneAnyMap(value.Parameters)
		value.Stream = cloneBoolPointer(value.Stream)
		out.Models[id] = value
	}
	return out
}

func cloneBoolPointer(value *bool) *bool {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneStringMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func cloneAnyMap(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	raw, err := json.Marshal(in)
	if err != nil {
		return nil
	}
	var out map[string]any
	if json.Unmarshal(raw, &out) != nil {
		return nil
	}
	return out
}
