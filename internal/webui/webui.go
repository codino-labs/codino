// Package webui is the web UI (`codino ui`): a page served by the Host
// Daemon on 127.0.0.1 while it is open, where the human edits the
// Preferences, browses the Themes and forces the Device into each Session
// State, on a live preview run by the Device core built for the browser.
//
// The preview gets the protocol stream the Device gets (host.Watcher) as
// Server-Sent Events; the page acts through JSON POSTs. Only the human who
// ran `codino ui` gets in: the URL it opens carries a one-time ticket that
// /open swaps for the page's address, under a secret key (/KEY/), so the
// key is never on a command line other local users can read and never goes
// to another server on 127.0.0.1, as a cookie would. Every request must name
// the server's own address as Host (no DNS rebinding), and a POST must come
// from the page's own origin as JSON (no cross-site request).
package webui

import (
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"

	"fmt"
	"io"
	"io/fs"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/codino-labs/codino/internal/host"
	"github.com/codino-labs/codino/internal/preferences"
	"github.com/codino-labs/codino/internal/protocol"
)

//go:embed assets
var assets embed.FS

// Host is what the web UI asks of the Host core.
type Host interface {
	Watch() *host.Watcher
	Preview(state host.SessionState, elapsed time.Duration) error
	EndPreview()
	Status() host.Status
	ReloadPreferences()
}

// Preferences is the config file the web UI edits, as `codino set` does.
type Preferences interface {
	List() ([]preferences.Entry, error)
	Set(key, value string) error
	Unset(key string) error
}

// Options are what a Server serves.
type Options struct {
	Host        Host
	Preferences Preferences
	Clock       host.Clock
	// Addr is the host:port the Server listens on: the only Host a request
	// may name.
	Addr string
	// PreferencesFile is where the Preferences are, for the page to say.
	PreferencesFile string
}

// KeepAlive is how long the UI stays open after the page's last heartbeat
// (it sends one every few seconds) once its stream is closed.
const KeepAlive = 30 * time.Second

// FirstVisitWait is how long an open URL waits for the browser to load the
// page, and how long its ticket is good for.
const FirstVisitWait = 2 * time.Minute

// streamKeepAlive is how often an idle stream gets a comment, so a closed
// page is noticed.
const streamKeepAlive = 15 * time.Second

// Server is the web UI's HTTP handler. It is open while the page heartbeats
// or holds its stream open, and for FirstVisitWait after each OpenURL.
type Server struct {
	opts Options
	key  string
	mux  *http.ServeMux
	done chan struct{}
	stop sync.Once

	mu sync.Mutex
	// tickets are the unused tickets of open URLs, by when they lapse.
	tickets map[string]time.Time
	// openUntil is when the UI closes without a heartbeat or a stream.
	openUntil time.Time
	streams   int
}

// NewServer returns the web UI for opts.
func NewServer(opts Options) *Server {
	s := &Server{opts: opts, key: randomHex(), done: make(chan struct{}), tickets: map[string]time.Time{},
		openUntil: opts.Clock.Now().Add(FirstVisitWait)}
	page, err := fs.Sub(assets, "assets")
	if err != nil {
		panic(err) // embedded at build time
	}
	s.mux = http.NewServeMux()
	s.mux.Handle("GET /", http.FileServerFS(page))
	s.mux.HandleFunc("GET /api/state", s.state)
	s.mux.HandleFunc("GET /api/stream", s.stream)
	s.mux.HandleFunc("POST /api/preferences", s.setPreference)
	s.mux.HandleFunc("POST /api/preview", s.preview)
	s.mux.HandleFunc("POST /api/heartbeat", s.heartbeat)
	return s
}

func randomHex() string {
	b := make([]byte, 32)
	rand.Read(b) // never fails (crypto/rand)
	return hex.EncodeToString(b)
}

// OpenURL returns a URL that opens the page once, within FirstVisitWait,
// and keeps the UI open until then.
func (s *Server) OpenURL() string {
	ticket := randomHex()
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.opts.Clock.Now()
	for t, lapse := range s.tickets {
		if !now.Before(lapse) {
			delete(s.tickets, t)
		}
	}
	s.tickets[ticket] = now.Add(FirstVisitWait)
	s.extend(now.Add(FirstVisitWait))
	return "http://" + s.opts.Addr + "/open?ticket=" + ticket
}

// extend keeps the UI open until at least t. Callers hold s.mu.
func (s *Server) extend(t time.Time) {
	if t.After(s.openUntil) {
		s.openUntil = t
	}
}

// Expired reports whether the page has gone: no stream open, no heartbeat
// for KeepAlive, and no open URL waiting for the browser.
func (s *Server) Expired() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.streams == 0 && !s.opts.Clock.Now().Before(s.openUntil)
}

// Stop ends every stream, so the HTTP server can shut down.
func (s *Server) Stop() { s.stop.Do(func() { close(s.done) }) }

// ServeHTTP checks who is asking before anything is served.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'wasm-unsafe-eval'; "+
		"style-src 'self'; img-src 'self' data:; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
	if r.Host != s.opts.Addr {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if r.URL.Path == "/open" && r.Method == http.MethodGet {
		s.open(w, r)
		return
	}
	rest, ok := s.underKey(r.URL.Path)
	if !ok {
		http.Error(w, "forbidden: run codino ui to open the page", http.StatusForbidden)
		return
	}
	if r.Method != http.MethodGet && !s.sameOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	r2 := r.Clone(r.Context())
	r2.URL.Path = rest
	r2.URL.RawPath = ""
	s.mux.ServeHTTP(w, r2)
}

