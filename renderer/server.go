package main

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

//go:embed web
var webFS embed.FS

type event struct {
	Name string
	Data string
}

type Server struct {
	store *Store
	port  int
	css   string // path of custom.css, watched for live reloads

	mu         sync.Mutex
	clients    map[chan event]bool // value: connected from a window we launched (?app=1)
	current    showMsg
	lastLaunch time.Time
}

type showMsg struct {
	Path string `json:"path"`
	Line int    `json:"line"`
}

// configDir is ~/.config/notesview (or $XDG_CONFIG_HOME/notesview), home of custom.css.
func configDir() string {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "notesview")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "notesview")
}

func NewServer(store *Store, port int) *Server {
	return &Server{store: store, port: port, css: filepath.Join(configDir(), "custom.css"), clients: map[chan event]bool{}}
}

func (s *Server) broadcast(e event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for c := range s.clients {
		select {
		case c <- e:
		default:
		}
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	sub, _ := fs.Sub(webFS, "web")
	static := http.FileServer(http.FS(sub))
	mux.Handle("/", noCache(static))
	mux.HandleFunc("/chroma.css", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/css")
		fmt.Fprint(w, ChromaCSS())
	})
	mux.HandleFunc("/custom.css", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/css")
		w.Header().Set("Cache-Control", "no-cache")
		if b, err := os.ReadFile(s.css); err == nil {
			w.Write(b)
		}
	})
	// theme.js runs before first paint (blocking script in index.html);
	// /api/config gives the same answer to a page that is already open.
	mux.HandleFunc("/theme.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript")
		w.Header().Set("Cache-Control", "no-cache")
		b, _ := json.Marshal(themeInfo())
		fmt.Fprintf(w, "window.__notesviewTheme=%s;", b)
	})
	mux.HandleFunc("/api/folder-colors", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, loadFolderColors()) })
	mux.HandleFunc("/api/folder-color", s.post(s.handleFolderColor))
	mux.HandleFunc("/api/config", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, themeInfo()) })
	mux.HandleFunc("/files/", s.handleFile)
	mux.HandleFunc("/events", s.handleEvents)
	mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		n := len(s.clients)
		s.mu.Unlock()
		writeJSON(w, map[string]any{"viewers": n, "dir": s.store.Root, "current": s.current})
	})
	mux.HandleFunc("/api/tree", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, s.store.Tree()) })
	mux.HandleFunc("/api/tasks", s.handleTasks)
	mux.HandleFunc("/api/activity", func(w http.ResponseWriter, r *http.Request) {
		now := time.Now()
		a := s.store.FullActivity(now, 12)
		writeJSON(w, a)
	})
	mux.HandleFunc("/api/folders", func(w http.ResponseWriter, r *http.Request) {
		list := s.store.Folders().List
		if list == nil {
			list = []Folder{}
		}
		writeJSON(w, list)
	})
	mux.HandleFunc("/api/folder", s.handleFolder)
	mux.HandleFunc("/api/search", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, s.store.Search(r.URL.Query().Get("q")))
	})
	mux.HandleFunc("/api/today", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]string{"path": "daily/" + time.Now().Format("2006-01-02") + ".md"})
	})
	mux.HandleFunc("/api/note", s.handleNote)
	mux.HandleFunc("/api/daily", s.post(s.handleDaily))
	mux.HandleFunc("/api/toggle", s.post(s.handleToggle))
	mux.HandleFunc("/api/show", s.post(s.handleShow))
	mux.HandleFunc("/api/scroll", s.post(s.handleScroll))
	mux.HandleFunc("/api/openurl", s.post(s.handleOpenURL))
	return s.guardHost(mux)
}

func themeInfo() map[string]string {
	c, _ := LoadConfig()
	palette, mode := c.Resolved()
	return map[string]string{"palette": palette, "mode": mode}
}

func noCache(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		h.ServeHTTP(w, r)
	})
}

// guardHost rejects requests whose Host header is not loopback, which blocks
// DNS-rebinding attacks against the local server.
func (s *Server) guardHost(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.Host)
		if err != nil {
			host = r.Host
		}
		if host != "127.0.0.1" && host != "localhost" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		h.ServeHTTP(w, r)
	})
}

