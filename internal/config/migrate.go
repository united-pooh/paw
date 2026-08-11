package config

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type legacyDocument struct {
	SchemaVersion        int             `json:"schemaVersion"`
	ModelProfiles        []legacyProfile `json:"modelProfiles"`
	ActiveModelProfileID string          `json:"activeModelProfileId"`
}

type legacyProfile struct {
	ID                      string                    `json:"id"`
	Name                    string                    `json:"name"`
	Provider                string                    `json:"provider"`
	Transport               string                    `json:"transport"`
	BaseURL                 string                    `json:"baseUrl"`
	APIPath                 string                    `json:"apiPath"`
	APIKeyEnvName           string                    `json:"apiKeyEnvName"`
	APIKey                  string                    `json:"apiKey"`
	CredentialID            string                    `json:"credentialId"`
	Model                   string                    `json:"model"`
	Models                  []string                  `json:"models"`
	TimeoutSeconds          int                       `json:"timeoutSeconds"`
	RetryCount              int                       `json:"retryCount"`
	Stream                  *bool                     `json:"stream"`
	ExtraBody               map[string]any            `json:"extraBody"`
	ModelExtraBody          map[string]map[string]any `json:"modelExtraBody"`
	ContextLimitTokens      int                       `json:"context_limit_tokens"`
	ModelContextLimitTokens map[string]int            `json:"model_context_limit_tokens"`
}

func migrateLegacy(ctx context.Context, paths Paths, store CredentialStore) (bool, []Diagnostic, error) {
	if _, err := os.Stat(paths.GlobalConfig); err == nil {
		return false, nil, nil
	}
	if _, err := os.Stat(paths.LegacyConfig); err != nil {
		if os.IsNotExist(err) {
			return false, nil, nil
		}
		return false, nil, err
	}
	raw, err := os.ReadFile(paths.LegacyConfig)
	if err != nil {
		return false, nil, err
	}
	var legacy legacyDocument
	if err := json.Unmarshal(raw, &legacy); err != nil {
		return false, nil, fmt.Errorf("read legacy config %s: %w", paths.LegacyConfig, err)
	}
	if len(legacy.ModelProfiles) == 0 {
		return false, nil, nil
	}

	document := Document{Schema: "./schemas/config-v2.schema.json", SchemaVersion: SchemaVersion, Providers: map[string]Provider{}, Models: map[string]Model{}}
	usedProviderIDs := map[string]int{}
	activeProfile := strings.TrimSpace(legacy.ActiveModelProfileID)
	for index, profile := range legacy.ModelProfiles {
		providerID := sanitizeID(firstNonEmpty(profile.ID, profile.Provider, fmt.Sprintf("provider-%d", index+1)))
		if count := usedProviderIDs[providerID]; count > 0 {
			providerID = fmt.Sprintf("%s-%d", providerID, count+1)
		}
		usedProviderIDs[providerID]++
		credentialID := strings.TrimSpace(profile.CredentialID)
		if credentialID == "" {
			credentialID = "provider/" + providerID
		}
		if secret := strings.TrimSpace(profile.APIKey); secret != "" {
			if store == nil {
				return false, nil, fmt.Errorf("%w: legacy profile %q contains a plaintext API key but no credential store is available", ErrCredentialMigrationBlocked, providerID)
			}
			if err := store.Set(ctx, credentialID, secret); err != nil {
				return false, nil, fmt.Errorf("%w: legacy profile %q API key was not migrated: %v; configure an environment variable and retry", ErrCredentialMigrationBlocked, providerID, err)
			}
		}
		provider := Provider{
			Transport: profile.Transport, Endpoint: profile.BaseURL, APIPath: profile.APIPath,
			Auth: Auth{Credential: credentialID}, Body: cloneAnyMap(profile.ExtraBody),
			TimeoutSeconds: profile.TimeoutSeconds, Stream: profile.Stream,
		}
		if profile.RetryCount > 0 {
			provider.Retries = intPointer(profile.RetryCount)
		}
		if env := strings.TrimSpace(profile.APIKeyEnvName); env != "" {
			provider.Auth.Env = []string{env}
		}
		if _, ok := builtinPresets[strings.ToLower(profile.Provider)]; ok {
			provider.Preset = strings.ToLower(profile.Provider)
		}
		document.Providers[providerID] = provider

		modelNames := append([]string(nil), profile.Models...)
		if len(modelNames) == 0 && strings.TrimSpace(profile.Model) != "" {
			modelNames = []string{profile.Model}
		}
		for _, upstream := range modelNames {
			upstream = strings.TrimSpace(upstream)
			if upstream == "" {
				continue
			}
			modelID := uniqueModelID(document.Models, providerID+"/"+upstream)
			contextWindow := profile.ContextLimitTokens
			if value := profile.ModelContextLimitTokens[upstream]; value > 0 {
				contextWindow = value
			}
			parameters := cloneAnyMap(profile.ModelExtraBody[upstream])
			adapter := ""
			if strings.EqualFold(profile.Provider, "deepseek") {
				adapter = AdapterDeepSeek
			}
			if strings.EqualFold(profile.Provider, "openai") || strings.EqualFold(profile.Provider, "gpt") {
				adapter = AdapterGPT
			}
			document.Models[modelID] = Model{Provider: providerID, Name: upstream, Adapter: adapter, ContextWindow: contextWindow, Parameters: parameters}
			if document.ActiveModel == "" && ((activeProfile != "" && profile.ID == activeProfile && upstream == firstNonEmpty(profile.Model, modelNames[0])) || (activeProfile == "" && index == 0)) {
				document.ActiveModel = modelID
			}
		}
	}
	if document.ActiveModel == "" {
		ids := sortedModelIDs(document.Models)
		if len(ids) > 0 {
			document.ActiveModel = ids[0]
		}
	}
	if _, err := validateDocument(document, paths.GlobalConfig); err != nil {
		return false, nil, fmt.Errorf("migrate legacy config: %w", err)
	}
	encoded, err := marshalStarter(document, "Migrated from "+paths.LegacyConfig)
	if err != nil {
		return false, nil, err
	}
	if err := atomicWriteNewConfigFile(paths.GlobalConfig, encoded, 0o600); err != nil {
		if errors.Is(err, ErrRevisionConflict) {
			if _, statErr := os.Stat(paths.GlobalConfig); statErr == nil {
				return false, nil, nil
			}
		}
		return false, nil, err
	}
	if err := atomicWriteFile(filepath.Join(paths.Home, "config-v1.backup.json"), raw, 0o600); err != nil {
		return false, nil, err
	}
	marker, _ := json.MarshalIndent(map[string]any{"schemaVersion": 2, "source": paths.LegacyConfig, "migratedAt": time.Now().UTC().Format(time.RFC3339)}, "", "  ")
	if err := atomicWriteFile(paths.MigrationMarker, append(marker, '\n'), 0o600); err != nil {
		return false, nil, err
	}
	return true, []Diagnostic{{Severity: "info", File: paths.GlobalConfig, Message: "legacy Paw configuration was copied and migrated; the original was preserved"}}, nil
}

