package config

import (
	"context"
	"testing"
)

func TestCredentialResolutionPrefersKeyringThenOrderedEnvironment(t *testing.T) {
	t.Setenv("PAW_TEST_PRIMARY", "env-primary")
	t.Setenv("PAW_TEST_SECONDARY", "env-secondary")
	auth := Auth{Credential: "provider/test", Env: []string{"PAW_TEST_PRIMARY", "PAW_TEST_SECONDARY"}}
	store := &FakeCredentialStore{Values: map[string]string{"provider/test": "keyring-secret"}}
	value, source, err := resolveCredential(context.Background(), store, auth)
	if err != nil {
		t.Fatal(err)
	}
	if value != "keyring-secret" || source.Kind != "keyring" {
		t.Fatalf("value/source=%q/%#v", value, source)
	}
	delete(store.Values, "provider/test")
	value, source, err = resolveCredential(context.Background(), store, auth)
	if err != nil {
		t.Fatal(err)
	}
	if value != "env-primary" || source.Name != "PAW_TEST_PRIMARY" {
		t.Fatalf("fallback value/source=%q/%#v", value, source)
	}
	store.Unavailable = true
	value, source, err = resolveCredential(context.Background(), store, auth)
	if err != nil {
		t.Fatal(err)
	}
	if value != "env-primary" || source.Kind != "env" {
		t.Fatalf("unavailable fallback=%q/%#v", value, source)
	}
}
