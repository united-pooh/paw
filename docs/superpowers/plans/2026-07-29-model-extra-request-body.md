# 模型级额外请求体配置实现计划

> **面向 AI 代理的工作者：** 必需子技能：使用 superpowers:subagent-driven-development（推荐）或 superpowers:executing-plans 逐任务实现此计划。步骤使用复选框（`- [ ]`）语法来跟踪进度。

**目标：** 为模型 Profile 增加 `extraBody` 与 `modelExtraBody`，将任意 JSON 参数安全地深度合并进 OpenAI-compatible 和 Anthropic 请求体，同时保护 Paw 管理的结构性字段。

**架构：** 新增独立的请求体模块，集中负责 JSON object 深拷贝、深度合并、transport 保护字段校验和统一序列化。模型配置层负责严格解析、模型名校验、跨 Profile/Config 的深拷贝与持久化；各 transport 只负责构造原有强类型基础请求，再调用统一 helper 合并当前模型的有效额外请求体。

**技术栈：** Go、`encoding/json`、`net/http/httptest`、Go `testing`

---

## 文件结构

- 创建：`internal/model/request_body.go` — 定义 `RequestBody`，实现深拷贝、深度合并、有效配置计算、受保护字段校验和请求体序列化。
- 创建：`internal/model/request_body_test.go` — 单元测试 JSON 合并、复制、`null`、数组替换、保护字段和模型名校验。
- 修改：`internal/model/config.go` — 扩展持久化/runtime 配置，严格解析 `extraBody`/`modelExtraBody`，校验并保存配置，保证 Profile 切换时深拷贝。
- 修改：`internal/model/config_test.go` — 覆盖配置解析错误、保存 round-trip、未知模型和深拷贝。
- 修改：`internal/model/client.go` — `NewClient`/`ApplyModelConfig` 校验配置，`CurrentModelConfig` 返回隔离副本，`RunMessage` 应用额外请求体。
- 修改：`internal/model/client_test.go` — 覆盖应用配置错误和 `RunMessage` 请求体透传。
- 修改：`internal/model/stream.go` — OpenAI-compatible 流式与非流式请求统一应用额外请求体。
- 修改：`internal/model/stream_test.go` — 捕获 OpenAI 流式/非流式请求体，验证当前模型参数选择。
- 修改：`internal/model/anthropic_stream.go` — Anthropic 基础请求序列化后应用额外请求体，允许覆盖 `max_tokens`。
- 修改：`internal/model/stream_test.go` — 捕获 Anthropic 请求体，验证默认与覆盖后的 `max_tokens`。
- 修改：`internal/ui/bubble/bubble_test.go` — 验证 `/model` 切换 Profile/模型时额外配置不会丢失。
- 参考：`docs/superpowers/specs/2026-07-29-model-extra-request-body.md` — 本计划的完整行为规格。

### 任务 1：实现通用 JSON 请求体能力

**文件：**
- 创建：`internal/model/request_body.go`
- 创建：`internal/model/request_body_test.go`

- [ ] **步骤 1：编写深拷贝与深度合并失败测试**

创建 `internal/model/request_body_test.go`：

```go
package model

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestMergeRequestBodiesDeepMergesWithoutMutatingInputs(t *testing.T) {
	base := RequestBody{
		"metadata": map[string]any{
			"team":        "platform",
			"environment": "production",
		},
		"tags": []any{"profile"},
	}
	override := RequestBody{
		"metadata": map[string]any{
			"environment": "development",
			"feature":     "agent",
		},
		"tags":     []any{"model"},
		"optional": nil,
	}

	got := MergeRequestBodies(base, override)
	want := RequestBody{
		"metadata": map[string]any{
			"team":        "platform",
			"environment": "development",
			"feature":     "agent",
		},
		"tags":     []any{"model"},
		"optional": nil,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("MergeRequestBodies() = %#v, want %#v", got, want)
	}

	got["metadata"].(map[string]any)["team"] = "changed"
	got["tags"].([]any)[0] = "changed"
	if base["metadata"].(map[string]any)["team"] != "platform" {
		t.Fatalf("base metadata was mutated: %#v", base)
	}
	if override["tags"].([]any)[0] != "model" {
		t.Fatalf("override tags were mutated: %#v", override)
	}
}

func TestMergeRequestBodiesReplacesMismatchedTypes(t *testing.T) {
	got := MergeRequestBodies(
		RequestBody{"value": map[string]any{"nested": true}},
		RequestBody{"value": "replacement"},
	)
	if got["value"] != "replacement" {
		t.Fatalf("value = %#v, want replacement", got["value"])
	}
}

func TestMarshalRequestBodyPreservesExplicitNull(t *testing.T) {
	data, err := MarshalRequestBody(struct {
		Model string `json:"model"`
	}{Model: "gpt-5.6-sol"}, RequestBody{"optional": nil})
	if err != nil {
		t.Fatalf("MarshalRequestBody() error = %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if value, ok := got["optional"]; !ok || value != nil {
		t.Fatalf("optional = %#v, present=%v; want explicit null", value, ok)
	}
}
```

- [ ] **步骤 2：运行测试验证失败**

运行：

```bash
go test ./internal/model -run 'Test(MergeRequestBodies|MarshalRequestBody)' -count=1
```

预期：FAIL，编译错误包含 `undefined: RequestBody`。

- [ ] **步骤 3：实现 `RequestBody` 深拷贝、深度合并和统一序列化**

创建 `internal/model/request_body.go`：

