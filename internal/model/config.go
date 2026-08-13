package model

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	defaultTimeoutSeconds     = 60
	defaultRetryCountValue    = 3
	DefaultContextLimitTokens = 128 * 1024
	modelConfigDirName        = ".paw"
	modelConfigFileName       = "config.json"
)

type Config struct {
	Provider                string
	Transport               string
	Adapter                 string
	ProfileID               string
	ProfileName             string
	APIBaseURL              string
	APIPath                 string
	APIKey                  string
	APIKeyEnvName           string
	Headers                 map[string]string
	Model                   string
	Models                  []string
	ExtraBody               RequestBody
	ModelExtraBody          map[string]RequestBody
	ContextLimitTokens      int
	ModelContextLimitTokens map[string]int
	Timeout                 time.Duration
	RetryCount              int
	RetryCountSet           bool
	Stream                  bool
	StreamSet               bool
	streamSet               bool
	Profiles                []Profile
}

// Profile is one fully configured provider entry from ~/.paw/config.json.
// The UI uses one profile as the first-level provider choice and its Models
// as the second-level model choices.
type Profile struct {
	ID                      string
	Name                    string
	Provider                string
	Transport               string
	Adapter                 string
	APIBaseURL              string
	APIPath                 string
	APIKey                  string
	APIKeyEnvName           string
	Headers                 map[string]string
	Model                   string
	Models                  []string
	ExtraBody               RequestBody
	ModelExtraBody          map[string]RequestBody
	ContextLimitTokens      int
	ModelContextLimitTokens map[string]int
	Timeout                 time.Duration
	RetryCount              int
	RetryCountSet           bool
	Stream                  bool
	StreamSet               bool
	CredentialID            string
}

func (p Profile) Config() Config {
	models := normalizeModelNames(p.Models)
	modelName := strings.TrimSpace(p.Model)
	if modelName == "" && len(models) > 0 {
		modelName = models[0]
	}
	return Config{
		Provider:                p.Provider,
		Transport:               p.Transport,
		Adapter:                 p.Adapter,
		ProfileID:               p.ID,
		ProfileName:             p.Name,
		APIBaseURL:              p.APIBaseURL,
		APIPath:                 p.APIPath,
		APIKey:                  p.APIKey,
		APIKeyEnvName:           p.APIKeyEnvName,
		Headers:                 cloneStringMap(p.Headers),
		Model:                   modelName,
		Models:                  models,
		ExtraBody:               CloneRequestBody(p.ExtraBody),
		ModelExtraBody:          CloneModelExtraBodies(p.ModelExtraBody),
		ContextLimitTokens:      p.ContextLimitTokens,
		ModelContextLimitTokens: cloneModelContextLimits(p.ModelContextLimitTokens),
		Timeout:                 p.Timeout,
		RetryCount:              p.RetryCount,
		RetryCountSet:           p.RetryCountSet,
		Stream:                  p.Stream,
		StreamSet:               p.StreamSet,
		streamSet:               p.StreamSet,
	}
}

type persistedModelConfig struct {
	ID                      string                 `json:"id,omitempty"`
	Name                    string                 `json:"name,omitempty"`
	Provider                string                 `json:"provider,omitempty"`
	Transport               string                 `json:"transport,omitempty"`
	APIBaseURL              string                 `json:"baseUrl,omitempty"`
	APIPath                 string                 `json:"apiPath,omitempty"`
	APIKeyEnvName           string                 `json:"apiKeyEnvName,omitempty"`
	APIKey                  string                 `json:"apiKey,omitempty"`
	Model                   string                 `json:"model,omitempty"`
	Models                  []string               `json:"models,omitempty"`
	Timeout                 int                    `json:"timeoutSeconds,omitempty"`
	RetryCount              int                    `json:"retryCount,omitempty"`
	Stream                  *bool                  `json:"stream,omitempty"`
	CredentialID            string                 `json:"credentialId,omitempty"`
	ExtraBody               RequestBody            `json:"-"`
	ModelExtraBody          map[string]RequestBody `json:"-"`
	ContextLimitTokens      int                    `json:"context_limit_tokens,omitempty"`
	ModelContextLimitTokens map[string]int         `json:"model_context_limit_tokens,omitempty"`
	extraBodySet            bool
	modelExtraBodySet       bool
}

