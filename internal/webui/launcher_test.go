package webui_test

import (
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/codino-labs/codino/internal/devicelink"
	"github.com/codino-labs/codino/internal/host"
	"github.com/codino-labs/codino/internal/preferences"
	"github.com/codino-labs/codino/internal/webui"
)

func newLauncher(t *testing.T) (*webui.Launcher, *host.Host, *fakeClock) {
	t.Helper()
	clock := &fakeClock{now: time.UnixMilli(1760432400000)}
	h := host.New(clock, devicelink.NoDevices, host.WithExitGrace(time.Minute))
	t.Cleanup(h.Close)
	l := &webui.Launcher{Host: h, Preferences: preferences.File{Path: filepath.Join(t.TempDir(), "c.toml")}, Clock: clock}
	t.Cleanup(l.Close)
	return l, h, clock
}

func open(t *testing.T, l *webui.Launcher) string {
	t.Helper()
	u, err := l.Open()
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return u
}

// visit opens the page in a fresh browser.
func visit(t *testing.T, openURL string) (*http.Client, error) {
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar, Timeout: 5 * time.Second}
	resp, err := c.Get(openURL)
	if err != nil {
		return nil, err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: %s", openURL, resp.Status)
	}
	return c, nil
}

func retired(h *host.Host) bool {
	select {
	case <-h.Retired():
		return true
	default:
		return false
	}
}

func TestTheUIServesOnLocalhostAndHoldsTheHostDaemon(t *testing.T) {
	l, h, clock := newLauncher(t)

	u := open(t, l)

	parsed, _ := url.Parse(u)
	if parsed.Hostname() != "127.0.0.1" {
		t.Fatalf("serves on %s, want 127.0.0.1", parsed.Host)
	}
	if _, err := visit(t, u); err != nil {
		t.Fatal(err)
	}
	clock.Advance(time.Minute)
	h.Tick()
	if retired(h) {
		t.Fatal("the Host Daemon retired with the UI open")
	}
}

func TestOpeningTheUIAgainReusesTheServer(t *testing.T) {
	l, _, _ := newLauncher(t)
	first, _ := url.Parse(open(t, l))

	second, _ := url.Parse(open(t, l))

	if first.Host != second.Host {
		t.Fatalf("second server %s, want %s", second.Host, first.Host)
	}
	if first.RawQuery == second.RawQuery {
		t.Fatal("same ticket twice")
	}
}

func TestClosingThePageStopsTheServerEndsThePreviewAndReleasesTheHostDaemon(t *testing.T) {
	l, h, clock := newLauncher(t)
	u := open(t, l)
	page, err := visit(t, u)
	if err != nil {
		t.Fatal(err)
	}
	base := strings.Split(u, "/open")[0]
	req, _ := http.NewRequest(http.MethodPost, base+"/api/preview", strings.NewReader(`{"state":"done"}`))
	req.Header.Set("Content-Type", "application/json")
	if resp, err := page.Do(req); err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("preview: %v %v", resp, err)
	}
	w := h.Watch()
	defer w.Close()
	lines(w)

	clock.Advance(webui.FirstVisitWait) // no heartbeat: the page is gone
	l.Sweep()

	if _, err := http.Get(base + "/"); err == nil {
		t.Fatal("the server still answers")
	}
	if got := lines(w); len(got) != 1 || got[0] != `{"type":"preview_exit"}` {
		t.Fatalf("sent %q, want the preview ended", got)
	}
	clock.Advance(time.Minute)
	h.Tick()
	if !retired(h) {
		t.Fatal("the Host Daemon is still held an exit grace after the UI closed")
	}
}

func TestTheUIOpensAgainAfterItClosed(t *testing.T) {
	l, _, clock := newLauncher(t)
	open(t, l)
	clock.Advance(webui.FirstVisitWait)
	l.Sweep()

	u := open(t, l)

	if _, err := visit(t, u); err != nil {
		t.Fatalf("the new UI: %v", err)
	}
}