```go
package model

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// RequestBody is an arbitrary JSON object merged into a provider request body.
type RequestBody map[string]any

func CloneRequestBody(body RequestBody) RequestBody {
	if body == nil {
		return nil
	}
	cloned := make(RequestBody, len(body))
	for key, value := range body {
		cloned[key] = cloneJSONValue(value)
	}
	return cloned
}

func cloneJSONValue(value any) any {
	switch typed := value.(type) {
	case RequestBody:
		return CloneRequestBody(typed)
	case map[string]any:
		cloned := make(map[string]any, len(typed))
		for key, item := range typed {
			cloned[key] = cloneJSONValue(item)
		}
		return cloned
	case []any:
		cloned := make([]any, len(typed))
		for index, item := range typed {
			cloned[index] = cloneJSONValue(item)
		}
		return cloned
	default:
		return typed
	}
}

func MergeRequestBodies(base, override RequestBody) RequestBody {
	merged := CloneRequestBody(base)
	if merged == nil {
		merged = make(RequestBody)
	}
	for key, overrideValue := range override {
		baseObject, baseOK := jsonObject(merged[key])
		overrideObject, overrideOK := jsonObject(overrideValue)
		if baseOK && overrideOK {
			merged[key] = MergeRequestBodies(baseObject, overrideObject)
			continue
		}
		merged[key] = cloneJSONValue(overrideValue)
	}
	return merged
}

func jsonObject(value any) (RequestBody, bool) {
	switch typed := value.(type) {
	case RequestBody:
		return typed, true
	case map[string]any:
		return RequestBody(typed), true
	default:
		return nil, false
	}
}

func MarshalRequestBody(base any, extra RequestBody) ([]byte, error) {
	baseData, err := json.Marshal(base)
	if err != nil {
		return nil, fmt.Errorf("marshal base request body: %w", err)
	}
	var baseObject RequestBody
	if err := json.Unmarshal(baseData, &baseObject); err != nil {
		return nil, fmt.Errorf("decode base request body object: %w", err)
	}
	merged := MergeRequestBodies(baseObject, extra)
	data, err := json.Marshal(merged)
	if err != nil {
		return nil, fmt.Errorf("marshal merged request body: %w", err)
	}
	return data, nil
}

func EffectiveExtraRequestBody(cfg Config) RequestBody {
	return MergeRequestBodies(cfg.ExtraBody, cfg.ModelExtraBody[strings.TrimSpace(cfg.Model)])
}

func sortedRequestBodyKeys(body RequestBody) []string {
	keys := make([]string, 0, len(body))
	for key := range body {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
```

- [ ] **步骤 4：运行合并测试验证通过**

运行：

```bash
go test ./internal/model -run 'Test(MergeRequestBodies|MarshalRequestBody)' -count=1
```

预期：PASS。

- [ ] **步骤 5：编写保护字段与模型名失败测试**

继续向 `internal/model/request_body_test.go` 添加：

```go
func TestValidateExtraRequestBodiesRejectsProtectedFields(t *testing.T) {
	tests := []struct {
		name      string
		transport string
		body      RequestBody
		want      string
	}{
		{name: "openai stream", transport: "openai-compatible", body: RequestBody{"stream": false}, want: `extraBody contains protected field "stream"`},
		{name: "anthropic system", transport: "anthropic", body: RequestBody{"system": "override"}, want: `extraBody contains protected field "system"`},
		{name: "anthropic fallback stream options", transport: "anthropic", body: RequestBody{"stream_options": map[string]any{}}, want: `extraBody contains protected field "stream_options"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateExtraRequestBodies(Config{
				ProfileID: "gateway",
				Transport: tt.transport,
				Model:     "model-a",
				Models:    []string{"model-a"},
				ExtraBody: tt.body,
			})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestValidateExtraRequestBodiesAllowsNestedProtectedNamesAndAnthropicMaxTokens(t *testing.T) {
	err := ValidateExtraRequestBodies(Config{
		ProfileID: "anthropic",
		Transport: "anthropic",
		Model:     "claude",
		Models:    []string{"claude"},
		ExtraBody: RequestBody{
			"max_tokens": 16384,
			"metadata":   map[string]any{"model": "label"},
		},
	})
	if err != nil {
		t.Fatalf("ValidateExtraRequestBodies() error = %v", err)
	}
}

func TestValidateExtraRequestBodiesRejectsUnknownModel(t *testing.T) {
	err := ValidateExtraRequestBodies(Config{
		ProfileID: "gateway",
		Transport: "openai-compatible",
		Model:     "model-a",
		Models:    []string{"model-a"},
		ModelExtraBody: map[string]RequestBody{
			"model-typo": {"service_tier": "fast"},
		},
	})
	if err == nil || !strings.Contains(err.Error(), `modelExtraBody references unknown model "model-typo"`) {
		t.Fatalf("error = %v", err)
	}
}
```

并把 import 增加为：

```go
import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)
```

- [ ] **步骤 6：运行校验测试验证失败**

运行：

```bash
go test ./internal/model -run TestValidateExtraRequestBodies -count=1
```

预期：FAIL，编译错误包含 `undefined: ValidateExtraRequestBodies`。

- [ ] **步骤 7：实现 transport 保护字段和模型名校验**

向 `internal/model/request_body.go` 添加：

```go
var openAIProtectedRequestFields = map[string]struct{}{
	"model":          {},
	"messages":       {},
	"tools":          {},
	"stream":         {},
	"stream_options": {},
}

