package config

import (
	"context"
	"os"
	"strings"
	"sync"
)

type CredentialStore interface {
	Get(context.Context, string) (string, error)
	Set(context.Context, string, string) error
	Delete(context.Context, string) error
}

// OSCredentialStore stores secrets in the platform credential manager. On
// platforms without a supported secure store its methods return
// ErrCredentialStoreUnavailable; env resolution remains available.
type OSCredentialStore struct{}

func (OSCredentialStore) Get(ctx context.Context, id string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return platformCredentialGet(strings.TrimSpace(id))
}
func (OSCredentialStore) Set(ctx context.Context, id, secret string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return platformCredentialSet(strings.TrimSpace(id), secret)
}
func (OSCredentialStore) Delete(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return platformCredentialDelete(strings.TrimSpace(id))
}

type CredentialSource struct {
	Kind string
	Name string
}

// resolveCredential resolves a provider secret from environment variables
// only. The platform credential store (macOS keychain / Windows Credential
// Manager) is intentionally never consulted: locally-built binaries fail
// keychain ACL checks — the binary's cdhash changes with every rebuild — which
// makes macOS prompt for the keychain password on every startup. Secrets
// therefore come exclusively from auth.env variables; the keyring remains
// available for explicit Set/Delete through /config but is never read
// automatically.
func resolveCredential(_ context.Context, _ CredentialStore, auth Auth) (string, CredentialSource, error) {
	for _, name := range auth.Env {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			return value, CredentialSource{Kind: "env", Name: name}, nil
		}
	}
	return "", CredentialSource{}, ErrCredentialNotFound
}

// FakeCredentialStore is exported for integration tests and embedders.
type FakeCredentialStore struct {
	mu          sync.RWMutex
	Values      map[string]string
	Unavailable bool
}

func (s *FakeCredentialStore) Get(_ context.Context, id string) (string, error) {
	if s.Unavailable {
		return "", ErrCredentialStoreUnavailable
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	value, ok := s.Values[id]
	if !ok {
		return "", ErrCredentialNotFound
	}
	return value, nil
}
func (s *FakeCredentialStore) Set(_ context.Context, id, secret string) error {
	if s.Unavailable {
		return ErrCredentialStoreUnavailable
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Values == nil {
		s.Values = map[string]string{}
	}
	s.Values[id] = secret
	return nil
}
func (s *FakeCredentialStore) Delete(_ context.Context, id string) error {
	if s.Unavailable {
		return ErrCredentialStoreUnavailable
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.Values[id]; !ok {
		return ErrCredentialNotFound
	}
	delete(s.Values, id)
	return nil
}
