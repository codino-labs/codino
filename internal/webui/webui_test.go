package webui_test

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/codino-labs/codino/internal/devicelink"
	"github.com/codino-labs/codino/internal/host"
	"github.com/codino-labs/codino/internal/preferences"
	"github.com/codino-labs/codino/internal/webui"
)

// fakeClock is the injected clock: tests move time by hand and never sleep.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// ui is a web UI served by an httptest server, for a Host without a Device
// whose Preferences are a config file in a temporary directory.
type ui struct {
	t      *testing.T
	clock  *fakeClock
	host   *host.Host
	prefs  preferences.File
	server *webui.Server
	http   *httptest.Server
	// client has been through /open: it holds the UI's cookie.
	client *http.Client
	base   string
}

const configText = "# my Codino setup\nbrightness = 40 # dim desk\n"

func newUI(t *testing.T) *ui {
	t.Helper()
	clock := &fakeClock{now: time.UnixMilli(1760432400000)}
	prefs := preferences.File{Path: filepath.Join(t.TempDir(), "config.toml")}
	if err := os.WriteFile(prefs.Path, []byte(configText), 0o600); err != nil {
		t.Fatal(err)
	}
	h := host.New(clock, devicelink.NoDevices, host.WithPreferences(prefs))
	t.Cleanup(h.Close)
	u := &ui{t: t, clock: clock, host: h, prefs: prefs}
	u.http = httptest.NewUnstartedServer(nil)
	u.base = "http://" + u.http.Listener.Addr().String()
	u.server = webui.NewServer(webui.Config{Host: h, Preferences: prefs, Clock: clock, Addr: u.http.Listener.Addr().String()})
	u.http.Config.Handler = u.server
	u.http.Start()
	t.Cleanup(u.http.Close)
	t.Cleanup(u.server.Stop)
	jar, _ := cookiejar.New(nil)
	u.client = &http.Client{Jar: jar}
	resp := u.get(u.client, u.server.OpenURL())
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("opening the UI: %s", resp.Status)
	}
	return u
}

