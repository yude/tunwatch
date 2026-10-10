package monitor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/yude/tunwatch/internal/config"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

type HostsSyncStatus struct {
	URL         string    `json:"url"`
	LastChecked time.Time `json:"last_checked"`
	LastSuccess time.Time `json:"last_success"`
	Entries     int       `json:"entries"`
	Error       string    `json:"error,omitempty"`
}
type HostsSync struct {
	url, etag string
	client    *http.Client
	mu        sync.RWMutex
	status    HostsSyncStatus
}

func NewHostsSync(raw string) (*HostsSync, error) {
	url, err := config.HostsURL(raw)
	if err != nil {
		return nil, err
	}
	return &HostsSync{url: url, status: HostsSyncStatus{URL: raw}, client: &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (s *HostsSync) Status() HostsSyncStatus { s.mu.RLock(); defer s.mu.RUnlock(); return s.status }
func (s *HostsSync) refresh(ctx context.Context, e *Engine) (err error) {
	defer func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.status.LastChecked = time.Now().UTC()
		if err != nil {
			s.status.Error = err.Error()
		} else {
			s.status.Error = ""
			s.status.LastSuccess = s.status.LastChecked
		}
	}()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.url, nil)
	if err != nil {
		return errors.New("hosts request failed")
	}
	if s.etag != "" {
		req.Header.Set("If-None-Match", s.etag)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return errors.New("hosts download failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotModified && s.etag != "" {
		return nil
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("hosts download: HTTP %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (4<<20)+1))
	if err != nil {
		return errors.New("hosts read failed")
	}
	if len(raw) > 4<<20 {
		return errors.New("hosts file exceeds 4 MiB")
	}
	h, err := parseHosts(bytes.NewReader(raw), Hosts{Names: map[string]string{}, File: s.status.URL})
	if err != nil {
		return err
	}
	e.SetHosts(h)
	s.etag = resp.Header.Get("ETag")
	s.mu.Lock()
	s.status.Entries = len(h.Names)
	s.mu.Unlock()
	return nil
}
func (s *HostsSync) Run(ctx context.Context, e *Engine, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if err := s.refresh(ctx, e); err != nil && ctx.Err() == nil {
			slog.Warn("hosts sync failed; keeping previous mapping", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
