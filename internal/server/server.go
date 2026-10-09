package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/yude/tunwatch/internal/config"
	"github.com/yude/tunwatch/internal/monitor"
	"github.com/yude/tunwatch/internal/store"
)

//go:embed ui/*
var assets embed.FS

type Server struct {
	Engine      *monitor.Engine
	Store       *store.Store
	Config      config.Config
	Ready       atomic.Bool
	Stats       func() any
	streams     chan struct{}
	queries     chan struct{}
	loginSecond atomic.Int64
	loginCount  atomic.Int64
}

func New(e *monitor.Engine, s *store.Store, c config.Config) *Server {
	return &Server{Engine: e, Store: s, Config: c, streams: make(chan struct{}, 16), queries: make(chan struct{}, 2)}
}
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok\n")) })
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		if !s.Ready.Load() {
			http.Error(w, "not ready", 503)
			return
		}
		w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("POST /api/v1/session", s.login)
	mux.HandleFunc("DELETE /api/v1/session", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "tunwatch_session", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode})
		w.WriteHeader(204)
	})
	mux.Handle("GET /api/v1/devices", s.auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		respond(w, map[string]any{"devices": s.Engine.Devices(), "time": time.Now().UTC()})
	})))
	mux.Handle("GET /api/v1/events", s.auth(http.HandlerFunc(s.events)))
	mux.Handle("GET /api/v1/stream", s.auth(http.HandlerFunc(s.stream)))
	mux.Handle("GET /api/v1/status", s.auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count, rejected := s.Engine.Stats()
		var stats any
		if s.Stats != nil {
			stats = s.Stats()
		}
		respond(w, map[string]any{"ready": s.Ready.Load(), "node_name": s.Config.NodeName, "device_count": count, "capacity_rejections": rejected, "capture": stats, "config": s.Config})
	})))
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) { http.Error(w, "not found", 404) })
	sub, _ := fs.Sub(assets, "ui")
	mux.Handle("/", http.FileServer(http.FS(sub)))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		mux.ServeHTTP(w, r)
	})
}
func respond(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(v)
}
func (s *Server) validToken(token string) bool {
	a, b := sha256.Sum256([]byte(token)), sha256.Sum256([]byte(s.Config.Token))
	return subtle.ConstantTimeCompare(a[:], b[:]) == 1
}
func (s *Server) sign(value string) string {
	h := hmac.New(sha256.New, []byte(s.Config.Token))
	h.Write([]byte(value))
	return hex.EncodeToString(h.Sum(nil))
}
func (s *Server) authenticated(r *http.Request) bool {
	if value, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok && s.validToken(value) {
		return true
	}
	cookie, err := r.Cookie("tunwatch_session")
	if err != nil {
		return false
	}
	parts := strings.Split(cookie.Value, ".")
	if len(parts) != 2 {
		return false
	}
	until, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || time.Now().Unix() >= until || until > time.Now().Add(9*time.Hour).Unix() {
		return false
	}
	return hmac.Equal([]byte(parts[1]), []byte(s.sign(parts[0])))
}
func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.authenticated(r) {
			http.Error(w, "authentication required", 401)
			return
		}
		next.ServeHTTP(w, r)
	})
}
func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	sec := time.Now().Unix()
	if s.loginSecond.Swap(sec) != sec {
		s.loginCount.Store(0)
	}
	if s.loginCount.Add(1) > 10 {
		http.Error(w, "try again later", 429)
		return
	}
	var body struct {
		Token string `json:"token"`
	}
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		http.Error(w, "application/json required", 415)
		return
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil || !s.validToken(body.Token) {
		http.Error(w, "invalid token", 401)
		return
	}
	expires := time.Now().Add(8 * time.Hour)
	value := strconv.FormatInt(expires.Unix(), 10)
	http.SetCookie(w, &http.Cookie{Name: "tunwatch_session", Value: value + "." + s.sign(value), Path: "/", HttpOnly: true, Secure: r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https", SameSite: http.SameSiteStrictMode, Expires: expires, MaxAge: 8 * 3600})
	w.WriteHeader(204)
}
func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	select {
	case s.queries <- struct{}{}:
		defer func() { <-s.queries }()
	default:
		http.Error(w, "history query busy", 429)
		return
	}
	limit := 100
	if q := r.URL.Query().Get("limit"); q != "" {
		n, e := strconv.Atoi(q)
		if e != nil || n < 1 || n > 1000 {
			http.Error(w, "limit must be 1..1000", 400)
			return
		}
		limit = n
	}
	before, device := r.URL.Query().Get("before"), r.URL.Query().Get("device")
	if len(before) > 64 || len(device) > 256 {
		http.Error(w, "invalid cursor or device", 400)
		return
	}
	events, err := s.Store.Query(before, device, limit)
	if err != nil {
		http.Error(w, "history unavailable", 503)
		return
	}
	next := ""
	if len(events) == limit {
		next = events[len(events)-1].ID
	}
	respond(w, map[string]any{"events": events, "next_cursor": next})
}
func (s *Server) stream(w http.ResponseWriter, r *http.Request) {
	select {
	case s.streams <- struct{}{}:
		defer func() { <-s.streams }()
	default:
		http.Error(w, "stream limit reached", 503)
		return
	}
	f, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", 500)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("X-Accel-Buffering", "no")
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		if !s.authenticated(r) {
			return
		}
		data, err := json.Marshal(map[string]any{"devices": s.Engine.Devices(), "time": time.Now().UTC(), "ready": s.Ready.Load()})
		if err != nil {
			return
		}
		http.NewResponseController(w).SetWriteDeadline(time.Now().Add(10 * time.Second))
		if _, err = fmt.Fprintf(w, "event: snapshot\ndata: %s\n\n", data); err != nil {
			return
		}
		f.Flush()
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
		}
	}
}