var anthropicProtectedRequestFields = map[string]struct{}{
	"model":          {},
	"system":         {},
	"messages":       {},
	"tools":          {},
	"stream":         {},
	"stream_options": {},
}

func ValidateExtraRequestBodies(cfg Config) error {
	profileID := strings.TrimSpace(cfg.ProfileID)
	if profileID == "" {
		profileID = strings.TrimSpace(cfg.Provider)
	}
	if profileID == "" {
		profileID = "default"
	}

	protected := openAIProtectedRequestFields
	if isAnthropicTransport(cfg.Transport) {
		protected = anthropicProtectedRequestFields
	}
	if err := validateProtectedRequestFields(profileID, "extraBody", cfg.ExtraBody, protected); err != nil {
		return err
	}

	knownModels := make(map[string]struct{})
	for _, modelName := range AvailableModels(cfg) {
		knownModels[modelName] = struct{}{}
	}
	for _, modelName := range sortedModelExtraBodyKeys(cfg.ModelExtraBody) {
		if _, ok := knownModels[modelName]; !ok {
			return fmt.Errorf("model profile %q: modelExtraBody references unknown model %q", profileID, modelName)
		}
		location := fmt.Sprintf("modelExtraBody[%q]", modelName)
		if err := validateProtectedRequestFields(profileID, location, cfg.ModelExtraBody[modelName], protected); err != nil {
			return err
		}
	}
	return nil
}

func validateProtectedRequestFields(profileID, location string, body RequestBody, protected map[string]struct{}) error {
	for _, field := range sortedRequestBodyKeys(body) {
		if _, blocked := protected[field]; blocked {
			return fmt.Errorf("model profile %q: %s contains protected field %q", profileID, location, field)
		}
	}
	return nil
}

