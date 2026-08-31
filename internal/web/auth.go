package web

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"
)

const SessionCookieName = "paw_session"

var (
	ErrInvalidBootstrapToken = errors.New("invalid_bootstrap_token")
	ErrInvalidSession        = errors.New("invalid_session")
)

type AuthStore struct {
	mu sync.Mutex

	bootstrap map[[32]byte]struct{}
	sessions  map[[32]byte]time.Time
	now       func() time.Time
	secure    bool
}

func NewAuthStore(secureCookie bool) *AuthStore {
	return &AuthStore{
		bootstrap: make(map[[32]byte]struct{}), sessions: make(map[[32]byte]time.Time),
		now: time.Now, secure: secureCookie,
	}
}

func (s *AuthStore) NewBootstrapToken() (string, error) {
	if s == nil {
		return "", errors.New("auth store is nil")
	}
	token, err := randomToken()
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256([]byte(token))
	s.mu.Lock()
	s.bootstrap[hash] = struct{}{}
	s.mu.Unlock()
	return token, nil
}

func (s *AuthStore) ExchangeBootstrapToken(token string) (*http.Cookie, error) {
	if s == nil {
		return nil, ErrInvalidBootstrapToken
	}
	provided := sha256.Sum256([]byte(token))
	s.mu.Lock()
	var matched [32]byte
	found := false
	for stored := range s.bootstrap {
		if subtle.ConstantTimeCompare(stored[:], provided[:]) == 1 {
			matched = stored
			found = true
		}
	}
	if !found {
		s.mu.Unlock()
		return nil, ErrInvalidBootstrapToken
	}
	delete(s.bootstrap, matched)
	sessionToken, err := randomToken()
	if err != nil {
		s.mu.Unlock()
		return nil, err
	}
	sessionHash := sha256.Sum256([]byte(sessionToken))
	s.sessions[sessionHash] = s.now().UTC()
	s.mu.Unlock()
	return &http.Cookie{
		Name: SessionCookieName, Value: sessionToken, Path: "/",
		HttpOnly: true, Secure: s.secure, SameSite: http.SameSiteStrictMode,
	}, nil
}

func (s *AuthStore) ExchangeHandler() http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost {
			writeJSONError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "POST required", RequestID(request.Context()))
			return
		}
		var input struct {
			Token string `json:"token"`
		}
		if err := DecodeJSON(writer, request, &input); err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				writeJSONError(writer, http.StatusRequestEntityTooLarge, "body_too_large", "request body is too large", RequestID(request.Context()))
				return
			}
			writeJSONError(writer, http.StatusBadRequest, "invalid_json", err.Error(), RequestID(request.Context()))
			return
		}
		cookie, err := s.ExchangeBootstrapToken(input.Token)
		if err != nil {
			writeJSONError(writer, http.StatusUnauthorized, "invalid_bootstrap_token", "bootstrap token is invalid or already used", RequestID(request.Context()))
			return
		}
		http.SetCookie(writer, cookie)
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(struct {
			Status string `json:"status"`
		}{Status: "authenticated"})
	})
}

func (s *AuthStore) Authenticate(request *http.Request) error {
	if s == nil || request == nil {
		return ErrInvalidSession
	}
	cookie, err := request.Cookie(SessionCookieName)
	if err != nil {
		return ErrInvalidSession
	}
	provided := sha256.Sum256([]byte(cookie.Value))
	s.mu.Lock()
	defer s.mu.Unlock()
	for stored := range s.sessions {
		if subtle.ConstantTimeCompare(stored[:], provided[:]) == 1 {
			return nil
		}
	}
	return ErrInvalidSession
}

func randomToken() (string, error) {
	buffer := make([]byte, 32)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("generate auth token: %w", err)
	}
	return hex.EncodeToString(buffer), nil
}
