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
	ProviderCustom   = "custom"
	ProviderDeepSeek = "deepseek"

	CustomAPIBaseURL    = "http://localhost:8317/v1"
	CustomChatPath      = "/chat/completions"
	CustomDefaultAPIKey = "sk-dummy"
	legacyCustomModel   = "gpt-5.4-xhigh"
	CustomDefaultModel  = "gpt-5.5"
	CustomAPIKeyEnvName = "NEWAPI_API_KEY"

	DeepSeekAPIBaseURL    = "https://api.deepseek.com"
	DeepSeekChatPath      = "/chat/completions"
	DeepSeekDefaultModel  = "deepseek-chat"
	DeepSeekAPIKeyEnvName = "DEEPSEEK_API_KEY"

	defaultTimeoutSeconds  = 60
	defaultRetryCountValue = 3
	modelConfigPath        = ".paw/model.json"
)

var apiKeyEnvNames = []string{
	CustomAPIKeyEnvName,
	DeepSeekAPIKeyEnvName,
}

var placeholderAPIKeys = map[string]struct{}{
	"":               {},
	"dummy":          {},
	"your_key_here":  {},
	"your-key-here":  {},
	"<your_api_key>": {},
}

type Config struct {
	Provider      string
	APIBaseURL    string
	APIPath       string
	APIKey        string
	APIKeyEnvName string
	Model         string
	Models        []string
	Timeout       time.Duration
	RetryCount    int
	Stream        bool
	streamSet     bool
}

type persistedModelConfig struct {
	Provider      string   `json:"provider"`
	APIBaseURL    string   `json:"api_base_url"`
	APIPath       string   `json:"api_path"`
	APIKeyEnvName string   `json:"api_key_env_name"`
	Model         string   `json:"model"`
	Models        []string `json:"models,omitempty"`
	Timeout       int      `json:"timeout_seconds,omitempty"`
	RetryCount    int      `json:"retry_count,omitempty"`
	Stream        *bool    `json:"stream,omitempty"`
}

func LoadConfigFromEnv() (Config, error) {
	fileEnvValues, err := loadOptionalEnvFiles(".env", ".env.local")
	if err != nil {
		return Config{}, err
	}

	cfg := defaultConfigForProvider(ProviderCustom)
	persisted, err := loadPersistedModelConfig()
	if err != nil {
		return Config{}, err
	}
	if persisted != nil {
		cfg = mergePersistedConfig(cfg, *persisted)
	}

	cfg.Provider = normalizeProvider(cfg.Provider)
	cfg = fillConfigDefaults(cfg)
	cfg.APIKey = sanitizeAPIKey(cfg.Provider, loadAPIKeyByEnvName(cfg.APIKeyEnvName, fileEnvValues))
	if cfg.Provider == ProviderCustom && cfg.APIKey == "" {
		cfg.APIKey = CustomDefaultAPIKey
	}
	if providerRequiresAPIKey(cfg.Provider) && cfg.APIKey == "" {
		return Config{}, fmt.Errorf("missing %s", cfg.APIKeyEnvName)
	}
	return cfg, nil
}

func SaveModelConfig(cfg Config) error {
	cfg = fillConfigDefaults(cfg)
	if err := os.MkdirAll(filepath.Dir(modelConfigPath), 0o755); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}

	payload := persistedModelConfig{
		Provider:      cfg.Provider,
		APIBaseURL:    cfg.APIBaseURL,
		APIPath:       cfg.APIPath,
		APIKeyEnvName: cfg.APIKeyEnvName,
		Model:         cfg.Model,
		Models:        AvailableModels(cfg),
		Timeout:       int(cfg.Timeout / time.Second),
		RetryCount:    cfg.RetryCount,
		Stream:        boolPointer(cfg.Stream),
	}

	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal model config: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(modelConfigPath, data, 0o600); err != nil {
		return fmt.Errorf("write model config: %w", err)
	}
	return nil
}

func defaultConfigForProvider(provider string) Config {
	switch normalizeProvider(provider) {
	case ProviderCustom:
		return Config{
			Provider:      ProviderCustom,
			APIBaseURL:    CustomAPIBaseURL,
			APIPath:       CustomChatPath,
			APIKeyEnvName: CustomAPIKeyEnvName,
			Model:         CustomDefaultModel,
			Models:        []string{CustomDefaultModel},
			Timeout:       defaultTimeout(),
			RetryCount:    defaultRetryCount(),
			Stream:        true,
		}
	default:
		return Config{
			Provider:      ProviderDeepSeek,
			APIBaseURL:    DeepSeekAPIBaseURL,
			APIPath:       DeepSeekChatPath,
			APIKeyEnvName: DeepSeekAPIKeyEnvName,
			Model:         DeepSeekDefaultModel,
			Models:        []string{DeepSeekDefaultModel},
			Timeout:       defaultTimeout(),
			RetryCount:    defaultRetryCount(),
			Stream:        true,
		}
	}
}