// post wraps a state-changing handler: POST only, and a custom header that
// cross-site pages cannot send without a CORS preflight (which we never grant).
func (s *Server) post(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("X-Notesview") != "1" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		h(w, r)
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache")
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) handleFile(w http.ResponseWriter, r *http.Request) {
	rel := strings.TrimPrefix(r.URL.Path, "/files/")
	full, err := s.store.Resolve(rel)
	if err != nil {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	http.ServeFile(w, r, full)
}

func (s *Server) handleNote(w http.ResponseWriter, r *http.Request) {
	rel := r.URL.Query().Get("path")
	src, err := s.store.Read(rel)
	if err != nil {
		code := http.StatusNotFound
		if err == ErrOutside {
			code = http.StatusForbidden
		}
		http.Error(w, err.Error(), code)
		return
	}
	body, err := NewMarkdown(s.store, rel).Render(src)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, map[string]any{
		"path": rel, "title": Title(rel, src), "html": body, "backlinks": s.store.Backlinks(rel),
	})
}

// handleTasks returns open tasks with date groups and inline-rendered text.
func (s *Server) handleTasks(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	tasks := s.store.CollectTasks(TaskQuery{Now: now})
	s.renderTasks(tasks)
	writeJSON(w, map[string]any{"today": now.Format(isoDate), "groups": GroupOrder, "tasks": tasks})
}

func (s *Server) renderTasks(tasks []Task) {
	md := NewMarkdown(s.store, "")
	for i := range tasks {
		tasks[i].HTML = md.RenderInline(tasks[i].File, tasks[i].Display)
	}
}

// handleFolder returns the generated page for a category or topic folder.
func (s *Server) handleFolder(w http.ResponseWriter, r *http.Request) {
	f, ok := s.store.Folders().Resolve(r.URL.Query().Get("path"))
	if !ok {
		http.Error(w, "no such folder", http.StatusNotFound)
		return
	}
	now := time.Now()
	page := s.store.FolderPage(f, now)
	s.renderTasks(page.Tasks)
	writeJSON(w, map[string]any{"today": now.Format(isoDate), "folder": page})
}

// handleDaily creates today's daily note (with carry-over) if needed.
func (s *Server) handleDaily(w http.ResponseWriter, r *http.Request) {
	res, err := s.store.EnsureDaily(time.Now(), true)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, res)
}

type toggleReq struct {
	Path string `json:"path"`
	Line int    `json:"line"`
}

func (s *Server) handleToggle(w http.ResponseWriter, r *http.Request) {
	var req toggleReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", 400)
		return
	}
	if err := s.store.ToggleCheckbox(req.Path, req.Line); err != nil {
		code := 400
		if err == ErrOutside {
			code = http.StatusForbidden
		}
		http.Error(w, err.Error(), code)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

// handleShow tells viewers to display a note. The response says whether the
// caller should open a viewer window (none connected and none launched lately).
func (s *Server) handleShow(w http.ResponseWriter, r *http.Request) {
	var req showMsg
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", 400)
		return
	}
	rel := req.Path
	if rel != tasksPath {
		var err error
		if rel, err = s.store.Rel(req.Path); err != nil {
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}
	}
	req.Path = rel
	s.mu.Lock()
	s.current = req
	launch := !s.hasAppClient() && time.Since(s.lastLaunch) > 8*time.Second
	if launch {
		s.lastLaunch = time.Now()
	}
	s.mu.Unlock()
	b, _ := json.Marshal(req)
	s.broadcast(event{"show", string(b)})
	writeJSON(w, map[string]any{"launch": launch, "path": rel})
}

func (s *Server) handleScroll(w http.ResponseWriter, r *http.Request) {
	var req showMsg
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", 400)
		return
	}
	if rel, err := s.store.Rel(req.Path); err == nil {
		req.Path = rel
	}
	b, _ := json.Marshal(req)
	s.broadcast(event{"scroll", string(b)})
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) handleOpenURL(w http.ResponseWriter, r *http.Request) {
	var req struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", 400)
		return
	}
	u, err := url.Parse(req.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https" && u.Scheme != "mailto") {
		http.Error(w, "unsupported url", 400)
		return
	}
	openDefault(u.String())
	writeJSON(w, map[string]bool{"ok": true})
}