func sortedModelExtraBodyKeys(values map[string]RequestBody) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func isAnthropicTransport(transport string) bool {
	return strings.Contains(strings.ToLower(strings.TrimSpace(transport)), "anthropic")
}
```

- [ ] **步骤 8：运行请求体模块测试**

运行：

```bash
go test ./internal/model -run 'Test(MergeRequestBodies|MarshalRequestBody|ValidateExtraRequestBodies)' -count=1
```

预期：PASS。

- [ ] **步骤 9：Commit**

```bash
git add internal/model/request_body.go internal/model/request_body_test.go
git commit -m "feat: 增加模型额外请求体合并能力"
```

### 任务 2：扩展配置解析、复制、校验和持久化

**文件：**
- 修改：`internal/model/config.go`
- 修改：`internal/model/config_test.go`

- [ ] **步骤 1：编写严格 JSON object 解析失败测试**

向 `internal/model/config_test.go` 添加：

```go
func TestLoadPawConfigDocumentRejectsNonObjectExtraBodies(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "extra null", body: `"extraBody": null`, want: `extraBody must be a JSON object`},
		{name: "extra string", body: `"extraBody": "fast"`, want: `extraBody must be a JSON object`},
		{name: "model map null", body: `"modelExtraBody": null`, want: `modelExtraBody must be a JSON object`},
		{name: "model value null", body: `"modelExtraBody": {"model-a": null}`, want: `modelExtraBody["model-a"] must be a JSON object`},
		{name: "model value array", body: `"modelExtraBody": {"model-a": []}`, want: `modelExtraBody["model-a"] must be a JSON object`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			data := fmt.Sprintf(`{
				"schemaVersion": 1,
				"activeModelProfileId": "gateway",
				"modelProfiles": [{
					"id": "gateway",
					"transport": "openai-compatible",
					"model": "model-a",
					"models": ["model-a"],
					%s
				}]
			}`, tt.body)
			if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
				t.Fatal(err)
			}
			_, _, err := loadPawConfigDocument(path)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want substring %q", err, tt.want)
			}
		})
	}
}
```

确保 import 包含：

```go
import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)
```

- [ ] **步骤 2：运行严格解析测试验证失败**

运行：

```bash
go test ./internal/model -run TestLoadPawConfigDocumentRejectsNonObjectExtraBodies -count=1
```

预期：FAIL，因为当前 typed unmarshal 会把 `null` 接受为 nil map，错误不符合预期。

- [ ] **步骤 3：扩展配置类型并实现严格自定义解码**

在 `internal/model/config.go` 的 `Config`、`Profile` 中加入：

```go
ExtraBody      RequestBody
ModelExtraBody map[string]RequestBody
```

在 `persistedModelConfig` 中加入非导出的 presence 标记和公开运行值：

```go
ExtraBody          RequestBody
ModelExtraBody     map[string]RequestBody
extraBodySet       bool
modelExtraBodySet  bool
```

并为 `persistedModelConfig` 实现：

```go
func (p *persistedModelConfig) UnmarshalJSON(data []byte) error {
	type alias persistedModelConfig
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	var decoded alias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*p = persistedModelConfig(decoded)

	if raw, ok := fields["extraBody"]; ok {
		p.extraBodySet = true
		body, err := decodeRequiredRequestBody(raw, "extraBody")
		if err != nil {
			return err
		}
		p.ExtraBody = body
	}
	if raw, ok := fields["modelExtraBody"]; ok {
		p.modelExtraBodySet = true
		values, err := decodeModelExtraBodies(raw)
		if err != nil {
			return err
		}
		p.ModelExtraBody = values
	}
	return nil
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
```

保持原有 JSON tag 字段不变；自定义解码的 alias 仍解析 `id`、`models`、`stream` 等现有字段。

- [ ] **步骤 4：运行严格解析测试验证通过**

运行：

```bash
go test ./internal/model -run TestLoadPawConfigDocumentRejectsNonObjectExtraBodies -count=1
```

预期：PASS。

- [ ] **步骤 5：编写配置加载、校验和深拷贝测试**

向 `internal/model/config_test.go` 添加：

```go
func TestConfiguredProfilesLoadAndDeepCopyExtraBodies(t *testing.T) {
	raw := []persistedModelConfig{{
		ID:        "gateway",
		Transport: "openai-compatible",
		Model:     "model-a",
		Models:    []string{"model-a", "model-b"},
		ExtraBody: RequestBody{
			"metadata": map[string]any{"team": "platform"},
		},
		ModelExtraBody: map[string]RequestBody{
			"model-a": {"service_tier": "fast"},
		},
	}}
	profiles, err := configuredProfiles(raw, nil)
	if err != nil {
		t.Fatalf("configuredProfiles() error = %v", err)
	}
	cfg := profiles[0].Config()
	cfg.ExtraBody["metadata"].(map[string]any)["team"] = "changed"
	cfg.ModelExtraBody["model-a"]["service_tier"] = "slow"
	if profiles[0].ExtraBody["metadata"].(map[string]any)["team"] != "platform" {
		t.Fatalf("Profile.Config shared ExtraBody: %#v", profiles[0].ExtraBody)
	}
	if profiles[0].ModelExtraBody["model-a"]["service_tier"] != "fast" {
		t.Fatalf("Profile.Config shared ModelExtraBody: %#v", profiles[0].ModelExtraBody)
	}
}

func TestConfiguredProfilesRejectInvalidExtraBodyConfiguration(t *testing.T) {
	_, err := configuredProfiles([]persistedModelConfig{{
		ID:        "gateway",
		Transport: "openai-compatible",
		Model:     "model-a",
		Models:    []string{"model-a"},
		ModelExtraBody: map[string]RequestBody{
			"model-typo": {"service_tier": "fast"},
		},
	}}, nil)
	if err == nil || !strings.Contains(err.Error(), `modelExtraBody references unknown model "model-typo"`) {
		t.Fatalf("error = %v", err)
	}
}
```

- [ ] **步骤 6：运行配置转换测试验证失败**

运行：

```bash
go test ./internal/model -run 'TestConfiguredProfiles(LoadAndDeepCopy|RejectInvalid)' -count=1
```

预期：FAIL，因为 `configuredProfiles` 仍返回单值，且尚未复制/校验新字段。

- [ ] **步骤 7：让 Profile/Config 转换深拷贝并返回校验错误**

修改 `configuredProfiles` 签名：

```go
func configuredProfiles(persisted []persistedModelConfig, envValues map[string]string) ([]Profile, error)
```

构造 `Profile` 时加入：

```go
ExtraBody:      CloneRequestBody(raw.ExtraBody),
ModelExtraBody: CloneModelExtraBodies(raw.ModelExtraBody),
```

每个 Profile 完成默认 ID 处理后执行：

```go
if err := ValidateExtraRequestBodies(profile.Config()); err != nil {
	return nil, err
}
```

函数结尾改为：

```go
return profiles, nil
```

在 `request_body.go` 添加：

```go
func CloneModelExtraBodies(values map[string]RequestBody) map[string]RequestBody {
	if values == nil {
		return nil
	}
	cloned := make(map[string]RequestBody, len(values))
	for modelName, body := range values {
		cloned[modelName] = CloneRequestBody(body)
	}
	return cloned
}
```

`Profile.Config()`、`cloneProfiles()`、`ConfiguredProfiles()` fallback 都加入深拷贝字段。`LoadConfigFromEnv()` 改为：

```go
profiles, err := configuredProfiles(persisted.ModelProfiles, fileEnvValues)
if err != nil {
	return Config{}, err
}
```

- [ ] **步骤 8：运行配置转换测试验证通过**

运行：

```bash
go test ./internal/model -run 'TestConfiguredProfiles(LoadAndDeepCopy|RejectInvalid)' -count=1
```

预期：PASS。

- [ ] **步骤 9：编写保存 round-trip 测试**

向 `internal/model/config_test.go` 添加：

```go
func TestSaveModelConfigPreservesExtraBodies(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".paw", "config.json")
	cfg := Config{
		ProfileID:   "gateway",
		ProfileName: "Gateway",
		Provider:    "openai",
		Transport:   "openai-compatible",
		APIBaseURL:  "https://example.test/v1",
		APIPath:     "/chat/completions",
		Model:       "model-a",
		Models:      []string{"model-a"},
		ExtraBody: RequestBody{
			"metadata": map[string]any{"team": "platform"},
		},
		ModelExtraBody: map[string]RequestBody{
			"model-a": {"service_tier": "fast"},
		},
	}
	if err := saveModelConfigAtPath(cfg, path); err != nil {
		t.Fatalf("saveModelConfigAtPath() error = %v", err)
	}
	_, persisted, err := loadPawConfigDocument(path)
	if err != nil {
		t.Fatalf("loadPawConfigDocument() error = %v", err)
	}
	got := persisted.ModelProfiles[0]
	if !reflect.DeepEqual(got.ExtraBody, cfg.ExtraBody) {
		t.Fatalf("ExtraBody = %#v, want %#v", got.ExtraBody, cfg.ExtraBody)
	}
	if !reflect.DeepEqual(got.ModelExtraBody, cfg.ModelExtraBody) {
		t.Fatalf("ModelExtraBody = %#v, want %#v", got.ModelExtraBody, cfg.ModelExtraBody)
	}
}
```

- [ ] **步骤 10：运行保存测试验证失败**

运行：

```bash
go test ./internal/model -run TestSaveModelConfigPreservesExtraBodies -count=1
```

预期：FAIL，因为保存逻辑尚未写入新字段。

- [ ] **步骤 11：持久化前校验并显式保存新字段**

在 `saveModelConfigAtPath` 的 `fillConfigDefaults` 后加入：

```go
if err := ValidateExtraRequestBodies(cfg); err != nil {
	return err
}
```

写 Profile document 时加入：

```go
if len(cfg.ExtraBody) > 0 {
	profile["extraBody"] = CloneRequestBody(cfg.ExtraBody)
} else {
	delete(profile, "extraBody")
}
if len(cfg.ModelExtraBody) > 0 {
	profile["modelExtraBody"] = CloneModelExtraBodies(cfg.ModelExtraBody)
} else {
	delete(profile, "modelExtraBody")
}
```

- [ ] **步骤 12：运行 model 配置完整测试**

运行：

```bash
go test ./internal/model -run 'Test(LoadPawConfigDocument|ConfiguredProfiles|SaveModelConfig)' -count=1
```

预期：PASS。

- [ ] **步骤 13：Commit**

```bash
git add internal/model/config.go internal/model/config_test.go internal/model/request_body.go
git commit -m "feat: 支持模型额外请求体配置"
```

### 任务 3：在 Client 边界校验并接入简单 OpenAI 请求

**文件：**
- 修改：`internal/model/client.go`
- 修改：`internal/model/client_test.go`

- [ ] **步骤 1：编写 ApplyModelConfig 校验与复制测试**

向 `internal/model/client_test.go` 添加：

```go
func TestApplyModelConfigRejectsProtectedExtraBody(t *testing.T) {
	client := NewClient(Config{Model: "model-a", Models: []string{"model-a"}})
	err := client.ApplyModelConfig(Config{
		ProfileID: "gateway",
		Transport: "openai-compatible",
		Model:     "model-a",
		Models:    []string{"model-a"},
		ExtraBody: RequestBody{"stream": false},
	})
	if err == nil || !strings.Contains(err.Error(), `protected field "stream"`) {
		t.Fatalf("error = %v", err)
	}
}

