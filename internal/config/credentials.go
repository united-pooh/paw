package config

import (
	"context"
	"fmt"
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

func resolveCredential(ctx context.Context, store CredentialStore, auth Auth) (string, CredentialSource, error) {
	if id := strings.TrimSpace(auth.Credential); id != "" && store != nil {
		value, err := store.Get(ctx, id)
		if err == nil && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value), CredentialSource{Kind: "keyring", Name: id}, nil
		}
		if err != nil && !errorsIsCredentialMiss(err) {
			return "", CredentialSource{}, fmt.Errorf("read credential %q: %w", id, err)
		}
	}
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

func errorsIsCredentialMiss(err error) bool {
	return err == ErrCredentialNotFound || err == ErrCredentialStoreUnavailable
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
