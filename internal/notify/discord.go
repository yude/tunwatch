package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/yude/tunwatch/internal/model"
)

type Provider interface {
	Send(context.Context, model.Event) error
}
type Discord struct {
	endpoint string
	client   *http.Client
}

func NewDiscord(raw string) (*Discord, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, errors.New("invalid Discord webhook URL")
	}
	if u.Scheme != "https" || u.User != nil || u.Port() != "" || u.RawQuery != "" || u.Fragment != "" || (u.Host != "discord.com" && u.Host != "canary.discord.com" && u.Host != "ptb.discord.com") || !regexp.MustCompile(`^/api/webhooks/[0-9]+/[A-Za-z0-9_-]+$`).MatchString(u.Path) {
		return nil, errors.New("webhook must be an HTTPS discord.com /api/webhooks/id/token URL")
	}
	return &Discord{endpoint: raw, client: &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (d *Discord) Send(ctx context.Context, e model.Event) error {
	status, color := "観測を開始 / 再開", 0x22c55e
	if e.Type == "offline" {
		status, color = "オフライン推定（無通信タイムアウト）", 0xf59e0b
	}
	// Keep the same minimal outbound identity fields, rendered as a Discord embed.
	field := func(name, value string) map[string]any {
		if value == "" {
			value = "未取得"
		}
		r := []rune(value)
		if len(r) > 1024 {
			value = string(r[:1024])
		}
		return map[string]any{"name": name, "value": value, "inline": true}
	}
	body, _ := json.Marshal(map[string]any{
		"embeds": []any{map[string]any{
			"title": "tunwatch · " + status, "color": color,
			"timestamp": e.Time.UTC().Format(time.RFC3339),
			"fields":    []any{field("MAC", e.Device.MAC), field("Interface", e.Device.Interface)},
		}},
		"allowed_mentions": map[string]any{"parse": []string{}},
	})
	for attempt := 0; attempt < 3; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.endpoint, bytes.NewReader(body))
		if err != nil {
			return errors.New("webhook request failed")
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := d.client.Do(req)
		delay := time.Duration(1<<attempt) * time.Second
		if err == nil {
			io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
			resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				return nil
			}
			if resp.StatusCode != 429 && resp.StatusCode < 500 {
				return fmt.Errorf("webhook rejected: HTTP %d", resp.StatusCode)
			}
			if n, e := strconv.ParseFloat(resp.Header.Get("Retry-After"), 64); e == nil && n > 0 {
				delay = max(delay, min(time.Minute, time.Duration(n*float64(time.Second))))
			}
		}
		if attempt < 2 {
			t := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				t.Stop()
				return ctx.Err()
			case <-t.C:
			}
		}
	}
	return errors.New("webhook failed after 3 attempts") // Never return HTTP errors containing the secret URL.
}

type Worker struct {
	provider Provider
	queue    chan model.Event
	Sent     atomic.Uint64
	Failed   atomic.Uint64
	Dropped  atomic.Uint64
}

func NewWorker(p Provider) *Worker { return &Worker{provider: p, queue: make(chan model.Event, 128)} }
func (w *Worker) Enqueue(e model.Event) {
	select {
	case w.queue <- e:
	default:
		w.Dropped.Add(1)
		slog.Warn("notification queue full; event remains in local history")
	}
}
func (w *Worker) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case e := <-w.queue:
			if err := w.provider.Send(ctx, e); err != nil {
				w.Failed.Add(1)
				slog.Warn("notification delivery failed", "error", err)
			} else {
				w.Sent.Add(1)
			}
		}
	}
}