func (p *persistedModelConfig) UnmarshalJSON(data []byte) error {
	type alias persistedModelConfig
	var decoded alias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*p = persistedModelConfig(decoded)
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if p.ContextLimitTokens == 0 {
		if raw, ok := fields["contextLimitTokens"]; ok {
			_ = json.Unmarshal(raw, &p.ContextLimitTokens)
		}
	}
	if len(p.ModelContextLimitTokens) == 0 {
		if raw, ok := fields["modelContextLimitTokens"]; ok {
			_ = json.Unmarshal(raw, &p.ModelContextLimitTokens)
		}
	}
	if raw, ok := fields["extraBody"]; ok {
		p.extraBodySet = true
		body, err := decodeRequiredRequestBody(raw, "extraBody")
		if err != nil {
			return fmt.Errorf("model profile %q: %w", persistedProfileLabel(*p), err)
		}
		p.ExtraBody = body
	}
	if raw, ok := fields["modelExtraBody"]; ok {
		p.modelExtraBodySet = true
		values, err := decodeModelExtraBodies(raw)
		if err != nil {
			return fmt.Errorf("model profile %q: %w", persistedProfileLabel(*p), err)
		}
		p.ModelExtraBody = values
	}
	return nil
}

func persistedProfileLabel(p persistedModelConfig) string {
	for _, value := range []string{p.ID, p.Name, p.Provider} {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return "default"
}

func (p persistedModelConfig) MarshalJSON() ([]byte, error) {
	type fields persistedModelConfig

	// Keep nil maps omitted for backwards-compatible config output, while
	// retaining explicitly configured empty objects as {} rather than null.
	var extraBody *RequestBody
	if p.ExtraBody != nil {
		cloned := CloneRequestBody(p.ExtraBody)
		extraBody = &cloned
	}
	var modelExtraBody *map[string]RequestBody
	if p.ModelExtraBody != nil {
		cloned := CloneModelExtraBodies(p.ModelExtraBody)
		modelExtraBody = &cloned
	}

	return json.Marshal(struct {
		fields
		ExtraBody      *RequestBody            `json:"extraBody,omitempty"`
		ModelExtraBody *map[string]RequestBody `json:"modelExtraBody,omitempty"`
	}{
		fields:         fields(p),
		ExtraBody:      extraBody,
		ModelExtraBody: modelExtraBody,
	})
}

func decodeRequiredRequestBody(raw json.RawMessage, location string) (RequestBody, error) {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, fmt.Errorf("%s: %w", location, err)
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s must be a JSON object", location)
	}
	return CloneRequestBody(RequestBody(object)), nil
}

func decodeModelExtraBodies(raw json.RawMessage) (map[string]RequestBody, error) {
	body, err := decodeRequiredRequestBody(raw, "modelExtraBody")
	if err != nil {
		return nil, err
	}
	result := make(map[string]RequestBody, len(body))
	for _, modelName := range sortedRequestBodyKeys(body) {
		object, ok := jsonObject(body[modelName])
		if !ok {
			return nil, fmt.Errorf("modelExtraBody[%q] must be a JSON object", modelName)
		}
		result[modelName] = CloneRequestBody(object)
	}
	return result, nil
}

type persistedPawConfig struct {
	SchemaVersion        int                    `json:"schemaVersion,omitempty"`
	ModelProfiles        []persistedModelConfig `json:"modelProfiles,omitempty"`
	ActiveModelProfileID string                 `json:"activeModelProfileId,omitempty"`
}

