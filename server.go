package replicate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/zap-proto/fiber/v3"
	zip "github.com/zap-proto/zip"
)

// SocketConfig configures the Unix socket for control commands.
type SocketConfig struct {
	Enabled     bool   `yaml:"enabled"`
	Path        string `yaml:"path"`
	Permissions uint32 `yaml:"permissions"`
}

// DefaultSocketConfig returns the default socket configuration.
func DefaultSocketConfig() SocketConfig {
	return SocketConfig{
		Enabled:     false,
		Path:        "/var/run/replicate.sock",
		Permissions: 0600,
	}
}

// Server manages runtime control via Unix socket using HTTP.
type Server struct {
	store *Store

	// SocketPath is the path to the Unix socket.
	SocketPath string

	// SocketPerms is the file permissions for the socket.
	SocketPerms uint32

	// PathExpander optionally expands paths (e.g., ~ expansion).
	// If nil, paths are used as-is.
	PathExpander func(string) (string, error)

	// Version is the version string to report in /info.
	Version string

	// startedAt is set when the server starts.
	startedAt time.Time

	socketListener net.Listener
	app            *zip.App

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	logger *slog.Logger
}

// NewServer creates a new Server instance.
func NewServer(store *Store) *Server {
	ctx, cancel := context.WithCancel(context.Background())
	s := &Server{
		store:       store,
		SocketPerms: 0600,
		ctx:         ctx,
		cancel:      cancel,
		logger:      slog.Default().With(LogKeySystem, LogSystemServer),
		app:         NewApp("replicate-control"),
	}

	s.app.Post("/start", s.handleStart)
	s.app.Post("/stop", s.handleStop)
	s.app.Get("/txid", s.handleTXID)
	s.app.Post("/register", s.handleRegister)
	s.app.Post("/unregister", s.handleUnregister)
	s.app.Post("/sync", s.handleSync)
	s.app.Get("/list", s.handleList)
	s.app.Get("/info", s.handleInfo)

	// pprof endpoints (GET only, as the control socket has always exposed them).
	RegisterPprof(s.app.Get)

	return s
}

// Start begins listening for control connections.
func (s *Server) Start() error {
	if s.SocketPath == "" {
		return fmt.Errorf("socket path required")
	}

	// Check if socket file exists and is actually a socket before removing
	if info, err := os.Lstat(s.SocketPath); err == nil {
		if info.Mode()&os.ModeSocket != 0 {
			if err := os.Remove(s.SocketPath); err != nil {
				return fmt.Errorf("remove existing socket: %w", err)
			}
		} else {
			return fmt.Errorf("socket path exists but is not a socket: %s", s.SocketPath)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("check socket path: %w", err)
	}

	listener, err := net.Listen("unix", s.SocketPath)
	if err != nil {
		return fmt.Errorf("listen on unix socket: %w", err)
	}
	s.socketListener = listener

	if err := os.Chmod(s.SocketPath, os.FileMode(s.SocketPerms)); err != nil {
		listener.Close()
		return fmt.Errorf("chmod socket: %w", err)
	}

	// Set startedAt after successful socket setup to ensure uptime reflects
	// the actual time the server became available.
	s.startedAt = time.Now()

	s.logger.Info("control socket listening", "path", s.SocketPath)

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		// The socket is bound and chmod'ed above so Start returns only once the
		// control socket is reachable; app.Listen would bind it itself and race
		// callers. Serving a pre-bound net.Listener is the one thing zip's
		// transport registry does not cover, so we hand it to the underlying
		// engine directly — every route, middleware and error still runs on zip.
		if err := s.app.Fiber().Listener(listener, fiber.ListenConfig{DisableStartupMessage: true}); err != nil {
			s.logger.Error("http server error", "error", err)
		}
	}()

	return nil
}

// Close gracefully shuts down the control server.
func (s *Server) Close() error {
	s.cancel()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if s.socketListener != nil {
		if err := s.app.ShutdownWithContext(ctx); err != nil {
			s.logger.Error("http server shutdown error", "error", err)
		}
	}
	s.wg.Wait()
	return nil
}

