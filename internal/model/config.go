package model

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"time"
)

const (
	defaultTimeoutSeconds     = 60
	defaultRetryCountValue    = 3
	DefaultContextLimitTokens = 128 * 1024
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
	Proxy                   *ProxyConfig
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

// ProxyMode 描述出站 HTTP 请求的代理解析方式。
type ProxyMode string

const (
	// ProxyModeAuto 使用进程环境变量（HTTP_PROXY/HTTPS_PROXY 等），等价于
	// Go 默认的 ProxyFromEnvironment。空值归一化为 auto。
	ProxyModeAuto ProxyMode = "auto"
	// ProxyModeDirect 强制直连，忽略环境变量代理。
	ProxyModeDirect ProxyMode = "direct"
	// ProxyModeCustom 使用 ProxyConfig.URL 指定的代理；URL 缺失或非法时回退直连。
	ProxyModeCustom ProxyMode = "custom"
)

// ProxyConfig 是一条代理设置。全局 Document.proxy 提供默认值，Provider 可用
// 自己的 proxy 覆盖；两者都缺省时行为等同 ProxyModeAuto。
type ProxyConfig struct {
	Mode ProxyMode `json:"mode,omitempty"`
	URL  string    `json:"url,omitempty"`
}

// NormalizeProxyMode 把任意字符串归一到合法 ProxyMode；非法值回退 auto。
func NormalizeProxyMode(mode ProxyMode) ProxyMode {
	switch ProxyMode(strings.ToLower(strings.TrimSpace(string(mode)))) {
	case ProxyModeDirect:
		return ProxyModeDirect
	case ProxyModeCustom:
		return ProxyModeCustom
	default:
		return ProxyModeAuto
	}
}

// CloneProxyConfig 深拷贝代理配置；nil 保持不变。
func CloneProxyConfig(proxy *ProxyConfig) *ProxyConfig {
	if proxy == nil {
		return nil
	}
	cloned := *proxy
	return &cloned
}

// Profile is one fully resolved provider entry synthesized from config.jsonc.
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
	Proxy                   *ProxyConfig
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
		Proxy:                   CloneProxyConfig(p.Proxy),
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

func cloneProfiles(profiles []Profile) []Profile {
	cloned := make([]Profile, len(profiles))
	for index, profile := range profiles {
		cloned[index] = profile
		cloned[index].Models = append([]string(nil), profile.Models...)
		cloned[index].Headers = cloneStringMap(profile.Headers)
		cloned[index].Proxy = CloneProxyConfig(profile.Proxy)
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
		Proxy:          CloneProxyConfig(cfg.Proxy),
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

func loadAPIKeyByEnvName(primary string, values map[string]string) string {
	primary = strings.TrimSpace(primary)
	if primary == "" {
		return ""
	}
	if value := strings.TrimSpace(values[primary]); value != "" {
		return value
	}
	return strings.TrimSpace(os.Getenv(primary))
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