// hasAppClient reports whether a viewer window we launched is connected; stray
// tabs (an old browser tab, a stale connection) don't count. Caller holds s.mu.
func (s *Server) hasAppClient() bool {
	for _, app := range s.clients {
		if app {
			return true
		}
	}
	return false
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", 500)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	ch := make(chan event, 16)
	s.mu.Lock()
	s.clients[ch] = r.URL.Query().Get("app") == "1"
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.clients, ch)
		s.mu.Unlock()
	}()
	fmt.Fprint(w, ": connected\n\n")
	fl.Flush()
	tick := time.NewTicker(30 * time.Second)
	defer tick.Stop()
	for {
		select {
		case e := <-ch:
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", e.Name, e.Data)
			fl.Flush()
		case <-tick.C:
			fmt.Fprint(w, ": ping\n\n")
			fl.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

// Watch pushes a "change" event whenever files under the notes folder or
// custom.css changes. The event says whether the folder structure changed
// (tree) or custom.css did (css). Stop with the
// returned function.
func (s *Server) Watch() (func(), error) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	addAll := func(root string) {
		filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
			if err == nil && d.IsDir() {
				if p != s.store.Root && strings.HasPrefix(d.Name(), ".") {
					return filepath.SkipDir
				}
				_ = w.Add(p)
			}
			return nil
		})
	}
	// resync drops watches on folders that are gone (renamed or removed) and
	// adds any folder that is new, so the watch list always matches the disk.
	resync := func() {
		for _, p := range w.WatchList() {
			if !within(s.store.Root, p) {
				continue
			}
			if _, err := os.Stat(p); err != nil {
				_ = w.Remove(p)
			}
		}
		addAll(s.store.Root)
	}
	addAll(s.store.Root)
	// custom.css is usually a symlink into the repo: watch both folders.
	cfgDirs := map[string]bool{filepath.Dir(s.css): true}
	if real, err := filepath.EvalSymlinks(s.css); err == nil {
		cfgDirs[filepath.Dir(real)] = true
	}
	for d := range cfgDirs {
		if !within(s.store.Root, d) {
			_ = w.Add(d)
		}
	}
	isConfigFile := func(p string) bool {
		b := filepath.Base(p)
		return cfgDirs[filepath.Dir(p)] && (b == filepath.Base(s.css) || b == "config.json" || b == "folder-colors.json")
	}
	done := make(chan struct{})
	go func() {
		var fire <-chan time.Time
		tree, cfgChanged := false, false
		for {
			select {
			case <-done:
				return
			case ev, ok := <-w.Events:
				if !ok {
					return
				}
				if !within(s.store.Root, ev.Name) {
					if !isConfigFile(ev.Name) {
						continue
					}
					cfgChanged = true
				} else {
					if strings.HasPrefix(filepath.Base(ev.Name), ".") {
						continue
					}
					if ev.Has(fsnotify.Create) || ev.Has(fsnotify.Remove) || ev.Has(fsnotify.Rename) {
						tree = true
					}
				}
				fire = time.After(80 * time.Millisecond)
			case <-fire:
				fire = nil
				if tree {
					resync()
				}
				s.broadcast(event{"change", fmt.Sprintf(`{"tree":%t,"css":%t}`, tree, cfgChanged)})
				tree, cfgChanged = false, false
			case err, ok := <-w.Errors:
				if !ok {
					return
				}
				log.Println("watch:", err)
			}
		}
	}()
	return func() { close(done); w.Close() }, nil
}

func (s *Server) ListenAndServe() error {
	if _, err := s.Watch(); err != nil {
		log.Println("file watching disabled:", err)
	}
	go func() { // keep the score graph filled in even when nothing is open
		for {
			s.store.FullActivity(time.Now(), 1)
			time.Sleep(scoreRecordEvery)
		}
	}()
	addr := fmt.Sprintf("127.0.0.1:%d", s.port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	log.Printf("notesview serving %s on http://%s", s.store.Root, addr)
	return (&http.Server{Handler: s.Handler()}).Serve(ln)
}

// ── Opening windows ──────────────────────────────────────────────

func openDefault(target string) {
	name := "xdg-open"
	if runtime.GOOS == "darwin" {
		name = "open"
	}
	_ = exec.Command(name, target).Start()
}

func findAppBrowser() string {
	if b := os.Getenv("NOTESVIEW_BROWSER"); b != "" {
		return b
	}
	for _, n := range []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser",
		"brave-browser", "brave", "microsoft-edge", "microsoft-edge-stable", "msedge"} {
		if p, err := exec.LookPath(n); err == nil {
			return p
		}
	}
	if runtime.GOOS == "darwin" {
		for _, p := range []string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
			"/Applications/Brave Browser.app/Contents/MacOS/Brave Browser",
			"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
		} {
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
	}
	return ""
}

// openViewerWindow opens a chrome-less app window if a Chromium-family
// browser exists, otherwise the default browser.
func openViewerWindow(target string) {
	if b := findAppBrowser(); b != "" {
		cmd := exec.Command(b, "--app="+target)
		if cmd.Start() == nil {
			go cmd.Wait()
			return
		}
	}
	openDefault(target)
}
