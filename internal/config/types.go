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
	DiscoveryFormatOpenAIList    = "openai-list"
	DiscoveryFormatOllamaTags    = "ollama-tags"
	DiscoveryModeMerge           = "merge"
	DiscoveryModeReplace         = "replace"
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
	Proxy         *model.ProxyConfig  `json:"proxy,omitempty"`
	Providers     map[string]Provider `json:"providers"`
	Models        map[string]Model    `json:"models"`
}

type Provider struct {
	Preset         string             `json:"preset,omitempty"`
	Transport      string             `json:"transport,omitempty"`
	Endpoint       string             `json:"endpoint,omitempty"`
	APIPath        string             `json:"apiPath,omitempty"`
	Auth           Auth               `json:"auth,omitempty"`
	Headers        map[string]string  `json:"headers,omitempty"`
	Body           map[string]any     `json:"body,omitempty"`
	TimeoutSeconds int                `json:"timeoutSeconds,omitempty"`
	Retries        *int               `json:"retries,omitempty"`
	Stream         *bool              `json:"stream,omitempty"`
	Proxy          *model.ProxyConfig `json:"proxy,omitempty"`
	Discovery      *DiscoveryConfig   `json:"discovery,omitempty"`
}

type DiscoveryConfig struct {
	Enabled        *bool    `json:"enabled,omitempty"`
	Path           string   `json:"path,omitempty"`
	PathSet        bool     `json:"-"`
	Format         string   `json:"format,omitempty"`
	TimeoutSeconds int      `json:"timeoutSeconds,omitempty"`
	Include        []string `json:"include,omitempty"`
	Exclude        []string `json:"exclude,omitempty"`
	Mode           string   `json:"mode,omitempty"`
}

func (value DiscoveryConfig) MarshalJSON() ([]byte, error) {
	type encodedDiscoveryConfig struct {
		Enabled        *bool     `json:"enabled,omitempty"`
		Path           *string   `json:"path,omitempty"`
		Format         string    `json:"format,omitempty"`
		TimeoutSeconds int       `json:"timeoutSeconds,omitempty"`
		Include        *[]string `json:"include,omitempty"`
		Exclude        *[]string `json:"exclude,omitempty"`
		Mode           string    `json:"mode,omitempty"`
	}
	encoded := encodedDiscoveryConfig{
		Enabled:        value.Enabled,
		Format:         value.Format,
		TimeoutSeconds: value.TimeoutSeconds,
		Mode:           value.Mode,
	}
	if value.PathSet || value.Path != "" {
		path := value.Path
		encoded.Path = &path
	}
	if value.Include != nil {
		include := cloneStringSlice(value.Include)
		encoded.Include = &include
	}
	if value.Exclude != nil {
		exclude := cloneStringSlice(value.Exclude)
		encoded.Exclude = &exclude
	}
	return json.Marshal(encoded)
}

func (value *DiscoveryConfig) UnmarshalJSON(raw []byte) error {
	type encodedDiscoveryConfig struct {
		Enabled        *bool           `json:"enabled,omitempty"`
		Path           json.RawMessage `json:"path"`
		Format         string          `json:"format,omitempty"`
		TimeoutSeconds int             `json:"timeoutSeconds,omitempty"`
		Include        *[]string       `json:"include,omitempty"`
		Exclude        *[]string       `json:"exclude,omitempty"`
		Mode           string          `json:"mode,omitempty"`
	}
	var encoded encodedDiscoveryConfig
	if err := json.Unmarshal(raw, &encoded); err != nil {
		return err
	}
	decoded := DiscoveryConfig{
		Enabled:        cloneBoolPointer(encoded.Enabled),
		Format:         encoded.Format,
		TimeoutSeconds: encoded.TimeoutSeconds,
		Mode:           encoded.Mode,
	}
	if len(encoded.Path) != 0 {
		if strings.TrimSpace(string(encoded.Path)) == "null" {
			return errors.New("discovery path must be a string")
		}
		if err := json.Unmarshal(encoded.Path, &decoded.Path); err != nil {
			return err
		}
		decoded.PathSet = true
	}
	if encoded.Include != nil {
		decoded.Include = cloneStringSlice(*encoded.Include)
	}
	if encoded.Exclude != nil {
		decoded.Exclude = cloneStringSlice(*encoded.Exclude)
	}
	*value = decoded
	return nil
}

