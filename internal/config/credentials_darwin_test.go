//go:build darwin && cgo

package config

import (
	"errors"
	"os"
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
