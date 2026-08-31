package web

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"paw/internal/app"
)

type ServerConfig struct {
	Listen      string
	Supervisor  *app.Supervisor
	Auth        *AuthStore
	OpenRuntime func(context.Context, app.WorkspaceRuntimeOptions) (*app.WorkspaceRuntime, error)
}

type Server struct {
	listen      string
	supervisor  *app.Supervisor
	auth        *AuthStore
	openRuntime func(context.Context, app.WorkspaceRuntimeOptions) (*app.WorkspaceRuntime, error)

	mu       sync.Mutex
	listener net.Listener
	http     *http.Server
	serveErr chan error
}

func NewServer(cfg ServerConfig) (*Server, error) {
	if strings.TrimSpace(cfg.Listen) == "" {
		cfg.Listen = "127.0.0.1:0"
	}
	if cfg.Supervisor == nil {
		return nil, fmt.Errorf("supervisor is required")
	}
	if cfg.Auth == nil {
		return nil, fmt.Errorf("auth store is required")
	}
	openRuntime := cfg.OpenRuntime
	if openRuntime == nil {
		openRuntime = cfg.Supervisor.Open
	}
	return &Server{listen: cfg.Listen, supervisor: cfg.Supervisor, auth: cfg.Auth, openRuntime: openRuntime}, nil
}

func (s *Server) Start() error {
	if s == nil {
		return fmt.Errorf("server is nil")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listener != nil {
		return fmt.Errorf("server already started")
	}
	listener, err := net.Listen("tcp", s.listen)
	if err != nil {
		return err
	}
	mux := http.NewServeMux()
	s.registerRoutes(mux)
	address := listener.Addr().String()
	port := listener.Addr().(*net.TCPAddr).Port
	handler := Middleware(MiddlewareConfig{
		Auth: s.auth, AllowedHosts: []string{address, fmt.Sprintf("localhost:%d", port)},
		AllowedOrigin: "http://" + address,
	}, mux)
	s.listener = listener
	s.serveErr = make(chan error, 1)
	s.http = &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		err := s.http.Serve(listener)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		s.serveErr <- err
	}()
	return nil
}

func (s *Server) URL() string {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listener == nil {
		return ""
	}
	return "http://" + s.listener.Addr().String()
}

func (s *Server) Wait() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	serveErr := s.serveErr
	s.mu.Unlock()
	if serveErr == nil {
		return nil
	}
	return <-serveErr
}

func (s *Server) Shutdown(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	server := s.http
	s.mu.Unlock()
	var errs []error
	if server != nil {
		errs = append(errs, server.Shutdown(ctx))
	}
	if s.supervisor != nil {
		errs = append(errs, s.supervisor.Shutdown(ctx))
	}
	return errors.Join(errs...)
}

func (s *Server) registerRoutes(mux *http.ServeMux) {
	mux.Handle("/api/auth/exchange", s.auth.ExchangeHandler())
	mux.HandleFunc("GET /api/bootstrap", s.handleBootstrap)
	mux.HandleFunc("GET /api/recent-workspaces", s.handleRecentWorkspaces)
	mux.HandleFunc("DELETE /api/recent-workspaces/{workspace_id}", s.handleForgetRecent)
	mux.HandleFunc("POST /api/workspaces/open", s.handleOpenWorkspace)
	mux.HandleFunc("POST /api/workspaces/{workspace_id}/close", s.handleCloseWorkspace)
	mux.HandleFunc("GET /api/workspaces/{workspace_id}/sessions", s.handleSessions)
	mux.HandleFunc("POST /api/workspaces/{workspace_id}/sessions", s.handleCreateSession)
	mux.HandleFunc("GET /api/workspaces/{workspace_id}/sessions/{session_id}", s.handleSessionSnapshot)
	mux.HandleFunc("POST /api/workspaces/{workspace_id}/sessions/{session_id}/fork", s.handleForkSession)
	mux.HandleFunc("GET /api/workspaces/{workspace_id}/sessions/{session_id}/export", s.handleExportSession)
}