// expandPath expands the path using PathExpander if set.
func (s *Server) expandPath(path string) (string, error) {
	if s.PathExpander != nil {
		return s.PathExpander(path)
	}
	return path, nil
}

func (s *Server) handleStart(c *zip.Ctx) error {
	var req StartRequest
	if err := decodeBody(c, &req); err != nil {
		return writeJSONError(c, http.StatusBadRequest, "invalid request body", err.Error())
	}

	if req.Path == "" {
		return writeJSONError(c, http.StatusBadRequest, "path required", nil)
	}

	expandedPath, err := s.expandPath(req.Path)
	if err != nil {
		return writeJSONError(c, http.StatusBadRequest, fmt.Sprintf("invalid path: %v", err), nil)
	}

	ctx := s.ctx
	if req.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(s.ctx, time.Duration(req.Timeout)*time.Second)
		defer cancel()
	}

	if err := s.store.EnableDB(ctx, expandedPath); err != nil {
		return writeJSONError(c, http.StatusInternalServerError, err.Error(), nil)
	}

	return writeJSON(c, http.StatusOK, StartResponse{
		Status: "started",
		Path:   expandedPath,
	})
}

func (s *Server) handleStop(c *zip.Ctx) error {
	var req StopRequest
	if err := decodeBody(c, &req); err != nil {
		return writeJSONError(c, http.StatusBadRequest, "invalid request body", err.Error())
	}

	if req.Path == "" {
		return writeJSONError(c, http.StatusBadRequest, "path required", nil)
	}

	expandedPath, err := s.expandPath(req.Path)
	if err != nil {
		return writeJSONError(c, http.StatusBadRequest, fmt.Sprintf("invalid path: %v", err), nil)
	}

	timeout := req.Timeout
	if timeout == 0 {
		timeout = 30
	}
	ctx, cancel := context.WithTimeout(s.ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	if err := s.store.DisableDB(ctx, expandedPath); err != nil {
		return writeJSONError(c, http.StatusInternalServerError, err.Error(), nil)
	}

	return writeJSON(c, http.StatusOK, StopResponse{
		Status: "stopped",
		Path:   expandedPath,
	})
}

func (s *Server) handleTXID(c *zip.Ctx) error {
	path := c.Query("path")
	if path == "" {
		return writeJSONError(c, http.StatusBadRequest, "path required", nil)
	}

	expandedPath, err := s.expandPath(path)
	if err != nil {
		return writeJSONError(c, http.StatusBadRequest, fmt.Sprintf("invalid path: %v", err), nil)
	}

	db := s.store.FindDB(expandedPath)
	if db == nil {
		return writeJSONError(c, http.StatusNotFound, "database not found", nil)
	}

	pos, err := db.Pos()
	if err != nil {
		return writeJSONError(c, http.StatusInternalServerError, err.Error(), nil)
	}

	return writeJSON(c, http.StatusOK, TXIDResponse{
		TXID: uint64(pos.TXID),
	})
}

// writeJSON is the single response seam: encoding/json with the trailing
// newline json.Encoder writes, so the bytes on the wire are byte-identical to
// what the net/http handlers produced.
func writeJSON(c *zip.Ctx, status int, v interface{}) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	c.SetHeader("Content-Type", "application/json")
	return c.Bytes(status, append(b, '\n'))
}

func writeJSONError(c *zip.Ctx, status int, message string, details interface{}) error {
	return writeJSON(c, status, ErrorResponse{
		Error:   message,
		Details: details,
	})
}

// decodeBody decodes the request body the way json.NewDecoder(r.Body) did:
// an empty body yields io.EOF, so a bodyless POST is still a 400.
func decodeBody(c *zip.Ctx, v interface{}) error {
	return json.NewDecoder(bytes.NewReader(c.Body())).Decode(v)
}

// StartRequest is the request body for the /start endpoint.
type StartRequest struct {
	Path    string `json:"path"`
	Timeout int    `json:"timeout,omitempty"`
}

// StartResponse is the response body for the /start endpoint.
type StartResponse struct {
	Status string `json:"status"`
	Path   string `json:"path"`
}

// StopRequest is the request body for the /stop endpoint.
type StopRequest struct {
	Path    string `json:"path"`
	Timeout int    `json:"timeout,omitempty"`
}

