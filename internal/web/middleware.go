package web

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
)

const defaultRequestBodyLimit int64 = 2 * 1024 * 1024

type MiddlewareConfig struct {
	Auth          *AuthStore
	AllowedHosts  []string
	AllowedOrigin string
	BodyLimit     int64
	PublicPaths   map[string]struct{}
}

type requestIDKey struct{}

func RequestID(ctx context.Context) string {
	value, _ := ctx.Value(requestIDKey{}).(string)
	return value
}

func Middleware(cfg MiddlewareConfig, next http.Handler) http.Handler {
	if next == nil {
		next = http.NotFoundHandler()
	}
	bodyLimit := cfg.BodyLimit
	if bodyLimit <= 0 {
		bodyLimit = defaultRequestBodyLimit
	}
	allowedHosts := normalizeHosts(cfg.AllowedHosts)
	publicPaths := cfg.PublicPaths
	if publicPaths == nil {
		publicPaths = map[string]struct{}{"/api/auth/exchange": {}}
	}
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requestID := newRequestID()
		writer.Header().Set("X-Request-ID", requestID)
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		writer.Header().Set("X-Frame-Options", "DENY")
		writer.Header().Set("Referrer-Policy", "no-referrer")
		writer.Header().Set("Content-Security-Policy", "default-src 'self'; frame-ancestors 'none'; base-uri 'none'")
		request = request.WithContext(context.WithValue(request.Context(), requestIDKey{}, requestID))

		if !hostAllowed(request.Host, allowedHosts) {
			writeJSONError(writer, http.StatusMisdirectedRequest, "invalid_host", "request host is not allowed", requestID)
			return
		}
		_, public := publicPaths[request.URL.Path]
		public = public || (!strings.HasPrefix(request.URL.Path, "/api/") && (request.Method == http.MethodGet || request.Method == http.MethodHead))
		if !public && (cfg.Auth == nil || cfg.Auth.Authenticate(request) != nil) {
			writeJSONError(writer, http.StatusUnauthorized, "unauthorized", "authentication required", requestID)
			return
		}
		if isWriteMethod(request.Method) && !safeWriteRequest(request, cfg.AllowedOrigin) {
			writeJSONError(writer, http.StatusForbidden, "cross_origin_forbidden", "cross-origin write request denied", requestID)
			return
		}
		if request.Body != nil && request.URL.Path != "/api/events" {
			request.Body = http.MaxBytesReader(writer, request.Body, bodyLimit)
		}
		next.ServeHTTP(writer, request)
	})
}

func DecodeJSON(writer http.ResponseWriter, request *http.Request, target any) error {
	if request == nil || request.Body == nil {
		return io.EOF
	}
	contentType, _, _ := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if contentType != "" && contentType != "application/json" {
		return fmt.Errorf("content type must be application/json")
	}
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("request body must contain one JSON value")
		}
		return err
	}
	return nil
}

func normalizeHosts(hosts []string) map[string]struct{} {
	normalized := make(map[string]struct{})
	for _, host := range hosts {
		host = strings.ToLower(strings.TrimSpace(host))
		if host != "" {
			normalized[host] = struct{}{}
		}
	}
	return normalized
}

func hostAllowed(hostport string, allowed map[string]struct{}) bool {
	hostport = strings.ToLower(strings.TrimSpace(hostport))
	if len(allowed) > 0 {
		_, ok := allowed[hostport]
		return ok
	}
	host := hostport
	if parsedHost, _, err := net.SplitHostPort(hostport); err == nil {
		host = strings.Trim(parsedHost, "[]")
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func isWriteMethod(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

func safeWriteRequest(request *http.Request, allowedOrigin string) bool {
	if request == nil {
		return false
	}
	fetchSite := strings.ToLower(strings.TrimSpace(request.Header.Get("Sec-Fetch-Site")))
	if fetchSite != "" && fetchSite != "same-origin" && fetchSite != "none" {
		return false
	}
	origin := strings.TrimSpace(request.Header.Get("Origin"))
	if origin == "" {
		return fetchSite == "" || fetchSite == "same-origin" || fetchSite == "none"
	}
	if allowedOrigin != "" {
		return origin == allowedOrigin
	}
	parsed, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return strings.EqualFold(parsed.Host, request.Host)
}

func newRequestID() string {
	buffer := make([]byte, 12)
	if _, err := rand.Read(buffer); err != nil {
		return "request"
	}
	return "req_" + hex.EncodeToString(buffer)
}