func TestApplyModelConfigStoresDeepCopy(t *testing.T) {
	cfg := Config{
		ProfileID: "gateway",
		Transport: "openai-compatible",
		Model:     "model-a",
		Models:    []string{"model-a"},
		ExtraBody: RequestBody{"metadata": map[string]any{"team": "platform"}},
	}
	client := NewClient(cfg)
	cfg.ExtraBody["metadata"].(map[string]any)["team"] = "changed"
	got := client.CurrentModelConfig()
	if got.ExtraBody["metadata"].(map[string]any)["team"] != "platform" {
		t.Fatalf("client config shared request body: %#v", got.ExtraBody)
	}
	got.ExtraBody["metadata"].(map[string]any)["team"] = "changed-again"
	if client.CurrentModelConfig().ExtraBody["metadata"].(map[string]any)["team"] != "platform" {
		t.Fatal("CurrentModelConfig returned shared request body")
	}
}
```

确保 import 包含 `strings`。

- [ ] **步骤 2：运行 Client 配置测试验证失败**

运行：

```bash
go test ./internal/model -run 'TestApplyModelConfig(RejectsProtectedExtraBody|StoresDeepCopy)' -count=1
```

预期：FAIL，当前 `ApplyModelConfig` 不校验且 Client 保存共享 map。

- [ ] **步骤 3：实现完整 Config 深拷贝和 Client 边界校验**

在 `request_body.go` 添加：

```go
func CloneConfig(cfg Config) Config {
	cfg.Models = append([]string(nil), cfg.Models...)
	cfg.ExtraBody = CloneRequestBody(cfg.ExtraBody)
	cfg.ModelExtraBody = CloneModelExtraBodies(cfg.ModelExtraBody)
	cfg.Profiles = cloneProfiles(cfg.Profiles)
	return cfg
}
```

修改 `NewClient`：

```go
func NewClient(cfg Config) *Client {
	cfg = CloneConfig(fillConfigDefaults(cfg))
	return &Client{
		httpClient: &http.Client{Timeout: cfg.Timeout},
		cfg:        cfg,
	}
}
```

修改 `CurrentModelConfig` 返回：

```go
return CloneConfig(c.cfg)
```

修改 `ApplyModelConfig`：

```go
cfg = fillConfigDefaults(cfg)
if err := ValidateExtraRequestBodies(cfg); err != nil {
	return err
}
// 保留现有 API key 加载逻辑
cfg = CloneConfig(cfg)
```

- [ ] **步骤 4：运行 Client 配置测试验证通过**

运行：

```bash
go test ./internal/model -run 'TestApplyModelConfig(RejectsProtectedExtraBody|StoresDeepCopy)' -count=1
```

预期：PASS。

- [ ] **步骤 5：编写 RunMessage 请求体透传测试**

向 `internal/model/client_test.go` 添加：

```go
func TestRunMessageMergesProfileAndModelExtraBody(t *testing.T) {
	var captured map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Errorf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`)
	}))
	defer server.Close()

	client := NewClient(Config{
		ProfileID: "gateway",
		Transport: "openai-compatible",
		APIBaseURL: server.URL,
		APIPath:   "/chat/completions",
		Model:     "gpt-5.6-sol",
		Models:    []string{"gpt-5.6-sol"},
		ExtraBody: RequestBody{
			"metadata": map[string]any{"team": "platform", "environment": "production"},
		},
		ModelExtraBody: map[string]RequestBody{
			"gpt-5.6-sol": {
				"service_tier": "fast",
				"metadata": map[string]any{"environment": "development"},
			},
		},
	})
	_, err := client.RunMessage(context.Background(), []message.Message{{Role: message.RoleUser, Content: "hello"}})
	if err != nil {
		t.Fatalf("RunMessage() error = %v", err)
	}
	if captured["service_tier"] != "fast" {
		t.Fatalf("service_tier = %#v", captured["service_tier"])
	}
	metadata := captured["metadata"].(map[string]any)
	if metadata["team"] != "platform" || metadata["environment"] != "development" {
		t.Fatalf("metadata = %#v", metadata)
	}
}
```

使用该文件已有 import 风格补充：`encoding/json`、`io`、`net/http`、`net/http/httptest` 和 `paw/internal/message`（若尚未存在）。

- [ ] **步骤 6：运行 RunMessage 测试验证失败**

运行：

```bash
go test ./internal/model -run TestRunMessageMergesProfileAndModelExtraBody -count=1
```

预期：FAIL，捕获请求中没有 `service_tier`。

- [ ] **步骤 7：让 RunMessage 使用统一请求体序列化**

将 `client.go` 中：

```go
bodyBytes, err := json.Marshal(reqBody)
```

替换为：

```go
bodyBytes, err := MarshalRequestBody(reqBody, EffectiveExtraRequestBody(cfg))
```

保留原有错误包装文本；如 `encoding/json` 仅用于该 marshal，清理未使用 import。

- [ ] **步骤 8：运行 client 测试**

运行：

```bash
go test ./internal/model -run 'Test(RunMessage|ApplyModelConfig)' -count=1
```

预期：PASS。

- [ ] **步骤 9：Commit**

```bash
git add internal/model/client.go internal/model/client_test.go internal/model/request_body.go
git commit -m "feat: 在模型客户端应用额外请求参数"
```

### 任务 4：接入 OpenAI-compatible 流式与非流式请求

**文件：**
- 修改：`internal/model/stream.go`
- 修改：`internal/model/stream_test.go`

- [ ] **步骤 1：编写 OpenAI 非流式和流式请求捕获测试**

向 `internal/model/stream_test.go` 添加 helper：

```go
func captureJSONRequest(t *testing.T, response string) (*httptest.Server, <-chan map[string]any) {
	t.Helper()
	captured := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		captured <- body
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, response)
	}))
	return server, captured
}
```

添加测试：

```go
func TestStreamMessageAppliesExtraBodyToOpenAIPaths(t *testing.T) {
	tests := []struct {
		name     string
		stream   bool
		response string
	}{
		{
			name:   "non-streaming",
			stream: false,
			response: `{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`,
		},
		{
			name:   "streaming",
			stream: true,
			response: "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server, captured := captureJSONRequest(t, tt.response)
			defer server.Close()
			cfg := Config{
				ProfileID: "gateway",
				Transport: "openai-compatible",
				APIBaseURL: server.URL,
				APIPath:   "/chat/completions",
				Model:     "model-a",
				Models:    []string{"model-a", "model-b"},
				Stream:    tt.stream,
				streamSet: true,
				ModelExtraBody: map[string]RequestBody{
					"model-a": {"service_tier": "fast"},
					"model-b": {"service_tier": "slow"},
				},
			}
			client := NewClient(cfg)
			events, err := client.StreamMessage(context.Background(), []message.Message{{Role: message.RoleUser, Content: "hello"}}, nil)
			if err != nil {
				t.Fatalf("StreamMessage() error = %v", err)
			}
			for range events {
			}
			body := <-captured
			if body["service_tier"] != "fast" {
				t.Fatalf("service_tier = %#v", body["service_tier"])
			}
			if body["model"] != "model-a" || body["stream"] != tt.stream {
				t.Fatalf("base fields changed: %#v", body)
			}
		})
	}
}
```

使用文件已有 imports 补充 `encoding/json`、`io`、`net/http`、`net/http/httptest`。

- [ ] **步骤 2：运行 OpenAI StreamMessage 测试验证失败**

运行：

```bash
go test ./internal/model -run TestStreamMessageAppliesExtraBodyToOpenAIPaths -count=1
```

预期：FAIL，`service_tier` 不存在。

- [ ] **步骤 3：统一修改两个 OpenAI StreamMessage 请求路径**

在 `nonStreamingOpenAIMessage()` 和 `streamOpenAIMessage()` 中，将：

```go
bodyBytes, err := json.Marshal(reqBody)
```

替换为：

```go
bodyBytes, err := MarshalRequestBody(reqBody, EffectiveExtraRequestBody(cfg))
```

两处都保留各自现有错误上下文。若 `stream.go` 仍用于解析响应 JSON，则不要移除 `encoding/json` import。

- [ ] **步骤 4：运行 OpenAI 请求测试验证通过**

运行：

```bash
go test ./internal/model -run 'Test(StreamMessageAppliesExtraBodyToOpenAIPaths|RunMessageMergesProfileAndModelExtraBody)' -count=1
```

预期：PASS。

- [ ] **步骤 5：运行现有流解析测试**

运行：

```bash
go test ./internal/model -run 'Test(Stream|NonStreaming|OpenAI)' -count=1
```

预期：PASS；没有额外配置的现有测试请求行为不变。

- [ ] **步骤 6：Commit**

```bash
git add internal/model/stream.go internal/model/stream_test.go
git commit -m "feat: 为 OpenAI 请求透传模型参数"
```

### 任务 5：接入 Anthropic 请求并允许覆盖 max_tokens

**文件：**
- 修改：`internal/model/anthropic_stream.go`
- 修改：`internal/model/stream_test.go`

- [ ] **步骤 1：编写 Anthropic 默认值与覆盖测试**

向 `internal/model/stream_test.go` 添加：

```go
func TestAnthropicRequestAppliesExtraBodyAndMaxTokensOverride(t *testing.T) {
	tests := []struct {
		name       string
		extra      RequestBody
		modelExtra RequestBody
		wantMax    float64
	}{
		{name: "default max tokens", wantMax: 8192},
		{
			name: "model override",
			extra: RequestBody{
				"metadata": map[string]any{"team": "platform", "environment": "production"},
			},
			modelExtra: RequestBody{
				"max_tokens": 16384,
				"metadata": map[string]any{"environment": "development"},
			},
			wantMax: 16384,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server, captured := captureJSONRequest(t, "data: {\"type\":\"message_stop\"}\n\n")
			defer server.Close()
			cfg := Config{
				ProfileID: "anthropic",
				Transport: "anthropic",
				APIBaseURL: server.URL,
				APIPath:   "/v1/messages",
				Model:     "claude-sonnet",
				Models:    []string{"claude-sonnet"},
				Stream:    true,
				streamSet: true,
				ExtraBody: tt.extra,
			}
			if tt.modelExtra != nil {
				cfg.ModelExtraBody = map[string]RequestBody{"claude-sonnet": tt.modelExtra}
			}
			client := NewClient(cfg)
			events, err := client.StreamMessage(context.Background(), []message.Message{
				{Role: message.RoleSystem, Content: "system"},
				{Role: message.RoleUser, Content: "hello"},
			}, nil)
			if err != nil {
				t.Fatalf("StreamMessage() error = %v", err)
			}
			for range events {
			}
			body := <-captured
			if body["max_tokens"] != tt.wantMax {
				t.Fatalf("max_tokens = %#v, want %v", body["max_tokens"], tt.wantMax)
			}
			if tt.modelExtra != nil {
				metadata := body["metadata"].(map[string]any)
				if metadata["team"] != "platform" || metadata["environment"] != "development" {
					t.Fatalf("metadata = %#v", metadata)
				}
			}
			if body["model"] != "claude-sonnet" || body["stream"] != true {
				t.Fatalf("protected base fields changed: %#v", body)
			}
		})
	}
}
```

- [ ] **步骤 2：运行 Anthropic 测试验证失败**

运行：

```bash
go test ./internal/model -run TestAnthropicRequestAppliesExtraBodyAndMaxTokensOverride -count=1
```

预期：默认子测试 PASS，覆盖子测试 FAIL，`max_tokens` 仍为 `8192`。

- [ ] **步骤 3：在 Anthropic 网络序列化前合并额外请求体**

在 `streamAnthropicMessage()` 中，将：

```go
bodyBytes, err := json.Marshal(requestBody)
```

替换为：

```go
bodyBytes, err := MarshalRequestBody(requestBody, EffectiveExtraRequestBody(cfg))
```

保留 `buildAnthropicMessagesRequest()` 的：

```go
MaxTokens: anthropicDefaultMaxTokens,
```

这样无配置时默认值保持 8192，有配置时通过深度合并覆盖。

- [ ] **步骤 4：运行 Anthropic 测试验证通过**

运行：

```bash
go test ./internal/model -run TestAnthropicRequestAppliesExtraBodyAndMaxTokensOverride -count=1
```

预期：PASS。

- [ ] **步骤 5：运行 model 包完整测试**

运行：

```bash
go test ./internal/model -count=1
```

预期：PASS。

- [ ] **步骤 6：Commit**

```bash
git add internal/model/anthropic_stream.go internal/model/stream_test.go
git commit -m "feat: 为 Anthropic 请求透传模型参数"
```

### 任务 6：验证 `/model` 切换保留并选择正确参数

**文件：**
- 修改：`internal/ui/bubble/bubble_test.go`
- 仅在测试暴露缺陷时修改：`internal/ui/bubble/model_wizard.go`

- [ ] **步骤 1：为 fake model controller 使用隔离副本**

先把 `internal/ui/bubble/bubble_test.go` 中 fake controller 的三个边界改成与真实 Client 一致：

```go
func (c *fakeModelConfigController) CurrentModelConfig() modelcfg.Config {
	return modelcfg.CloneConfig(c.current)
}

func (c *fakeModelConfigController) ApplyModelConfig(cfg modelcfg.Config) error {
	if c.err != nil {
		return c.err
	}
	cfg = modelcfg.CloneConfig(cfg)
	c.applied = append(c.applied, cfg)
	c.current = cfg
	return nil
}

func (c *fakeModelConfigController) SaveModelConfig(cfg modelcfg.Config) error {
	if c.saveErr != nil {
		return c.saveErr
	}
	c.saved = append(c.saved, modelcfg.CloneConfig(cfg))
	return nil
}
```

- [ ] **步骤 2：编写 Profile.Config 与模型选择保留测试**

向 `internal/ui/bubble/bubble_test.go` 添加：

```go
func TestModelWizardKeepsExtraBodiesWhenSelectingModel(t *testing.T) {
	controller := &fakeModelConfigController{current: modelcfg.Config{
		ProfileID: "gateway",
		Model:     "model-a",
		Models:    []string{"model-a", "model-b"},
		Profiles: []modelcfg.Profile{{
			ID:        "gateway",
			Name:      "Gateway",
			Transport: "openai-compatible",
			Model:     "model-a",
			Models:    []string{"model-a", "model-b"},
			ExtraBody: modelcfg.RequestBody{
				"metadata": map[string]any{"team": "platform"},
			},
			ModelExtraBody: map[string]modelcfg.RequestBody{
				"model-a": {"service_tier": "fast"},
				"model-b": {"service_tier": "slow"},
			},
		}},
	}}
	model := newModel(context.Background(), &fakeRunner{}, "session-1", controller, nil, nil, nil, newTerminalCursorAnchor())
	model.modelWizard = newModelWizard(controller.current)
	model.prepareModelWizardStep()
	model.modelWizard.selectedModel = 1

	next, cmd := model.handleModelWizardKey(tea.KeyMsg{Type: tea.KeyEnter})
	model = next.(appModel)
	if cmd != nil {
		_ = cmd()
	}
	if len(controller.applied) != 1 {
		t.Fatalf("applied configs = %d, want 1", len(controller.applied))
	}
	got := controller.applied[0]
	if got.Model != "model-b" {
		t.Fatalf("Model = %q, want model-b", got.Model)
	}
	if got.ModelExtraBody["model-b"]["service_tier"] != "slow" {
		t.Fatalf("ModelExtraBody lost: %#v", got.ModelExtraBody)
	}
	if got.ExtraBody["metadata"].(map[string]any)["team"] != "platform" {
		t.Fatalf("ExtraBody lost: %#v", got.ExtraBody)
	}
}
```

如果现有向导需要先从 provider step Enter 才进入 model step，按现有测试模式执行两次 `handleModelWizardKey(Enter)`：第一次选择 Profile，第二次确认 `selectedModel = 1`；不要绕过生产状态机直接调用保存函数。

- [ ] **步骤 3：运行模型向导测试**

运行：

```bash
go test ./internal/ui/bubble -run TestModelWizardKeepsExtraBodiesWhenSelectingModel -count=1
```

预期：PASS。如果 FAIL 且 `ExtraBody`/`ModelExtraBody` 丢失，修改 `configForProfile()` 或确认逻辑，使其只改 `cfg.Model`，不重建丢失 map 的 Config。

- [ ] **步骤 4：运行 model wizard 相关回归测试**

运行：

```bash
go test ./internal/ui/bubble -run 'TestModel' -count=1
```

预期：PASS。

- [ ] **步骤 5：Commit**

```bash
git add internal/ui/bubble/bubble_test.go internal/ui/bubble/model_wizard.go
git commit -m "test: 覆盖模型参数切换与保留"
```

如果 `model_wizard.go` 未修改，不要把它加入 `git add`。

### 任务 7：文档示例、格式化和全量验证

**文件：**
- 修改：`docs/superpowers/specs/2026-07-29-model-extra-request-body.md`（仅在实现 API 与规格命名有必要同步时）
- 验证：所有本计划涉及文件

- [ ] **步骤 1：运行 gofmt**

运行：

```bash
gofmt -w \
  internal/model/request_body.go \
  internal/model/request_body_test.go \
  internal/model/config.go \
  internal/model/config_test.go \
  internal/model/client.go \
  internal/model/client_test.go \
  internal/model/stream.go \
  internal/model/stream_test.go \
  internal/model/anthropic_stream.go \
  internal/ui/bubble/bubble_test.go
```

预期：命令成功，无输出。

- [ ] **步骤 2：运行 model 包测试**

运行：

```bash
go test ./internal/model -count=1
```

预期：PASS。

- [ ] **步骤 3：运行 bubble 包测试**

运行：

```bash
go test ./internal/ui/bubble -count=1
```

预期：PASS。

- [ ] **步骤 4：运行竞态测试覆盖配置共享引用**

运行：

```bash
go test -race ./internal/model ./internal/ui/bubble -count=1
```

预期：PASS，无 data race。

- [ ] **步骤 5：运行全项目测试**

运行：

```bash
go test ./... -count=1
```

预期：PASS。

- [ ] **步骤 6：检查 diff 质量**

运行：

```bash
git diff --check
git status --short
git diff -- internal/model internal/ui/bubble docs/superpowers/specs/2026-07-29-model-extra-request-body.md
```

预期：

- `git diff --check` 无输出；
- 没有意外修改用户的现有 transcript 自动跟随改动；
- 没有把 API key 或完整敏感配置写入日志/错误；
- `service_tier` 只出现在配置/测试示例中，没有新增强类型 `ServiceTier` 字段；
- 未配置额外参数时仍使用原有基础请求体。

- [ ] **步骤 7：最终 Commit**

```bash
git add \
  internal/model/request_body.go \
  internal/model/request_body_test.go \
  internal/model/config.go \
  internal/model/config_test.go \
  internal/model/client.go \
  internal/model/client_test.go \
  internal/model/stream.go \
  internal/model/stream_test.go \
  internal/model/anthropic_stream.go \
  internal/ui/bubble/bubble_test.go \
  docs/superpowers/specs/2026-07-29-model-extra-request-body.md
git commit -m "feat: 支持模型级额外请求参数"
```

如果前面各任务已按计划分别提交，本步骤只提交仍未提交的文档或收尾修改；不要制造空 commit。
