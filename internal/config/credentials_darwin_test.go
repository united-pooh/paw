//go:build darwin && cgo

package config

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDarwinCredentialStatusMapping(t *testing.T) {
	if err := darwinCredentialError("read", darwinErrSecSuccess); err != nil {
		t.Fatalf("success: %v", err)
	}
	if err := darwinCredentialError("read", darwinErrSecItemNotFound); !errors.Is(err, ErrCredentialNotFound) {
		t.Fatalf("not found: %v", err)
	}
	if err := darwinCredentialError("read", darwinErrSecNotAvailable); !errors.Is(err, ErrCredentialStoreUnavailable) {
		t.Fatalf("unavailable: %v", err)
	}
	err := darwinCredentialError("write", darwinErrSecAuthFailed)
	if err == nil || !strings.Contains(err.Error(), "write credential in macOS Keychain") || !strings.Contains(err.Error(), "-25293") {
		t.Fatalf("auth failure: %v", err)
	}
}

func TestDarwinCredentialRejectsEmptyInputWithoutKeychainAccess(t *testing.T) {
	if _, err := platformCredentialGet(""); !errors.Is(err, ErrCredentialNotFound) {
		t.Fatalf("get empty: %v", err)
	}
	if err := platformCredentialSet("", "secret"); !errors.Is(err, ErrCredentialNotFound) {
		t.Fatalf("set empty ID: %v", err)
	}
	if err := platformCredentialSet("provider/test", ""); !errors.Is(err, ErrCredentialNotFound) {
		t.Fatalf("set empty secret: %v", err)
	}
	if err := platformCredentialDelete(""); !errors.Is(err, ErrCredentialNotFound) {
		t.Fatalf("delete empty: %v", err)
	}
}

func TestDarwinCredentialLifecycle(t *testing.T) {
	if os.Getenv("PAW_TEST_MACOS_KEYCHAIN") != "1" {
		t.Skip("set PAW_TEST_MACOS_KEYCHAIN=1 to exercise the login keychain")
	}
	id := "test/codex-" + time.Now().UTC().Format("20060102T150405.000000000")
	t.Cleanup(func() { _ = platformCredentialDelete(id) })

	if err := platformCredentialSet(id, "first-secret"); err != nil {
		t.Fatal(err)
	}
	if got, err := platformCredentialGet(id); err != nil || got != "first-secret" {
		t.Fatalf("first get=%q err=%v", got, err)
	}
	if err := platformCredentialSet(id, "second-secret"); err != nil {
		t.Fatal(err)
	}
	if got, err := platformCredentialGet(id); err != nil || got != "second-secret" {
		t.Fatalf("updated get=%q err=%v", got, err)
	}
	if err := platformCredentialDelete(id); err != nil {
		t.Fatal(err)
	}
	if _, err := platformCredentialGet(id); !errors.Is(err, ErrCredentialNotFound) {
		t.Fatalf("get after delete: %v", err)
	}
}

func TestDarwinLegacyMigrationStoresMultipleCredentials(t *testing.T) {
	if os.Getenv("PAW_TEST_MACOS_KEYCHAIN") != "1" {
		t.Skip("set PAW_TEST_MACOS_KEYCHAIN=1 to exercise the login keychain")
	}
	clearDetectionEnv(t)
	suffix := time.Now().UTC().Format("20060102t150405000000000")
	providerA := "codex-darwin-a-" + suffix
	providerB := "codex-darwin-b-" + suffix
	credentialA := "provider/" + providerA
	credentialB := "provider/" + providerB
	store := OSCredentialStore{}
	t.Cleanup(func() {
		_ = store.Delete(context.Background(), credentialA)
		_ = store.Delete(context.Background(), credentialB)
	})

	paths := isolatedPaths(t, false)
	if err := os.MkdirAll(paths.LegacyHome, 0o700); err != nil {
		t.Fatal(err)
	}
	legacy := fmt.Sprintf(`{
  "schemaVersion": 1,
  "activeModelProfileId": %q,
  "modelProfiles": [
    {"id":%q,"provider":"custom","transport":"openai-compatible","baseUrl":"https://one.invalid/v1","apiKey":"migration-secret-a","model":"model-a"},
    {"id":%q,"provider":"custom","transport":"openai-compatible","baseUrl":"https://two.invalid/v1","apiKey":"migration-secret-b","model":"model-b"}
  ]
}`, providerA, providerA, providerB)
	if err := os.WriteFile(paths.LegacyConfig, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}

	manager := openTestManager(t, paths, store, false)
	if !manager.Snapshot().Ready {
		t.Fatalf("migrated snapshot not ready: %#v", manager.Snapshot().Diagnostics)
	}
	for id, want := range map[string]string{
		credentialA: "migration-secret-a",
		credentialB: "migration-secret-b",
	} {
		got, err := store.Get(context.Background(), id)
		if err != nil || got != want {
			t.Fatalf("credential %q was not migrated: present=%v err=%v", id, got != "", err)
		}
	}
	raw, err := os.ReadFile(paths.GlobalConfig)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "migration-secret-") {
		t.Fatal("migrated v2 configuration contains a plaintext credential")
	}
	for _, path := range []string{paths.MigrationMarker, paths.GlobalConfig, filepath.Join(paths.Home, "config-v1.backup.json")} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("migration output %q: %v", path, err)
		}
	}
}