// StopResponse is the response body for the /stop endpoint.
type StopResponse struct {
	Status string `json:"status"`
	Path   string `json:"path"`
}

// ErrorResponse is returned when an error occurs.
type ErrorResponse struct {
	Error   string      `json:"error"`
	Details interface{} `json:"details,omitempty"`
}

// TXIDResponse is the response body for the /txid endpoint.
type TXIDResponse struct {
	TXID uint64 `json:"txid"`
}

func (s *Server) handleSync(c *zip.Ctx) error {
	var req SyncRequest
	if err := decodeBody(c, &req); err != nil {
		return writeJSONError(c, http.StatusBadRequest, "invalid request body", err.Error())
	}

	if req.Path == "" {
		return writeJSONError(c, http.StatusBadRequest, "path required", nil)
	}

	expandedPath, err := s.expandPath(req.Path)
	if err != nil {
		return writeJSONError(c, http.StatusBadRequest, fmt.Sprintf("invalid path: %v", err), nil)
	}

	ctx := s.ctx
	if req.Wait && req.Timeout == 0 {
		req.Timeout = 30
	}
	if req.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(s.ctx, time.Duration(req.Timeout)*time.Second)
		defer cancel()
	}

	result, err := s.store.SyncDB(ctx, expandedPath, req.Wait)
	if err != nil {
		switch {
		case errors.Is(err, ErrDatabaseNotFound):
			return writeJSONError(c, http.StatusNotFound, err.Error(), nil)
		case errors.Is(err, ErrDatabaseNotOpen):
			return writeJSONError(c, http.StatusConflict, err.Error(), nil)
		default:
			return writeJSONError(c, http.StatusInternalServerError, err.Error(), nil)
		}
	}

	var status string
	if !result.Changed {
		status = "no_change"
	} else if req.Wait {
		status = "synced"
	} else {
		status = "synced_local"
	}

	return writeJSON(c, http.StatusOK, SyncResponse{
		Status:         status,
		Path:           expandedPath,
		TXID:           result.TXID,
		ReplicatedTXID: result.ReplicatedTXID,
	})
}

// SyncRequest is the request body for the /sync endpoint.
type SyncRequest struct {
	Path    string `json:"path"`
	Wait    bool   `json:"wait,omitempty"`
	Timeout int    `json:"timeout,omitempty"`
}

// SyncResponse is the response body for the /sync endpoint.
type SyncResponse struct {
	Status         string `json:"status"`
	Path           string `json:"path"`
	TXID           uint64 `json:"txid"`
	ReplicatedTXID uint64 `json:"replicated_txid"`
}

func (s *Server) handleList(c *zip.Ctx) error {
	dbs := s.store.DBs()
	resp := ListResponse{
		Databases: make([]DatabaseSummary, 0, len(dbs)),
	}

	for _, db := range dbs {
		var status string
		if db.IsOpen() {
			if db.Replica != nil && db.Replica.MonitorEnabled {
				status = "replicating"
			} else {
				status = "open"
			}
		} else {
			status = "stopped"
		}

		summary := DatabaseSummary{
			Path:   db.Path(),
			Status: status,
		}

		if t := db.LastSuccessfulSyncAt(); !t.IsZero() {
			summary.LastSyncAt = &t
		}

		resp.Databases = append(resp.Databases, summary)
	}

	return writeJSON(c, http.StatusOK, resp)
}

func (s *Server) handleInfo(c *zip.Ctx) error {
	resp := InfoResponse{
		Version:       s.Version,
		PID:           os.Getpid(),
		StartedAt:     s.startedAt,
		UptimeSeconds: int64(time.Since(s.startedAt).Seconds()),
		DatabaseCount: len(s.store.DBs()),
	}

	return writeJSON(c, http.StatusOK, resp)
}

// ListResponse is the response body for the /list endpoint.
type ListResponse struct {
	Databases []DatabaseSummary `json:"databases"`
}

// DatabaseSummary contains summary information about a database.
type DatabaseSummary struct {
	Path   string `json:"path"`
	Status string `json:"status"`

	// LastSyncAt is the timestamp of the last successful replica sync.
	// This reflects when data was last successfully uploaded to the replica
	// storage backend, not just when the local WAL was processed.
	LastSyncAt *time.Time `json:"last_sync_at,omitempty"`
}

