package monitor

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRemoteHostsRefreshPreservesGoodMappingAndAppliesDeletes(t *testing.T) {
	_, _, e, now := testEngine()
	o := observation(now)
	o.IP = "192.0.2.10"
	e.Observe(o)
	count := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count++
		if r.Method != "GET" || r.URL.RawQuery != "" {
			t.Error("unexpected request")
		}
		switch count {
		case 1:
			w.Header().Set("ETag", `"one"`)
			w.Write([]byte("192.0.2.10 first.example"))
		case 2:
			if r.Header.Get("If-None-Match") != `"one"` {
				t.Error("missing conditional request")
			}
			w.WriteHeader(304)
		case 3:
			w.WriteHeader(503)
		case 4:
			w.Header().Set("ETag", `"bad"`)
			w.Write([]byte("not a hosts file"))
		case 5:
			if r.Header.Get("If-None-Match") != `"one"` {
				t.Error("failed parse changed etag")
			}
			w.Header().Set("ETag", `"two"`)
			w.Write([]byte("192.0.2.10 second.example"))
		case 6:
			w.Write([]byte("# deliberately empty"))
		}
	}))
	defer ts.Close()
	s := &HostsSync{url: ts.URL, client: ts.Client(), status: HostsSyncStatus{URL: "https://github.com/org/repo/blob/main/file.hosts"}}
	for i := 1; i <= 6; i++ {
		err := s.refresh(context.Background(), e)
		if (i == 3 || i == 4) != (err != nil) {
			t.Fatalf("request %d: %v", i, err)
		}
		want := "first.example"
		if i == 5 {
			want = "second.example"
		}
		if i == 6 {
			want = ""
		}
		if got := e.Devices()[0].HostsName.Value; got != want {
			t.Fatalf("request %d: %q", i, got)
		}
		status := s.Status()
		if status.LastSuccess.IsZero() || status.LastChecked.IsZero() {
			t.Fatal("missing sync status")
		}
		if i == 3 && status.Error == "" {
			t.Fatal("missing failure status")
		}
	}
}
func TestRemoteHostsLimitsAndRedirects(t *testing.T) {
	_, _, e, _ := testEngine()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(strings.Repeat("#", (4<<20)+1))) }))
	defer ts.Close()
	s, _ := NewHostsSync("https://github.com/org/repo/blob/main/file.hosts")
	s.url = ts.URL
	if err := s.refresh(context.Background(), e); err == nil {
		t.Fatal("accepted oversized file")
	}
	if err := s.client.CheckRedirect(nil, nil); err != http.ErrUseLastResponse {
		t.Fatal("redirect allowed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan struct{})
	go func() { s.Run(ctx, e, time.Minute); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("shutdown blocked")
	}
}
