package mgmt

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Contemporaries/lynx/internal/config"
	"github.com/Contemporaries/lynx/internal/logx"
	"github.com/Contemporaries/lynx/internal/upgrade"
	"github.com/Contemporaries/lynx/internal/version"
)

type Role string

const (
	RoleServer Role = "server"
	RoleClient Role = "client"
)

type StatusProvider func() map[string]any

type ConfigStore interface {
	GetRedacted() any
	Put(body []byte, replace bool) (restartRequired bool, applied []string, err error)
	Path() string
}

type ServiceControl interface {
	Restart() error
	Reload() (applied []string, restartRequired bool, err error)
	Reconnect() error          // client only
	SubscribeRefresh() error   // client only
}

type Options struct {
	Listen       string
	Secret       string
	CORSOrigin   string
	AllowUpgrade bool
	ApplyRestart bool
	Role         Role
	Unit         string // systemd unit
	Binary       string // lynx-server / lynx-client
	Logger       *logx.Logger
	StartedAt    time.Time
	Status       StatusProvider
	Config       ConfigStore
	Service      ServiceControl
	Upgrade      *upgrade.Manager
}

type Server struct {
	opts Options
	http *http.Server
	mu   sync.Mutex
}

func ListenAndServe(ctx context.Context, opts Options) error {
	if strings.TrimSpace(opts.Listen) == "" {
		return nil
	}
	if opts.StartedAt.IsZero() {
		opts.StartedAt = time.Now()
	}
	if opts.Upgrade == nil && opts.Binary != "" {
		opts.Upgrade = upgrade.New(opts.Binary, opts.Unit)
	}
	s := &Server{opts: opts}
	mux := http.NewServeMux()
	s.routes(mux)
	ln, err := net.Listen("tcp", opts.Listen)
	if err != nil {
		return fmt.Errorf("mgmt listen %s: %w", opts.Listen, err)
	}
	s.http = &http.Server{Handler: s.cors(mux)}
	if opts.Logger != nil {
		opts.Logger.Info("mgmt listening", "addr", ln.Addr().String(), "role", string(opts.Role))
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = s.http.Shutdown(shutdownCtx)
	}()
	err = s.http.Serve(ln)
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

func (s *Server) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := s.opts.CORSOrigin
		if origin != "" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			w.Header().Set("Access-Control-Allow-Methods", "GET, PUT, PATCH, POST, OPTIONS")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) routes(mux *http.ServeMux) {
	mux.HandleFunc("/api/v1/health", s.handleHealth)
	mux.HandleFunc("/api/v1/version", s.auth(s.handleVersion))
	mux.HandleFunc("/api/v1/status", s.auth(s.handleStatus))
	mux.HandleFunc("/api/v1/config", s.auth(s.handleConfig))
	mux.HandleFunc("/api/v1/logs", s.auth(s.handleLogs))
	mux.HandleFunc("/api/v1/service/restart", s.auth(s.handleRestart))
	mux.HandleFunc("/api/v1/service/reload", s.auth(s.handleReload))
	mux.HandleFunc("/api/v1/upgrade", s.auth(s.handleUpgrade))
	mux.HandleFunc("/api/v1/upgrade/status", s.auth(s.handleUpgradeStatus))
	if s.opts.Role == RoleClient {
		mux.HandleFunc("/api/v1/transport/reconnect", s.auth(s.handleReconnect))
		mux.HandleFunc("/api/v1/subscribe/refresh", s.auth(s.handleSubscribeRefresh))
	}
	if s.opts.Role == RoleServer {
		mux.HandleFunc("/api/v1/clients", s.auth(s.handleClients))
	}
}

func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.opts.Logger != nil {
			s.opts.Logger.Debug("mgmt request", "method", r.Method, "path", r.URL.Path, "remote", r.RemoteAddr)
		}
		secret := strings.TrimSpace(s.opts.Secret)
		if secret == "" {
			writeErr(w, http.StatusUnauthorized, "mgmt secret is not configured")
			return
		}
		auth := r.Header.Get("Authorization")
		const prefix = "Bearer "
		if !strings.HasPrefix(auth, prefix) || subtleConstantTimeEq(strings.TrimPrefix(auth, prefix), secret) == false {
			writeErr(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next(w, r)
	}
}

