package notify

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/yude/tunwatch/internal/model"
)

func TestWebhookValidation(t *testing.T) {
	for _, raw := range []string{"http://discord.com/api/webhooks/1/a", "https://evil.test/api/webhooks/1/a", "https://discord.com.evil.test/api/webhooks/1/a", "https://discord.com/api/webhooks/1/a?extra=1", "https://discord.com:443/api/webhooks/1/a"} {
		if _, err := NewDiscord(raw); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	if _, err := NewDiscord("https://discord.com/api/webhooks/123/secret-token"); err != nil {
		t.Fatal(err)
	}
}
func TestMinimalPayloadAndNoMentions(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(body)
		if strings.Contains(string(raw), "secret-host") || strings.Contains(string(raw), "192.0.2.1") {
			t.Error("unnecessary identity sent")
		}
		if string(body["allowed_mentions"]) != `{"parse":[]}` {
			t.Error("mentions enabled")
		}
		var embeds []struct {
			Title     string
			Color     int
			Timestamp string
			Fields    []struct{ Name, Value string }
		}
		if err := json.Unmarshal(body["embeds"], &embeds); err != nil || len(embeds) != 1 {
			t.Errorf("missing embed: %s", raw)
		} else {
			e := embeds[0]
			if e.Color != 0x22c55e || !strings.Contains(e.Title, "観測") || len(e.Fields) != 2 || e.Fields[0].Value != "00:11:22:33:44:55" {
				t.Errorf("bad embed: %+v", e)
			}
			if _, err := time.Parse(time.RFC3339, e.Timestamp); err != nil {
				t.Error(err)
			}
		}
		if _, ok := body["content"]; ok {
			t.Error("plain text payload remains")
		}
		w.WriteHeader(204)
	}))
	defer ts.Close()
	d := &Discord{endpoint: ts.URL, client: ts.Client()}
	if err := d.Send(context.Background(), model.Event{Type: "online", Device: model.Device{MAC: "00:11:22:33:44:55", Hostname: model.Hint{Value: "secret-host"}, IPs: []string{"192.0.2.1"}}}); err != nil {
		t.Fatal(err)
	}
}
func TestErrorsDoNotExposeWebhookSecret(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(403) }))
	defer ts.Close()
	d := &Discord{endpoint: ts.URL + "/secret-token", client: ts.Client()}
	err := d.Send(context.Background(), model.Event{})
	if err == nil || strings.Contains(err.Error(), "secret-token") {
		t.Fatalf("unsafe error: %v", err)
	}
}

func TestOfflineEmbed(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Embeds []struct {
				Title string
				Color int
			}
		}
		if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
			t.Error(err)
		}
		if len(b.Embeds) != 1 || b.Embeds[0].Color != 0xf59e0b || !strings.Contains(b.Embeds[0].Title, "推定") {
			t.Errorf("bad offline embed: %+v", b)
		}
		w.WriteHeader(204)
	}))
	defer ts.Close()
	d := &Discord{endpoint: ts.URL, client: ts.Client()}
	if err := d.Send(context.Background(), model.Event{Type: "offline", Time: time.Now()}); err != nil {
		t.Fatal(err)
	}
}