// open swaps an unused ticket for the page's address, so the URL left in
// the browser's command line is spent.
func (s *Server) open(w http.ResponseWriter, r *http.Request) {
	ticket := r.URL.Query().Get("ticket")
	s.mu.Lock()
	lapse, ok := s.tickets[ticket]
	delete(s.tickets, ticket)
	s.mu.Unlock()
	if !ok || !s.opts.Clock.Now().Before(lapse) {
		http.Error(w, "forbidden: this link was used already; run codino ui again", http.StatusForbidden)
		return
	}
	http.Redirect(w, r, "/"+s.key+"/", http.StatusSeeOther)
}

// underKey returns the path below /KEY/, and whether path is under it.
func (s *Server) underKey(path string) (string, bool) {
	segment, rest, found := strings.Cut(strings.TrimPrefix(path, "/"), "/")
	if !found || subtle.ConstantTimeCompare([]byte(segment), []byte(s.key)) != 1 {
		return "", false
	}
	return "/" + rest, true
}

// sameOrigin tells a request of the page itself: JSON, from its origin
// (browsers send Origin with every POST).
func (s *Server) sameOrigin(r *http.Request) bool {
	if r.Header.Get("Origin") != "http://"+s.opts.Addr {
		return false
	}
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" {
		return false
	}
	return r.Header.Get("Content-Type") == "application/json"
}

// state is what the page shows besides the preview: the Preferences, the
// Device and the sessions.
type state struct {
	Preferences []preferences.Entry `json:"preferences"`
	// PreferencesProblem is why the config file can't be read.
	PreferencesProblem string      `json:"preferences_problem,omitempty"`
	PreferencesFile    string      `json:"preferences_file,omitempty"`
	Status             host.Status `json:"status"`
}

func (s *Server) state(w http.ResponseWriter, r *http.Request) {
	st := state{Status: s.opts.Host.Status(), PreferencesFile: s.opts.PreferencesFile}
	entries, err := s.opts.Preferences.List()
	if err != nil {
		st.PreferencesProblem = err.Error()
	}
	st.Preferences = entries
	writeJSON(w, http.StatusOK, st)
}

// maxBody bounds a POST's JSON.
const maxBody = 4096

// readJSON decodes a POST's body into v; false (and the reply sent) when it
// can't.
func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(io.LimitReader(r.Body, maxBody)).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("bad request: %w", err))
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, map[string]string{"error": err.Error()})
}

// setPreference sets or unsets one Preference in the config file, as
// `codino set` and `codino unset` do, and has the Host apply it at once.
func (s *Server) setPreference(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Key   string `json:"key"`
		Value string `json:"value"`
		Unset bool   `json:"unset"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	var err error
	if req.Unset {
		err = s.opts.Preferences.Unset(req.Key)
	} else {
		err = s.opts.Preferences.Set(req.Key, req.Value)
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	s.opts.Host.ReloadPreferences()
	writeJSON(w, http.StatusOK, struct{}{})
}

// preview forces the Device into a Session State, or ends the preview when
// the state is empty.
func (s *Server) preview(w http.ResponseWriter, r *http.Request) {
	var req struct {
		State     host.SessionState `json:"state"`
		ElapsedMS int64             `json:"elapsed_ms"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if req.State == "" {
		s.opts.Host.EndPreview()
	} else if err := s.opts.Host.Preview(req.State, time.Duration(req.ElapsedMS)*time.Millisecond); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, struct{}{})
}

// heartbeat keeps the UI open for KeepAlive: the page is still there.
func (s *Server) heartbeat(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.extend(s.opts.Clock.Now().Add(KeepAlive))
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, struct{}{})
}

// stream sends the page what the Device is sent, as Server-Sent Events:
// "device" (a Hardware Profile: start a Device afresh), "line" (a JSON
// protocol line) and "frame" (a Raw Mode frame, base64). It ends when the
// page goes, the Server stops, or the Host drops the Watcher (it fell
// behind: the page's EventSource connects again and starts over).
func (s *Server) stream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	s.mu.Lock()
	s.streams++
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.streams--
		// The page may only be reloading: as long as after a heartbeat.
		s.extend(s.opts.Clock.Now().Add(KeepAlive))
		s.mu.Unlock()
	}()
	watcher := s.opts.Host.Watch()
	defer watcher.Close()
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()
	idle := time.NewTicker(streamKeepAlive)
	defer idle.Stop()
	for {
		var err error
		select {
		case m, ok := <-watcher.Mirrored():
			if !ok {
				return
			}
			err = writeEvent(w, m)
		case <-idle.C:
			_, err = io.WriteString(w, ": still here\n\n")
		case <-r.Context().Done():
			return
		case <-s.done:
			return
		}
		if err != nil {
			return
		}
		flusher.Flush()
	}
}

func writeEvent(w io.Writer, m host.Mirrored) error {
	var name, data string
	switch {
	case m.Device != nil:
		b, err := json.Marshal(m.Device)
		if err != nil {
			return err
		}
		name, data = "device", string(b)
	case len(m.Line) > 0 && m.Line[0] == protocol.RawFrameStart:
		name, data = "frame", base64.StdEncoding.EncodeToString(m.Line)
	default:
		name, data = "line", string(m.Line)
	}
	_, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, data)
	return err
}