func (u *ui) get(c *http.Client, url string) *http.Response {
	u.t.Helper()
	resp, err := c.Get(url)
	if err != nil {
		u.t.Fatalf("GET %s: %v", url, err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return resp
}

// post sends a same-origin JSON POST, as the page does.
func (u *ui) post(path, body string) (int, string) {
	u.t.Helper()
	req, _ := http.NewRequest(http.MethodPost, u.base+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", u.base)
	resp, err := u.client.Do(req)
	if err != nil {
		u.t.Fatalf("POST %s: %v", path, err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(out)
}

func (u *ui) config() string {
	u.t.Helper()
	data, err := os.ReadFile(u.prefs.Path)
	if err != nil {
		u.t.Fatal(err)
	}
	return string(data)
}

// lines are the protocol lines w has seen so far.
func lines(w *host.Watcher) []string {
	var out []string
	for {
		select {
		case m := <-w.Mirrored():
			if m.Device == nil {
				out = append(out, string(m.Line))
			}
		default:
			return out
		}
	}
}

func TestThePageAndItsAPINeedTheCookieOfTheOpenURL(t *testing.T) {
	u := newUI(t)
	stranger := &http.Client{}

	for _, path := range []string{"/", "/app.js", "/device.wasm", "/api/state", "/api/stream"} {
		if resp := u.get(stranger, u.base+path); resp.StatusCode != http.StatusForbidden {
			t.Errorf("GET %s without the cookie: %s, want 403", path, resp.Status)
		}
		if path == "/api/stream" {
			continue
		}
		if resp := u.get(u.client, u.base+path); resp.StatusCode != http.StatusOK {
			t.Errorf("GET %s with the cookie: %s, want 200", path, resp.Status)
		}
	}
}

func TestAnOpenURLWorksOnce(t *testing.T) {
	u := newUI(t)
	open := u.server.OpenURL()
	jar, _ := cookiejar.New(nil)
	first := &http.Client{Jar: jar}
	if resp := u.get(first, open); resp.StatusCode != http.StatusOK {
		t.Fatalf("first visit: %s", resp.Status)
	}

	jar2, _ := cookiejar.New(nil)
	second := &http.Client{Jar: jar2}
	if resp := u.get(second, open); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("second visit of the same URL: %s, want 403", resp.Status)
	}
	if resp := u.get(second, u.base+"/api/state"); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("API after a reused URL: %s, want 403", resp.Status)
	}
}

func TestRequestsForAnotherHostAreRefused(t *testing.T) {
	u := newUI(t)
	req, _ := http.NewRequest(http.MethodGet, u.base+"/api/state", nil)
	req.Host = "attacker.example:" + strings.Split(u.base, ":")[2]

	resp, err := u.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("got %s, want 403 (DNS rebinding)", resp.Status)
	}
}

func TestPostsFromAnotherOriginChangeNothing(t *testing.T) {
	u := newUI(t)
	for name, header := range map[string][2]string{
		"another Origin":     {"Origin", "http://attacker.example"},
		"a cross-site fetch": {"Sec-Fetch-Site", "cross-site"},
		"a form, not JSON":   {"Content-Type", "application/x-www-form-urlencoded"},
	} {
		req, _ := http.NewRequest(http.MethodPost, u.base+"/api/preferences", strings.NewReader(`{"key":"brightness","value":"90"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(header[0], header[1])
		resp, err := u.client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s: got %s, want 403", name, resp.Status)
		}
	}
	if got := u.config(); got != configText {
		t.Fatalf("config file changed:\n%s", got)
	}
}

func TestAPreferenceSetInThePageWritesTheConfigFileAndReachesTheDevice(t *testing.T) {
	u := newUI(t)
	w := u.host.Watch()
	defer w.Close()
	lines(w)

	code, body := u.post("/api/preferences", `{"key":"brightness","value":"30"}`)

	if code != http.StatusOK {
		t.Fatalf("got %d %s", code, body)
	}
	want := "# my Codino setup\nbrightness = 30 # dim desk\n"
	if got := u.config(); got != want {
		t.Errorf("config file:\n%s\nwant:\n%s", got, want)
	}
	pushed := lines(w)
	if len(pushed) != 1 || !strings.Contains(pushed[0], `"brightness":30`) {
		t.Errorf("sent %q, want a Preferences push at once", pushed)
	}
}

func TestAnInvalidPreferenceIsRefusedWithTheReason(t *testing.T) {
	u := newUI(t)

	code, body := u.post("/api/preferences", `{"key":"brightness","value":"300"}`)

	if code != http.StatusBadRequest || !strings.Contains(body, "brightness") {
		t.Fatalf("got %d %s, want 400 naming the Preference", code, body)
	}
	if got := u.config(); got != configText {
		t.Fatalf("config file changed:\n%s", got)
	}
}

func TestUnsettingAPreferenceReturnsItToItsDefault(t *testing.T) {
	u := newUI(t)

	code, body := u.post("/api/preferences", `{"key":"brightness","unset":true}`)

	if code != http.StatusOK {
		t.Fatalf("got %d %s", code, body)
	}
	if got := u.config(); strings.Contains(got, "brightness") {
		t.Fatalf("brightness still set:\n%s", got)
	}
}

func TestTheStateListsThePreferencesAndThePreview(t *testing.T) {
	u := newUI(t)
	u.post("/api/preview", `{"state":"error","elapsed_ms":0}`)

	resp, err := u.client.Get(u.base + "/api/state")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var st struct {
		Preferences []struct{ Key, Value string }
		Status      host.Status
	}
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		t.Fatal(err)
	}

	found := false
	for _, p := range st.Preferences {
		found = found || (p.Key == "brightness" && p.Value == "40")
	}
	if !found {
		t.Errorf("preferences %+v lack brightness = 40", st.Preferences)
	}
	if st.Status.Preview != host.Error {
		t.Errorf("preview %q, want error", st.Status.Preview)
	}
}

func TestPreviewControlsForceTheDeviceIntoAStateThenRestoreIt(t *testing.T) {
	u := newUI(t)
	w := u.host.Watch()
	defer w.Close()
	lines(w)

	if code, body := u.post("/api/preview", `{"state":"awaiting_input","elapsed_ms":150000}`); code != http.StatusOK {
		t.Fatalf("preview: %d %s", code, body)
	}
	if code, body := u.post("/api/preview", `{"state":""}`); code != http.StatusOK {
		t.Fatalf("end preview: %d %s", code, body)
	}

	want := []string{
		`{"type":"preview_enter","state":"awaiting_input","elapsed_ms":150000}`,
		`{"type":"preview_exit"}`,
	}
	if got := lines(w); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("sent %q, want %q", got, want)
	}
	if code, _ := u.post("/api/preview", `{"state":"asleep"}`); code != http.StatusBadRequest {
		t.Fatalf("an unknown state: %d, want 400", code)
	}
}

func TestTheStreamStartsTheBrowserDeviceLikeTheDevice(t *testing.T) {
	u := newUI(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u.base+"/api/stream", nil)
	resp, err := u.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("Content-Type %q", ct)
	}

	events := readEvents(t, bufio.NewReader(resp.Body), 3)

	if events[0].name != "device" || !strings.Contains(events[0].data, `"width":8`) {
		t.Errorf("first event %+v, want the Device's Hardware Profile", events[0])
	}
	if events[1].name != "line" || !strings.Contains(events[1].data, `"type":"hello"`) {
		t.Errorf("second event %+v, want hello", events[1])
	}
	if events[2].name != "line" || !strings.Contains(events[2].data, `"brightness":40`) {
		t.Errorf("third event %+v, want the Preferences", events[2])
	}
}

type event struct{ name, data string }

func readEvents(t *testing.T, r *bufio.Reader, n int) []event {
	t.Helper()
	var out []event
	var e event
	for len(out) < n {
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatalf("after %d events: %v", len(out), err)
		}
		line = strings.TrimRight(line, "\n")
		switch {
		case line == "":
			if e.name != "" {
				out = append(out, e)
			}
			e = event{}
		case strings.HasPrefix(line, "event: "):
			e.name = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			e.data = strings.TrimPrefix(line, "data: ")
		}
	}
	return out
}

func TestTheUIStaysOpenWhileThePageHeartbeats(t *testing.T) {
	u := newUI(t)

	for range 10 {
		u.clock.Advance(webui.KeepAlive - time.Second)
		if code, _ := u.post("/api/heartbeat", `{}`); code != http.StatusOK {
			t.Fatalf("heartbeat: %d", code)
		}
	}
	if u.server.Expired() {
		t.Fatal("expired while the page heartbeats")
	}

	u.clock.Advance(webui.KeepAlive)
	if !u.server.Expired() {
		t.Fatal("not expired a KeepAlive after the last heartbeat")
	}
}

func TestAnOpenURLWaitsForTheBrowserToStart(t *testing.T) {
	clock := &fakeClock{now: time.UnixMilli(1760432400000)}
	h := host.New(clock, devicelink.NoDevices)
	defer h.Close()
	s := webui.NewServer(webui.Config{Host: h, Preferences: preferences.File{Path: filepath.Join(t.TempDir(), "c.toml")},
		Clock: clock, Addr: "127.0.0.1:1"})
	defer s.Stop()

	s.OpenURL()
	clock.Advance(webui.FirstVisitWait - time.Second)
	if s.Expired() {
		t.Fatal("expired before the browser had time to open the page")
	}
	clock.Advance(time.Second)
	if !s.Expired() {
		t.Fatal("still open with no page FirstVisitWait after the URL")
	}
}

func TestAnOpenStreamKeepsTheUIOpen(t *testing.T) {
	u := newUI(t)
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u.base+"/api/stream", nil)
	resp, err := u.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	readEvents(t, bufio.NewReader(resp.Body), 1)

	u.clock.Advance(time.Hour) // a background tab whose timers are throttled
	if u.server.Expired() {
		t.Fatal("expired with the page's stream open")
	}

	cancel()
	resp.Body.Close()
	deadline := time.Now().Add(5 * time.Second)
	for u.server.Streams() > 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond) // the server notices the closed connection
	}
	u.clock.Advance(webui.KeepAlive)
	if !u.server.Expired() {
		t.Fatal("still open a KeepAlive after the page closed its stream")
	}
}