func LoadConfigFromEnv() (Config, error) {
	fileEnvValues, err := loadOptionalEnvFiles(".env", ".env.local")
	if err != nil {
		return Config{}, err
	}
	configPath, err := modelConfigPath()
	if err != nil {
		return Config{}, err
	}

	document, persisted, err := loadPawConfigDocument(configPath)
	if err != nil {
		return Config{}, err
	}
	profiles, err := configuredProfiles(persisted.ModelProfiles, fileEnvValues)
	if err != nil {
		return Config{}, err
	}
	if len(profiles) == 0 {
		if document == nil {
			if err := savePawConfigDocument(configPath, map[string]any{
				"schemaVersion":        1,
				"modelProfiles":        []any{},
				"activeModelProfileId": "",
			}); err != nil {
				return Config{}, fmt.Errorf("create Paw config: %w", err)
			}
		}
		return Config{}, fmt.Errorf("no model profiles configured in %s", configPath)
	}

	selected := 0
	activeID := strings.TrimSpace(persisted.ActiveModelProfileID)
	if activeID != "" {
		for index, profile := range profiles {
			if profile.ID == activeID {
				selected = index
				break
			}
		}
	}
	cfg := profiles[selected].Config()
	cfg.Profiles = cloneProfiles(profiles)
	cfg = fillConfigDefaults(cfg)
	if cfg.APIKey == "" {
		cfg.APIKey = loadAPIKeyByEnvName(cfg.APIKeyEnvName, fileEnvValues)
	}
	return cfg, nil
}

func SaveModelConfig(cfg Config) error {
	configPath, err := modelConfigPath()
	if err != nil {
		return err
	}
	return saveModelConfigAtPath(cfg, configPath)
}

func saveModelConfigAtPath(cfg Config, configPath string) error {
	cfg = fillConfigDefaults(cfg)
	if err := ValidateExtraRequestBodies(cfg); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}

	document, persisted, err := loadPawConfigDocument(configPath)
	if err != nil {
		return err
	}
	if document == nil {
		document = make(map[string]any)
	}
	if _, ok := document["schemaVersion"]; !ok {
		document["schemaVersion"] = 1
	}

	profileID := strings.TrimSpace(cfg.ProfileID)
	if profileID == "" {
		profileID = strings.TrimSpace(persisted.ActiveModelProfileID)
	}
	if profileID == "" && len(persisted.ModelProfiles) > 0 {
		profileID = strings.TrimSpace(persisted.ModelProfiles[0].ID)
	}
	if profileID == "" {
		profileID = "default"
	}

	profiles := profileDocuments(document)
	profileIndex := -1
	for index, profile := range profiles {
		if strings.TrimSpace(stringValue(profile["id"])) == profileID {
			profileIndex = index
			break
		}
	}
	if profileIndex < 0 {
		profiles = append(profiles, make(map[string]any))
		profileIndex = len(profiles) - 1
	}
	profile := profiles[profileIndex]
	profile["id"] = profileID
	if strings.TrimSpace(cfg.ProfileName) != "" {
		profile["name"] = cfg.ProfileName
	}
	if strings.TrimSpace(cfg.Provider) != "" {
		profile["provider"] = cfg.Provider
	}
	if strings.TrimSpace(cfg.Transport) != "" {
		profile["transport"] = cfg.Transport
	}
	if strings.TrimSpace(cfg.APIBaseURL) != "" {
		profile["baseUrl"] = cfg.APIBaseURL
	}
	if strings.TrimSpace(cfg.APIPath) != "" {
		profile["apiPath"] = cfg.APIPath
	}
	if strings.TrimSpace(cfg.APIKeyEnvName) != "" {
		profile["apiKeyEnvName"] = cfg.APIKeyEnvName
	}
	if strings.TrimSpace(cfg.Model) != "" {
		profile["model"] = cfg.Model
	}
	if models := AvailableModels(cfg); len(models) > 0 {
		profile["models"] = models
	}
	if cfg.Timeout > 0 {
		profile["timeoutSeconds"] = int(cfg.Timeout / time.Second)
	}
	if cfg.RetryCount > 0 {
		profile["retryCount"] = cfg.RetryCount
	}
	profile["stream"] = cfg.Stream
	if cfg.ExtraBody != nil {
		profile["extraBody"] = CloneRequestBody(cfg.ExtraBody)
	} else {
		delete(profile, "extraBody")
	}
	if cfg.ModelExtraBody != nil {
		profile["modelExtraBody"] = CloneModelExtraBodies(cfg.ModelExtraBody)
	} else {
		delete(profile, "modelExtraBody")
	}
	if cfg.ContextLimitTokens > 0 {
		profile["context_limit_tokens"] = cfg.ContextLimitTokens
		delete(profile, "contextLimitTokens")
	} else {
		delete(profile, "context_limit_tokens")
		delete(profile, "contextLimitTokens")
	}
	if len(cfg.ModelContextLimitTokens) > 0 {
		profile["model_context_limit_tokens"] = cloneModelContextLimits(cfg.ModelContextLimitTokens)
		delete(profile, "modelContextLimitTokens")
	} else {
		delete(profile, "model_context_limit_tokens")
		delete(profile, "modelContextLimitTokens")
	}
	document["modelProfiles"] = profiles
	document["activeModelProfileId"] = profileID

	return savePawConfigDocument(configPath, document)
}