func subtleConstantTimeEq(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var v byte
	for i := 0; i < len(a); i++ {
		v |= a[i] ^ b[i]
	}
	return v == 0
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "version": version.Version})
}

func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"service": "lynx-" + string(s.opts.Role),
		"version": version.Version,
		"uptime":  time.Since(s.opts.StartedAt).Round(time.Second).String(),
		"started": s.opts.StartedAt.UTC().Format(time.RFC3339),
	})
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	st := map[string]any{}
	if s.opts.Status != nil {
		st = s.opts.Status()
	}
	st["version"] = version.Version
	st["uptime"] = time.Since(s.opts.StartedAt).Round(time.Second).String()
	st["role"] = string(s.opts.Role)
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	if s.opts.Config == nil {
		writeErr(w, http.StatusNotImplemented, "config store unavailable")
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, s.opts.Config.GetRedacted())
	case http.MethodPut, http.MethodPatch:
		body, err := readBody(r, 1<<20)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		if int64(len(body)) > 1<<20 {
			writeErr(w, http.StatusBadRequest, "body too large")
			return
		}
		replace := r.Method == http.MethodPut
		restartRequired, applied, err := s.opts.Config.Put(body, replace)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		resp := map[string]any{"written": true, "restart_required": restartRequired, "applied": applied, "path": s.opts.Config.Path()}
		if restartRequired && s.opts.ApplyRestart && s.opts.Service != nil {
			writeJSON(w, http.StatusAccepted, resp)
			go func() {
				time.Sleep(300 * time.Millisecond)
				_ = s.opts.Service.Restart()
			}()
			return
		}
		writeJSON(w, http.StatusOK, resp)
	default:
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	if s.opts.Logger == nil {
		writeErr(w, http.StatusNotImplemented, "logger unavailable")
		return
	}
	level := logx.ParseLevel(r.URL.Query().Get("level"))
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	ch := s.opts.Logger.Subscribe(level)
	defer s.opts.Logger.Unsubscribe(ch)
	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case e, ok := <-ch:
			if !ok {
				return
			}
			b, _ := json.Marshal(e)
			fmt.Fprintf(w, "data: %s\n\n", b)
			flusher.Flush()
		}
	}
}

func (s *Server) handleRestart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if s.opts.Service == nil {
		writeErr(w, http.StatusNotImplemented, "restart unavailable")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"restarting": true})
	go func() {
		time.Sleep(300 * time.Millisecond)
		if err := s.opts.Service.Restart(); err != nil && s.opts.Logger != nil {
			s.opts.Logger.Error("restart failed", "err", err)
		}
	}()
}

func (s *Server) handleReload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if s.opts.Service == nil {
		writeErr(w, http.StatusNotImplemented, "reload unavailable")
		return
	}
	applied, needRestart, err := s.opts.Service.Reload()
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"applied": applied, "restart_required": needRestart})
}

