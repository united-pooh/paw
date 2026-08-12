package config

import (
	"context"
	"errors"
	"os"
	"testing"
)

func unsetenv(t *testing.T, name string) {
	t.Helper()
	old, had := os.LookupEnv(name)
	if err := os.Unsetenv(name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if had {
			_ = os.Setenv(name, old)
		} else {
			_ = os.Unsetenv(name)
		}
	})
}

// failingStore returns a non-miss error so tests can verify that keyring
// errors are ignored entirely (resolveCredential is env-only).
type failingStore struct{ err error }

func (s failingStore) Get(context.Context, string) (string, error) { return "", s.err }
func (s failingStore) Set(context.Context, string, string) error   { return s.err }
func (s failingStore) Delete(context.Context, string) error        { return s.err }

func TestCredentialResolutionPrefersEnvironmentOverKeyring(t *testing.T) {
	t.Setenv("PAW_TEST_PRIMARY", "env-primary")
	auth := Auth{Credential: "provider/test", Env: []string{"PAW_TEST_PRIMARY"}}
	store := &FakeCredentialStore{Values: map[string]string{"provider/test": "keyring-secret"}}
	value, source, err := resolveCredential(context.Background(), store, auth)
	if err != nil {
		t.Fatal(err)
	}
	if value != "env-primary" || source.Kind != "env" || source.Name != "PAW_TEST_PRIMARY" {
		t.Fatalf("value/source=%q/%#v", value, source)
	}
}

func TestCredentialResolutionNeverConsultsKeyring(t *testing.T) {
	unsetenv(t, "PAW_TEST_PRIMARY")
	auth := Auth{Credential: "provider/test", Env: []string{"PAW_TEST_PRIMARY"}}
	store := &FakeCredentialStore{Values: map[string]string{"provider/test": "keyring-secret"}}
	_, _, err := resolveCredential(context.Background(), store, auth)
	if !errors.Is(err, ErrCredentialNotFound) {
		t.Fatalf("err=%v, want ErrCredentialNotFound: keyring must never be read", err)
	}
}

func TestCredentialResolutionSkipsEmptyEnvThenUsesLaterEnv(t *testing.T) {
	t.Setenv("PAW_TEST_SECONDARY", "env-secondary")
	unsetenv(t, "PAW_TEST_PRIMARY")
	auth := Auth{Credential: "provider/test", Env: []string{"PAW_TEST_PRIMARY", "PAW_TEST_SECONDARY"}}
	store := &FakeCredentialStore{Values: map[string]string{"provider/test": "keyring-secret"}}
	value, source, err := resolveCredential(context.Background(), store, auth)
	if err != nil {
		t.Fatal(err)
	}
	if value != "env-secondary" || source.Name != "PAW_TEST_SECONDARY" {
		t.Fatalf("value/source=%q/%#v", value, source)
	}
}

func TestCredentialResolutionUnavailableStoreFallsBackToEnv(t *testing.T) {
	t.Setenv("PAW_TEST_PRIMARY", "env-primary")
	auth := Auth{Credential: "provider/test", Env: []string{"PAW_TEST_PRIMARY"}}
	store := &FakeCredentialStore{Unavailable: true}
	value, source, err := resolveCredential(context.Background(), store, auth)
	if err != nil {
		t.Fatal(err)
	}
	if value != "env-primary" || source.Kind != "env" {
		t.Fatalf("value/source=%q/%#v", value, source)
	}
}

func TestCredentialResolutionNilStore(t *testing.T) {
	t.Setenv("PAW_TEST_PRIMARY", "env-primary")
	auth := Auth{Credential: "provider/test", Env: []string{"PAW_TEST_PRIMARY"}}
	value, source, err := resolveCredential(context.Background(), nil, auth)
	if err != nil {
		t.Fatal(err)
	}
	if value != "env-primary" || source.Kind != "env" {
		t.Fatalf("value/source=%q/%#v", value, source)
	}

	unsetenv(t, "PAW_TEST_PRIMARY")
	_, _, err = resolveCredential(context.Background(), nil, auth)
	if !errors.Is(err, ErrCredentialNotFound) {
		t.Fatalf("err=%v, want ErrCredentialNotFound", err)
	}
}

func TestCredentialResolutionNotFound(t *testing.T) {
	unsetenv(t, "PAW_TEST_PRIMARY")
	auth := Auth{Credential: "provider/test", Env: []string{"PAW_TEST_PRIMARY"}}
	store := &FakeCredentialStore{Values: map[string]string{}}
	_, _, err := resolveCredential(context.Background(), store, auth)
	if !errors.Is(err, ErrCredentialNotFound) {
		t.Fatalf("err=%v, want ErrCredentialNotFound", err)
	}
}

func TestCredentialResolutionIgnoresStoreFailures(t *testing.T) {
	unsetenv(t, "PAW_TEST_PRIMARY")
	auth := Auth{Credential: "provider/test", Env: []string{"PAW_TEST_PRIMARY"}}
	store := failingStore{err: errors.New("keychain exploded")}
	_, _, err := resolveCredential(context.Background(), store, auth)
	if !errors.Is(err, ErrCredentialNotFound) {
		t.Fatalf("err=%v, want ErrCredentialNotFound: store errors must be ignored", err)
	}
}