func defaultTimeout() time.Duration {
	return time.Duration(defaultTimeoutSeconds) * time.Second
}

func defaultRetryCount() int {
	return defaultRetryCountValue
}

func fillConfigDefaults(cfg Config) Config {
	cfg.Provider = strings.TrimSpace(cfg.Provider)
	cfg.Transport = strings.TrimSpace(cfg.Transport)
	cfg.ProfileID = strings.TrimSpace(cfg.ProfileID)
	cfg.ProfileName = strings.TrimSpace(cfg.ProfileName)
	cfg.APIBaseURL = strings.TrimSpace(cfg.APIBaseURL)
	cfg.APIPath = strings.TrimSpace(cfg.APIPath)
	cfg.APIKeyEnvName = strings.TrimSpace(cfg.APIKeyEnvName)
	cfg.Model = strings.TrimSpace(cfg.Model)
	cfg.Models = normalizeModelNames(cfg.Models)
	cfg.ModelContextLimitTokens = normalizeModelContextLimits(cfg.ModelContextLimitTokens)
	applyTransportDefaults(&cfg)
	if cfg.Model == "" && len(cfg.Models) > 0 {
		cfg.Model = cfg.Models[0]
	}
	if cfg.Model != "" && !containsModel(cfg.Models, cfg.Model) {
		cfg.Models = append(cfg.Models, cfg.Model)
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = defaultTimeout()
	}
	if cfg.RetryCount < 0 || (cfg.RetryCount == 0 && !cfg.RetryCountSet) {
		cfg.RetryCount = defaultRetryCount()
	}
	if !cfg.Stream && !cfg.streamSet && !cfg.StreamSet {
		cfg.Stream = true
	}
	return cfg
}

func applyTransportDefaults(cfg *Config) {
	if cfg == nil {
		return
	}
	transport := strings.ToLower(strings.TrimSpace(cfg.Transport))
	path := strings.ToLower(strings.TrimSpace(cfg.APIPath))
	if transport == "" {
		switch {
		case strings.Contains(path, "/chat/completions"):
			cfg.Transport = "openai-compatible"
		case strings.Contains(path, "/messages"):
			cfg.Transport = "anthropic-compatible"
		default:
			cfg.Transport = "openai-responses"
		}
		transport = strings.ToLower(cfg.Transport)
	}
	if cfg.APIPath == "" && strings.Contains(transport, "response") {
		cfg.APIPath = "/responses"
	}
}

func configuredProfiles(persisted []persistedModelConfig, envValues map[string]string) ([]Profile, error) {
	profiles := make([]Profile, 0, len(persisted))
	for _, raw := range persisted {
		profile := Profile{
			ID:                      strings.TrimSpace(raw.ID),
			Name:                    strings.TrimSpace(raw.Name),
			Provider:                strings.TrimSpace(raw.Provider),
			Transport:               strings.TrimSpace(raw.Transport),
			APIBaseURL:              strings.TrimSpace(raw.APIBaseURL),
			APIPath:                 strings.TrimSpace(raw.APIPath),
			APIKey:                  strings.TrimSpace(raw.APIKey),
			APIKeyEnvName:           strings.TrimSpace(raw.APIKeyEnvName),
			Model:                   strings.TrimSpace(raw.Model),
			Models:                  normalizeModelNames(raw.Models),
			ExtraBody:               CloneRequestBody(raw.ExtraBody),
			ModelExtraBody:          CloneModelExtraBodies(raw.ModelExtraBody),
			ContextLimitTokens:      raw.ContextLimitTokens,
			ModelContextLimitTokens: cloneModelContextLimits(raw.ModelContextLimitTokens),
			CredentialID:            strings.TrimSpace(raw.CredentialID),
			StreamSet:               raw.Stream != nil,
		}
		if raw.Timeout > 0 {
			profile.Timeout = time.Duration(raw.Timeout) * time.Second
		}
		if raw.RetryCount > 0 {
			profile.RetryCount = raw.RetryCount
			profile.RetryCountSet = true
		}
		if raw.Stream != nil {
			profile.Stream = *raw.Stream
		}
		if profile.APIKeyEnvName != "" {
			if key := loadAPIKeyByEnvName(profile.APIKeyEnvName, envValues); key != "" {
				profile.APIKey = key
			}
		}
		if profile.ID == "" {
			profile.ID = profile.Name
		}
		if profile.ID == "" {
			profile.ID = profile.Provider
		}
		if err := ValidateExtraRequestBodies(fillConfigDefaults(profile.Config())); err != nil {
			return nil, err
		}
		profiles = append(profiles, profile)
	}
	return profiles, nil
}