func (s *Server) handleUpgrade(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		cur := version.Version
		latest := ""
		if s.opts.Upgrade != nil {
			tag, err := s.opts.Upgrade.Latest(r.Context(), "latest")
			if err == nil {
				latest = tag
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"current":       cur,
			"latest":        latest,
			"allow_upgrade": s.opts.AllowUpgrade,
		})
	case http.MethodPost:
		if !s.opts.AllowUpgrade {
			writeErr(w, http.StatusForbidden, "upgrade disabled (set mgmt.allow_upgrade)")
			return
		}
		if s.opts.Upgrade == nil {
			writeErr(w, http.StatusNotImplemented, "upgrade unavailable")
			return
		}
		var req struct {
			Tag string `json:"tag"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		err := s.opts.Upgrade.Start(context.Background(), req.Tag, func() error {
			if s.opts.Service != nil {
				return s.opts.Service.Restart()
			}
			return upgrade.RestartService(s.opts.Unit)
		})
		if err != nil {
			writeErr(w, http.StatusConflict, err.Error())
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"started": true})
	default:
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) handleUpgradeStatus(w http.ResponseWriter, r *http.Request) {
	if s.opts.Upgrade == nil {
		writeJSON(w, http.StatusOK, upgrade.Status{State: "idle"})
		return
	}
	writeJSON(w, http.StatusOK, s.opts.Upgrade.Status())
}

func (s *Server) handleReconnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if s.opts.Service == nil {
		writeErr(w, http.StatusNotImplemented, "reconnect unavailable")
		return
	}
	if err := s.opts.Service.Reconnect(); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"reconnected": true})
}

func (s *Server) handleSubscribeRefresh(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if s.opts.Service == nil {
		writeErr(w, http.StatusNotImplemented, "subscribe refresh unavailable")
		return
	}
	if err := s.opts.Service.SubscribeRefresh(); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"refreshed": true, "restart_required": true})
}

func (s *Server) handleClients(w http.ResponseWriter, r *http.Request) {
	// Clients are part of server config redacted view; dedicated list endpoint.
	if s.opts.Config == nil {
		writeErr(w, http.StatusNotImplemented, "unavailable")
		return
	}
	raw, ok := s.opts.Config.GetRedacted().(map[string]any)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "bad config view")
		return
	}
	writeJSON(w, http.StatusOK, raw["clients"])
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]any{"error": msg})
}

func readBody(r *http.Request, limit int64) ([]byte, error) {
	defer r.Body.Close()
	return io.ReadAll(io.LimitReader(r.Body, limit+1))
}

// FileConfigStore implements ConfigStore for client/server JSON files.
type FileConfigStore struct {
	PathName string
	Role     Role
	mu       sync.Mutex
	Client   *config.Client
	Server   *config.Server
	OnChange func()
	Logger   *logx.Logger
}

func (f *FileConfigStore) Path() string { return f.PathName }

func (f *FileConfigStore) GetRedacted() any {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Role == RoleClient {
		return config.RedactClient(f.Client)
	}
	return config.RedactServer(f.Server)
}

func (f *FileConfigStore) Put(body []byte, replace bool) (bool, []string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	backupDir := "/var/backups/lynx"
	if _, err := config.BackupFile(f.PathName, backupDir); err != nil {
		// Fallback beside the file if /var/backups is unavailable.
		if _, err2 := config.BackupFile(f.PathName, ""); err2 != nil {
			return false, nil, fmt.Errorf("backup config: %v / %v", err, err2)
		}
	}
	if f.Role == RoleClient {
		merged, err := config.MergeClientJSON(f.Client, body, replace)
		if err != nil {
			return false, nil, err
		}
		if err := config.WriteClient(f.PathName, merged); err != nil {
			return false, nil, err
		}
		f.Client = merged
		if f.OnChange != nil {
			f.OnChange()
		}
		restart := true
		applied := []string{"file"}
		// Log level can hot-apply.
		if f.Logger != nil && merged.Log.Level != "" {
			f.Logger.SetLevel(logx.ParseLevel(merged.Log.Level))
			applied = append(applied, "log.level")
		}
		return restart, applied, nil
	}
	merged, err := config.MergeServerJSON(f.Server, body, replace)
	if err != nil {
		return false, nil, err
	}
	if err := config.WriteServer(f.PathName, merged); err != nil {
		return false, nil, err
	}
	f.Server = merged
	if f.OnChange != nil {
		f.OnChange()
	}
	applied := []string{"file"}
	if f.Logger != nil && merged.Log.Level != "" {
		f.Logger.SetLevel(logx.ParseLevel(merged.Log.Level))
		applied = append(applied, "log.level")
	}
	return true, applied, nil
}

// SelfRestart cancels ctx then re-execs the current process.
func SelfRestart(cancel context.CancelFunc) error {
	cancel()
	time.Sleep(200 * time.Millisecond)
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	attr := &os.ProcAttr{
		Files: []*os.File{os.Stdin, os.Stdout, os.Stderr},
		Env:   os.Environ(),
	}
	p, err := os.StartProcess(exe, os.Args, attr)
	if err != nil {
		return err
	}
	_ = p.Release()
	os.Exit(0)
	return nil
}
