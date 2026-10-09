package server

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/yude/tunwatch/internal/config"
	"github.com/yude/tunwatch/internal/model"
	"github.com/yude/tunwatch/internal/monitor"
	"github.com/yude/tunwatch/internal/store"
)

func setup(t *testing.T) (*Server, http.Handler) {
	t.Helper()
	s, err := store.Open(t.TempDir(), 30, 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	c := config.Config{Token: "0123456789abcdef0123456789abcdef", Webhook: "never-expose-this-secret", MaxDevices: 100, RetentionDays: 30}
	e := monitor.NewEngine(c, s, nil, nil, time.Now())
	e.Observe(model.Observation{Interface: "test0", MAC: "00:11:22:33:44:55", Source: "arp", At: time.Now()})
	api := New(e, s, c)
	api.Ready.Store(true)
	return api, api.Handler()
}
func TestAuthenticationAndSecretRedaction(t *testing.T) {
	s, h := setup(t)
	for _, path := range []string{"/api/v1/devices", "/api/v1/events", "/api/v1/status", "/api/v1/stream"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 401 {
			t.Fatalf("unprotected %s: %d", path, w.Code)
		}
	}
	r := httptest.NewRequest("GET", "/api/v1/status", nil)
	r.Header.Set("Authorization", "Bearer "+s.Config.Token)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 || strings.Contains(w.Body.String(), s.Config.Token) || strings.Contains(w.Body.String(), s.Config.Webhook) {
		t.Fatal("status leaked secrets or failed")
	}
}
func TestSessionCookieAndInvalidCursorLimit(t *testing.T) {
	s, h := setup(t)
	r := httptest.NewRequest("POST", "/api/v1/session", strings.NewReader(`{"token":"`+s.Config.Token+`"}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 204 {
		t.Fatalf("login: %d", w.Code)
	}
	cookie := w.Result().Cookies()[0]
	if !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatal("unsafe session cookie")
	}
	r = httptest.NewRequest("GET", "/api/v1/events?limit=0", nil)
	r.AddCookie(cookie)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 400 {
		t.Fatal("unbounded history request accepted")
	}
	cookie.Value += "tampered"
	r = httptest.NewRequest("GET", "/api/v1/devices", nil)
	r.AddCookie(cookie)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("tampered session accepted")
	}
}
func TestSSESnapshot(t *testing.T) {
	s, h := setup(t)
	ts := httptest.NewServer(h)
	defer ts.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r, _ := http.NewRequestWithContext(ctx, "GET", ts.URL+"/api/v1/stream", nil)
	r.Header.Set("Authorization", "Bearer "+s.Config.Token)
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	reader := bufio.NewReader(resp.Body)
	line, err := reader.ReadString('\n')
	if err != nil || line != "event: snapshot\n" {
		t.Fatalf("SSE: %q %v", line, err)
	}
	line, _ = reader.ReadString('\n')
	if !strings.Contains(line, "00:11:22:33:44:55") {
		t.Fatal("inventory missing from SSE")
	}
	cancel()
}