func cloneProfiles(profiles []Profile) []Profile {
	cloned := make([]Profile, len(profiles))
	for index, profile := range profiles {
		cloned[index] = profile
		cloned[index].Models = append([]string(nil), profile.Models...)
		cloned[index].Headers = cloneStringMap(profile.Headers)
		cloned[index].ExtraBody = CloneRequestBody(profile.ExtraBody)
		cloned[index].ModelExtraBody = CloneModelExtraBodies(profile.ModelExtraBody)
		cloned[index].ModelContextLimitTokens = cloneModelContextLimits(profile.ModelContextLimitTokens)
	}
	return cloned
}

// ConfiguredProfiles returns all configured first-level choices. The fallback
// keeps callers that construct Config directly usable without inventing any
// provider or endpoint values.
func ConfiguredProfiles(cfg Config) []Profile {
	if len(cfg.Profiles) > 0 {
		return cloneProfiles(cfg.Profiles)
	}
	return []Profile{{
		ID:             cfg.ProfileID,
		Name:           cfg.ProfileName,
		Provider:       cfg.Provider,
		Transport:      cfg.Transport,
		Adapter:        cfg.Adapter,
		APIBaseURL:     cfg.APIBaseURL,
		APIPath:        cfg.APIPath,
		APIKey:         cfg.APIKey,
		APIKeyEnvName:  cfg.APIKeyEnvName,
		Headers:        cloneStringMap(cfg.Headers),
		Model:          cfg.Model,
		Models:         append([]string(nil), cfg.Models...),
		ExtraBody:      CloneRequestBody(cfg.ExtraBody),
		ModelExtraBody: CloneModelExtraBodies(cfg.ModelExtraBody),
		Timeout:        cfg.Timeout,
		RetryCount:     cfg.RetryCount,
		RetryCountSet:  cfg.RetryCountSet,
		Stream:         cfg.Stream,
		StreamSet:      cfg.streamSet || cfg.StreamSet,
	}}
}

// AvailableModels returns the configured model names in stable order without
// duplicates. The active model is always included for compatibility with
// Config values built by callers that predate the model list.
func AvailableModels(cfg Config) []string {
	models := append([]string(nil), cfg.Models...)
	if strings.TrimSpace(cfg.Model) != "" {
		models = append(models, cfg.Model)
	}
	return normalizeModelNames(models)
}

// SupportsModel reports whether modelName is one of the configured models.
func SupportsModel(cfg Config, modelName string) bool {
	modelName = strings.TrimSpace(modelName)
	if modelName == "" {
		return false
	}
	return containsModel(AvailableModels(cfg), modelName)
}

func normalizeModelNames(models []string) []string {
	result := make([]string, 0, len(models))
	seen := make(map[string]struct{}, len(models))
	for _, name := range models {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		result = append(result, name)
	}
	return result
}

func containsModel(models []string, want string) bool {
	for _, name := range models {
		if name == want {
			return true
		}
	}
	return false
}

func EffectiveContextLimitTokens(cfg Config) int {
	if limit := cfg.ModelContextLimitTokens[strings.TrimSpace(cfg.Model)]; limit > 0 {
		return limit
	}
	if cfg.ContextLimitTokens > 0 {
		return cfg.ContextLimitTokens
	}
	if limit := MetadataContextLimit(cfg.Provider, cfg.Model); limit > 0 {
		return limit
	}
	return DefaultContextLimitTokens
}