func copyLegacyAssets(paths Paths) []Diagnostic {
	var diagnostics []Diagnostic
	assets := []struct {
		source, destination string
		directory           bool
	}{
		{filepath.Join(paths.LegacyHome, "settings.json"), paths.Settings, false},
		{filepath.Join(paths.LegacyHome, "mcp.toml"), paths.MCP, false},
		{filepath.Join(paths.LegacyHome, "skills"), paths.Skills, true},
	}
	for _, asset := range assets {
		if _, err := os.Stat(asset.destination); err == nil && !asset.directory {
			continue
		}
		if _, err := os.Stat(asset.source); err != nil {
			continue
		}
		var err error
		if asset.directory {
			err = copyDirectory(asset.source, asset.destination)
		} else {
			err = copyFileExclusive(asset.source, asset.destination)
		}
		if err != nil {
			diagnostics = append(diagnostics, Diagnostic{Severity: "warning", File: asset.source, Message: "legacy asset was not copied: " + err.Error()})
		}
	}
	return diagnostics
}

func copyFileExclusive(source, destination string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return err
	}
	output, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		_ = output.Close()
		if !ok {
			_ = os.Remove(destination)
		}
	}()
	if _, err := io.Copy(output, input); err != nil {
		return err
	}
	if err := output.Sync(); err != nil {
		return err
	}
	ok = true
	return output.Close()
}

func copyDirectory(source, destination string) error {
	return filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if _, err := os.Stat(target); err == nil {
			return nil
		}
		return copyFileExclusive(path, target)
	})
}

func sanitizeID(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var out strings.Builder
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			out.WriteRune(r)
		} else if out.Len() > 0 {
			out.WriteByte('-')
		}
	}
	return strings.Trim(out.String(), "-")
}
func uniqueModelID(existing map[string]Model, base string) string {
	if _, ok := existing[base]; !ok {
		return base
	}
	for n := 2; ; n++ {
		id := fmt.Sprintf("%s-%d", base, n)
		if _, ok := existing[id]; !ok {
			return id
		}
	}
}
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
func sortedModelIDs(values map[string]Model) []string {
	ids := make([]string, 0, len(values))
	for id := range values {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