func defaultTimeout() time.Duration {
	return time.Duration(defaultTimeoutSeconds) * time.Second
}

func defaultRetryCount() int {
	return defaultRetryCountValue
}

func normalizeProvider(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case ProviderCustom:
		return ProviderCustom
	case ProviderDeepSeek:
		return ProviderDeepSeek
	default:
		return ProviderCustom
	}
}

func fillConfigDefaults(cfg Config) Config {
	defaults := defaultConfigForProvider(cfg.Provider)
	cfg.Provider = defaults.Provider
	if strings.TrimSpace(cfg.APIBaseURL) == "" {
		cfg.APIBaseURL = defaults.APIBaseURL
	}
	if strings.TrimSpace(cfg.APIPath) == "" {
		cfg.APIPath = defaults.APIPath
	}
	if strings.TrimSpace(cfg.APIKeyEnvName) == "" {
		cfg.APIKeyEnvName = defaults.APIKeyEnvName
	}
	if strings.TrimSpace(cfg.Model) == "" {
		cfg.Model = defaults.Model
	}
	cfg.Models = normalizeModelNames(cfg.Models)
	if len(cfg.Models) == 0 {
		cfg.Models = append([]string(nil), defaults.Models...)
	}
	if !containsModel(cfg.Models, cfg.Model) {
		cfg.Models = append(cfg.Models, cfg.Model)
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = defaults.Timeout
	}
	if cfg.RetryCount <= 0 {
		cfg.RetryCount = defaults.RetryCount
	}
	if !cfg.Stream && !cfg.streamSet {
		cfg.Stream = defaults.Stream
	}
	return cfg
}

func boolPointer(value bool) *bool {
	return &value
}

func mergePersistedConfig(base Config, persisted persistedModelConfig) Config {
	cfg := base
	if strings.TrimSpace(persisted.Provider) != "" {
		cfg.Provider = persisted.Provider
	}
	if strings.TrimSpace(persisted.APIBaseURL) != "" {
		cfg.APIBaseURL = persisted.APIBaseURL
	}
	if strings.TrimSpace(persisted.APIPath) != "" {
		cfg.APIPath = persisted.APIPath
	}
	if strings.TrimSpace(persisted.APIKeyEnvName) != "" {
		cfg.APIKeyEnvName = persisted.APIKeyEnvName
	}
	if strings.TrimSpace(persisted.Model) != "" {
		cfg.Model = normalizePersistedModel(cfg.Provider, persisted.Model)
	}
	if len(persisted.Models) > 0 {
		cfg.Models = append([]string(nil), persisted.Models...)
	}
	if persisted.Timeout > 0 {
		cfg.Timeout = time.Duration(persisted.Timeout) * time.Second
	}
	if persisted.RetryCount > 0 {
		cfg.RetryCount = persisted.RetryCount
	}
	if persisted.Stream != nil {
		cfg.Stream = *persisted.Stream
		cfg.streamSet = true
	}
	return cfg
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

func loadPersistedModelConfig() (*persistedModelConfig, error) {
	data, err := os.ReadFile(modelConfigPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read model config: %w", err)
	}

	var persisted persistedModelConfig
	if err := json.Unmarshal(data, &persisted); err != nil {
		return nil, fmt.Errorf("parse model config: %w", err)
	}
	return &persisted, nil
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

func normalizePersistedModel(provider, modelName string) string {
	modelName = strings.TrimSpace(modelName)
	if normalizeProvider(provider) == ProviderCustom && modelName == legacyCustomModel {
		return CustomDefaultModel
	}
	return modelName
}

func providerRequiresAPIKey(provider string) bool {
	return normalizeProvider(provider) != ProviderCustom
}

func sanitizeAPIKey(provider, key string) string {
	key = strings.TrimSpace(key)
	if providerRequiresAPIKey(provider) {
		return key
	}
	if _, ok := placeholderAPIKeys[strings.ToLower(key)]; ok {
		return ""
	}
	return key
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