// Auth declares where a provider secret comes from: a named entry in the
// platform credential store (Credential) and/or environment variables (Env).
// Env variables are resolved first; Credential is the keyring fallback.
// CredentialSet records whether the JSON document explicitly set credential,
// including an explicit empty string that clears a preset's keyring reference.
type Auth struct {
	Credential    string   `json:"credential,omitempty"`
	CredentialSet bool     `json:"-"`
	Env           []string `json:"env,omitempty"`
}

func (value *Auth) UnmarshalJSON(raw []byte) error {
	type encodedAuth struct {
		Credential *string  `json:"credential"`
		Env        []string `json:"env"`
	}
	var encoded encodedAuth
	if err := json.Unmarshal(raw, &encoded); err != nil {
		return err
	}
	value.CredentialSet = encoded.Credential != nil
	value.Credential = ""
	if encoded.Credential != nil {
		value.Credential = *encoded.Credential
	}
	value.Env = append([]string(nil), encoded.Env...)
	return nil
}

func (value Auth) MarshalJSON() ([]byte, error) {
	type encodedAuth struct {
		Credential *string  `json:"credential,omitempty"`
		Env        []string `json:"env,omitempty"`
	}
	encoded := encodedAuth{Env: value.Env}
	// Emit credential when it was explicitly configured (even as an empty
	// clear marker) or when a non-empty value was set programmatically.
	if value.CredentialSet || value.Credential != "" {
		credential := value.Credential
		encoded.Credential = &credential
	}
	return json.Marshal(encoded)
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

type ModelSource string

const (
	ModelSourceConfigured ModelSource = "configured"
	ModelSourceDiscovered ModelSource = "discovered"
)

type CatalogModel struct {
	ID     string
	Model  Model
	Source ModelSource
}

// CatalogSelection pins the exact catalog entry and Snapshot revision observed
// by a selector. ProviderKey preserves the document's exact provider key while
// ModelName uses the catalog's normalized model-name identity.
type CatalogSelection struct {
	Revision    uint64
	ID          string
	ProviderKey string
	ModelName   string
	Source      ModelSource
}

type DiscoveryStatus struct {
	Attempted       bool
	ProviderID      string
	Source          string
	AttemptedAt     time.Time
	SucceededAt     time.Time
	DiscoveredAt    time.Time
	DiscoveredCount int
	FilteredCount   int
	EffectiveCount  int
	CacheProviders  int
	CacheState      string
	SkippedReason   string
	LastError       string
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
	Document        Document
	Workspace       WorkspaceDocument
	Active          model.Config
	ActiveModelID   string
	EffectiveModels map[string]CatalogModel
	Discovery       DiscoveryStatus
	Revision        uint64
	ContentHash     string
	Diagnostics     []Diagnostic
	Ready           bool
	LoadedAt        time.Time
	Raw             []byte

	globalConfigExists    bool
	workspaceConfigExists bool
	workspaceRaw          []byte
}

func (s Snapshot) Clone() Snapshot {
	s.Document = cloneDocument(s.Document)
	s.Workspace = cloneWorkspace(s.Workspace)
	s.Active = model.CloneConfig(s.Active)
	s.EffectiveModels = cloneCatalog(s.EffectiveModels)
	s.Diagnostics = append([]Diagnostic(nil), s.Diagnostics...)
	s.Raw = append([]byte(nil), s.Raw...)
	s.workspaceRaw = append([]byte(nil), s.workspaceRaw...)
	return s
}

// CatalogSelection resolves an exact catalog ID first. For compatibility with
// callers that surround an ID with whitespace, it falls back to trimmed ID
// comparison only when that comparison identifies exactly one catalog entry.
func (s Snapshot) CatalogSelection(requestedID string) (CatalogSelection, error) {
	if item, ok := s.EffectiveModels[requestedID]; ok {
		return newCatalogSelection(s.Revision, requestedID, item), nil
	}
	trimmedID := strings.TrimSpace(requestedID)
	var selectedID string
	var selected CatalogModel
	matches := 0
	for id, item := range s.EffectiveModels {
		if strings.TrimSpace(id) != trimmedID {
			continue
		}
		selectedID = id
		selected = item
		matches++
	}
	switch matches {
	case 0:
		return CatalogSelection{}, fmt.Errorf("model %q is not in the effective catalog", requestedID)
	case 1:
		return newCatalogSelection(s.Revision, selectedID, selected), nil
	default:
		return CatalogSelection{}, fmt.Errorf("model ID %q is ambiguous after trimming whitespace", requestedID)
	}
}

func newCatalogSelection(revision uint64, id string, item CatalogModel) CatalogSelection {
	return CatalogSelection{
		Revision:    revision,
		ID:          id,
		ProviderKey: item.Model.Provider,
		ModelName:   strings.TrimSpace(item.Model.Name),
		Source:      item.Source,
	}
}

func (s CatalogSelection) matches(item CatalogModel) bool {
	return item.ID == s.ID &&
		item.Source == s.Source &&
		identityForModel(item.Model) == (modelIdentity{providerID: s.ProviderKey, name: s.ModelName})
}

type OperationKind string

const (
	OperationSetActiveModel OperationKind = "set-active-model"
	OperationSetProxy       OperationKind = "set-proxy"
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
	return setActiveModelExact(strings.TrimSpace(id))
}

func setActiveModelExact(id string) Operation {
	raw, _ := json.Marshal(id)
	return Operation{Kind: OperationSetActiveModel, Value: raw}
}

// SetProxy 写入全局代理设置。proxy 为 nil 时从文档中删除全局代理字段
// （等价于 auto 语义）。
func SetProxy(proxy *model.ProxyConfig) Operation {
	var raw json.RawMessage
	if proxy != nil {
		raw, _ = json.Marshal(proxy)
	}
	return Operation{Kind: OperationSetProxy, Value: raw}
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
	out.Proxy = model.CloneProxyConfig(in.Proxy)
	out.Providers = make(map[string]Provider, len(in.Providers))
	for id, value := range in.Providers {
		out.Providers[id] = cloneProvider(value)
	}
	out.Models = make(map[string]Model, len(in.Models))
	for id, value := range in.Models {
		out.Models[id] = cloneModel(value)
	}
	return out
}

func cloneProvider(value Provider) Provider {
	value.Headers = cloneStringMap(value.Headers)
	value.Body = cloneAnyMap(value.Body)
	value.Auth.Env = cloneStringSlice(value.Auth.Env)
	if value.Retries != nil {
		retries := *value.Retries
		value.Retries = &retries
	}
	value.Stream = cloneBoolPointer(value.Stream)
	value.Proxy = model.CloneProxyConfig(value.Proxy)
	value.Discovery = cloneDiscoveryConfig(value.Discovery)
	return value
}

func cloneDiscoveryConfig(value *DiscoveryConfig) *DiscoveryConfig {
	if value == nil {
		return nil
	}
	cloned := *value
	cloned.Enabled = cloneBoolPointer(value.Enabled)
	cloned.PathSet = value.PathSet || value.Path != ""
	cloned.Include = cloneStringSlice(value.Include)
	cloned.Exclude = cloneStringSlice(value.Exclude)
	return &cloned
}

func cloneModel(value Model) Model {
	value.Parameters = cloneAnyMap(value.Parameters)
	value.Stream = cloneBoolPointer(value.Stream)
	value.Capabilities.Tools = cloneBoolPointer(value.Capabilities.Tools)
	value.Capabilities.Vision = cloneBoolPointer(value.Capabilities.Vision)
	value.Capabilities.Reasoning = cloneBoolPointer(value.Capabilities.Reasoning)
	value.Capabilities.Attachment = cloneBoolPointer(value.Capabilities.Attachment)
	return value
}

func cloneCatalog(in map[string]CatalogModel) map[string]CatalogModel {
	if in == nil {
		return nil
	}
	out := make(map[string]CatalogModel, len(in))
	for id, value := range in {
		value.Model = cloneModel(value.Model)
		out[id] = value
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

func cloneStringSlice(in []string) []string {
	if in == nil {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
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