// InfoResponse is the response body for the /info endpoint.
type InfoResponse struct {
	Version       string    `json:"version"`
	PID           int       `json:"pid"`
	UptimeSeconds int64     `json:"uptime_seconds"`
	StartedAt     time.Time `json:"started_at"`
	DatabaseCount int       `json:"database_count"`
}

// RegisterDatabaseRequest is the request body for the /register endpoint.
type RegisterDatabaseRequest struct {
	Path       string `json:"path"`
	ReplicaURL string `json:"replica_url"`
}

// RegisterDatabaseResponse is the response body for the /register endpoint.
type RegisterDatabaseResponse struct {
	Status string `json:"status"`
	Path   string `json:"path"`
}

// UnregisterDatabaseRequest is the request body for the /unregister endpoint.
type UnregisterDatabaseRequest struct {
	Path    string `json:"path"`
	Timeout int    `json:"timeout,omitempty"`
}

// UnregisterDatabaseResponse is the response body for the /unregister endpoint.
type UnregisterDatabaseResponse struct {
	Status string `json:"status"`
	Path   string `json:"path"`
}

func (s *Server) handleRegister(c *zip.Ctx) error {
	var req RegisterDatabaseRequest
	if err := decodeBody(c, &req); err != nil {
		return writeJSONError(c, http.StatusBadRequest, "invalid request body", err.Error())
	}

	if req.Path == "" {
		return writeJSONError(c, http.StatusBadRequest, "path required", nil)
	}

	if req.ReplicaURL == "" {
		return writeJSONError(c, http.StatusBadRequest, "replica_url required", nil)
	}

	expandedPath, err := s.expandPath(req.Path)
	if err != nil {
		return writeJSONError(c, http.StatusBadRequest, fmt.Sprintf("invalid path: %v", err), nil)
	}

	// Check if database already exists.
	if existing := s.store.FindDB(expandedPath); existing != nil {
		return writeJSON(c, http.StatusOK, RegisterDatabaseResponse{
			Status: "already_exists",
			Path:   expandedPath,
		})
	}

	// Create replica client from URL.
	client, err := NewReplicaClientFromURL(req.ReplicaURL)
	if err != nil {
		return writeJSONError(c, http.StatusBadRequest, fmt.Sprintf("invalid replica url: %v", err), nil)
	}

	// Create new database.
	db := NewDB(expandedPath)

	// Create replica and attach client.
	replica := NewReplica(db)
	replica.Client = client
	db.Replica = replica

	// Register database with store (this also opens the database).
	if err := s.store.RegisterDB(db); err != nil {
		return writeJSONError(c, http.StatusInternalServerError, fmt.Sprintf("failed to register database: %v", err), nil)
	}

	return writeJSON(c, http.StatusOK, RegisterDatabaseResponse{
		Status: "registered",
		Path:   expandedPath,
	})
}

func (s *Server) handleUnregister(c *zip.Ctx) error {
	var req UnregisterDatabaseRequest
	if err := decodeBody(c, &req); err != nil {
		return writeJSONError(c, http.StatusBadRequest, "invalid request body", err.Error())
	}

	if req.Path == "" {
		return writeJSONError(c, http.StatusBadRequest, "path required", nil)
	}

	expandedPath, err := s.expandPath(req.Path)
	if err != nil {
		return writeJSONError(c, http.StatusBadRequest, fmt.Sprintf("invalid path: %v", err), nil)
	}

	// Set up timeout context. Treat non-positive values as default.
	timeout := req.Timeout
	if timeout <= 0 {
		timeout = 30
	}
	ctx, cancel := context.WithTimeout(s.ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	// Remove database from store (this also closes it).
	if err := s.store.UnregisterDB(ctx, expandedPath); err != nil {
		return writeJSONError(c, http.StatusInternalServerError, fmt.Sprintf("failed to unregister database: %v", err), nil)
	}

	return writeJSON(c, http.StatusOK, UnregisterDatabaseResponse{
		Status: "unregistered",
		Path:   expandedPath,
	})
}