func cloneModelContextLimits(values map[string]int) map[string]int {
	if values == nil {
		return nil
	}
	out := make(map[string]int, len(values))
	for name, limit := range values {
		out[name] = limit
	}
	return out
}

func normalizeModelContextLimits(values map[string]int) map[string]int {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]int, len(values))
	for name, limit := range values {
		name = strings.TrimSpace(name)
		if name != "" && limit > 0 {
			out[name] = limit
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func modelConfigPath() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve user home: %w", err)
	}
	if strings.TrimSpace(homeDir) == "" {
		return "", fmt.Errorf("resolve user home: empty path")
	}
	return filepath.Join(homeDir, modelConfigDirName, modelConfigFileName), nil
}

func loadPawConfigDocument(configPath string) (map[string]any, persistedPawConfig, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, persistedPawConfig{}, nil
		}
		return nil, persistedPawConfig{}, fmt.Errorf("read Paw config: %w", err)
	}

	if strings.TrimSpace(string(data)) == "" {
		return make(map[string]any), persistedPawConfig{}, nil
	}

	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		return nil, persistedPawConfig{}, fmt.Errorf("parse Paw config: %w", err)
	}
	var persisted persistedPawConfig
	if err := json.Unmarshal(data, &persisted); err != nil {
		return nil, persistedPawConfig{}, fmt.Errorf("parse Paw model profiles: %w", err)
	}
	return document, persisted, nil
}

func savePawConfigDocument(configPath string, document map[string]any) error {
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		return fmt.Errorf("create Paw config directory: %w", err)
	}
	data, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal Paw config: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(configPath, data, 0o600); err != nil {
		return fmt.Errorf("write Paw config: %w", err)
	}
	return nil
}

func profileDocuments(document map[string]any) []map[string]any {
	values, ok := document["modelProfiles"].([]any)
	if !ok {
		return nil
	}
	profiles := make([]map[string]any, 0, len(values))
	for _, value := range values {
		profile, ok := value.(map[string]any)
		if ok {
			profiles = append(profiles, profile)
		}
	}
	return profiles
}

func stringValue(value any) string {
	stringValue, _ := value.(string)
	return stringValue
}

func loadAPIKeyByEnvName(primary string, values map[string]string) string {
	primary = strings.TrimSpace(primary)
	if primary == "" {
		return ""
	}
	if value := strings.TrimSpace(values[primary]); value != "" {
		return value
	}
	if value := strings.TrimSpace(os.Getenv(primary)); value != "" {
		return value
	}
	return ""
}

// LoadOptionalEnvFiles loads key=value pairs from .env and .env.local in the
// current working directory into the process environment (missing files are
// not errors). It is called once at process startup so provider auth declared
// via auth.env can resolve without touching the system keyring.
func LoadOptionalEnvFiles() (map[string]string, error) {
	return loadOptionalEnvFiles(".env", ".env.local")
}

func loadOptionalEnvFiles(paths ...string) (map[string]string, error) {
	loaded := make(map[string]string)
	for _, path := range paths {
		if err := loadOptionalEnvFile(path, loaded); err != nil {
			return nil, err
		}
	}
	return loaded, nil
}

func loadOptionalEnvFile(path string, loaded map[string]string) error {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer func() {
		_ = file.Close()
	}()

	scanner := bufio.NewScanner(file)
	for lineNo := 1; scanner.Scan(); lineNo++ {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return fmt.Errorf("%s:%d invalid env line", path, lineNo)
		}

		key = strings.TrimSpace(key)
		if key == "" {
			return fmt.Errorf("%s:%d missing env key", path, lineNo)
		}

		value = trimEnvValue(strings.TrimSpace(value))
		if err := os.Setenv(key, value); err != nil {
			return fmt.Errorf("set env %s: %w", key, err)
		}
		loaded[key] = value
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scan %s: %w", path, err)
	}
	return nil
}

func trimEnvValue(value string) string {
	if len(value) >= 2 {
		if value[0] == '"' && value[len(value)-1] == '"' {
			return value[1 : len(value)-1]
		}
		if value[0] == '\'' && value[len(value)-1] == '\'' {
			return value[1 : len(value)-1]
		}
	}
	return value
}
