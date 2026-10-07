package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Settings page API (web/settings.js): appearance (themes per light/dark, font, transparency,
// Ghostty sync), morning routine items, new-word folders, and the About box with rebuild/restart.
// Calendars, folder colours and books use their own endpoints.

var serverStarted = time.Now()

func (s *Server) settingsRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/settings", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, s.settingsInfo()) })
	mux.HandleFunc("/api/settings/appearance", s.post(s.handleAppearance))
	mux.HandleFunc("/api/routine/items", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			writeJSON(w, s.store.RoutineItems())
			return
		}
		s.post(s.handleRoutineItems)(w, r)
	})
	mux.HandleFunc("/api/words", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			writeJSON(w, s.wordsInfo())
			return
		}
		s.post(s.handleWords)(w, r)
	})
	mux.HandleFunc("/api/rebuild", s.post(s.handleRebuild))
}

// settingsInfo is everything the Settings page shows, apart from calendars, colours and books.
func (s *Server) settingsInfo() map[string]any {
	c, warn := LoadConfig()
	type themeOpt struct {
		Name string `json:"name"`
		Desc string `json:"desc"`
		Only string `json:"only,omitempty"`
	}
	var ts []themeOpt
	for _, t := range themes {
		ts = append(ts, themeOpt{t.Name, t.Desc, t.Only})
	}
	var fs []map[string]string
	for _, f := range fonts {
		fs = append(fs, map[string]string{"name": f.Name, "desc": f.Desc})
	}
	sw := make([]string, 0, len(swatches))
	for k := range swatches {
		sw = append(sw, k)
	}
	sort.Strings(sw)
	src, goBin := rendererSource(), goBinary()
	s.mu.Lock()
	viewers := len(s.clients)
	s.mu.Unlock()
	return map[string]any{
		"config": map[string]any{
			"theme_light": c.ThemeLight, "theme_dark": c.ThemeDark, "appearance": c.Appearance, "font": c.Font,
			"opacity": c.Opacity, "sync_ghostty": c.GhosttySync(),
		},
		"warning": warn, "themes": ts, "fonts": fs, "swatches": sw,
		"ghostty": map[string]any{"path": ghosttyConfigPath(), "opacity": ghosttyOpacity(), "theme": ghosttyThemeLine(c)},
		"about": map[string]any{
			"version": version, "dir": s.store.Root, "port": s.port, "viewers": viewers, "config": configPath(),
			"started": serverStarted.Format(time.RFC3339), "source": src, "go": goBin, "can_rebuild": src != "" && goBin != "",
		},
	}
}

// ghosttyOpacity is Ghostty's background-opacity (what the native app uses without its own), 0.9 if unset.
func ghosttyOpacity() float64 {
	if p := ghosttyConfigPath(); p != "" {
		if b, err := os.ReadFile(p); err == nil {
			for _, l := range strings.Split(string(b), "\n") {
				if k, v, ok := strings.Cut(l, "="); ok && strings.TrimSpace(k) == "background-opacity" {
					if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
						return min(1, max(0.05, f))
					}
				}
			}
		}
	}
	return 0.9
}

// reloadGhostty sends SIGUSR2 (reload the config) to running Ghostty processes. pgrep can't
// see Ghostty on this macOS (its process name is a truncated path), so it looks it up with ps.
func reloadGhostty() {
	out, err := exec.Command("ps", "-axo", "pid=,command=").Output()
	if err != nil {
		return
	}
	for _, l := range strings.Split(string(out), "\n") {
		f := strings.Fields(l)
		if len(f) >= 2 && strings.HasSuffix(f[1], "Ghostty.app/Contents/MacOS/ghostty") {
			_ = exec.Command("kill", "-USR2", f[0]).Run()
		}
	}
}

// AppearancePatch changes config.json; nil fields are left alone.
type AppearancePatch struct {
	ThemeLight  *string  `json:"theme_light"`
	ThemeDark   *string  `json:"theme_dark"`
	Appearance  *string  `json:"appearance"`
	Font        *string  `json:"font"`
	Opacity     *float64 `json:"opacity"` // 0 = follow Ghostty
	SyncGhostty *bool    `json:"sync_ghostty"`
}

// ApplyAppearance validates and saves a patch; it reports whether the themes changed.
func ApplyAppearance(p AppearancePatch) (Config, bool, error) {
	c, _ := LoadConfig()
	themesBefore := c.ThemeLight + "|" + c.ThemeDark
	slot := func(dst *string, v *string) error {
		if v == nil {
			return nil
		}
		t, ok := findTheme(*v)
		if !ok {
			return fmt.Errorf("unknown theme %q", *v)
		}
		*dst = t.Name
		return nil
	}
	if err := slot(&c.ThemeLight, p.ThemeLight); err != nil {
		return c, false, err
	}
	if err := slot(&c.ThemeDark, p.ThemeDark); err != nil {
		return c, false, err
	}
	if p.Appearance != nil {
		switch a := strings.ToLower(*p.Appearance); a {
		case "auto", "light", "dark":
			c.Appearance = a
		default:
			return c, false, fmt.Errorf("appearance must be auto, light or dark")
		}
	}
	if p.Font != nil {
		c.Font = strings.TrimSpace(*p.Font)
		if fontKey(c.Font) == "default" {
			c.Font = ""
		}
	}
	if p.Opacity != nil {
		c.Opacity = 0
		if *p.Opacity > 0 {
			c.Opacity = min(1, max(0.2, *p.Opacity))
		}
	}
	if p.SyncGhostty != nil {
		v := *p.SyncGhostty
		c.SyncGhostty = &v
	}
	if err := SaveConfig(c); err != nil {
		return c, false, err
	}
	return c, themesBefore != c.ThemeLight+"|"+c.ThemeDark, nil
}

func (s *Server) handleAppearance(w http.ResponseWriter, r *http.Request) {
	var p AppearancePatch
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<14)).Decode(&p) != nil {
		http.Error(w, "bad request", 400)
		return
	}
	c, changed, err := ApplyAppearance(p)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	ghostty := ""
	// turning the sync on, or changing a theme with it on, brings Ghostty in line
	if c.GhosttySync() && (changed || (p.SyncGhostty != nil && *p.SyncGhostty)) {
		if ok, err := SyncGhosttyTheme(c, ghosttyConfigPath()); err != nil {
			ghostty = "Ghostty: " + err.Error()
		} else if ok {
			ghostty = ghosttyThemeLine(c)
		}
	}
	writeJSON(w, map[string]any{"ok": true, "ghostty": ghostty})
}

// ── morning routine items ──

var routineIDRe = regexp.MustCompile(`[^a-z0-9]+`)

// SetRoutineItems saves the routine's items. Items without an id get one from their label;
// labels are required; ids stay unique; at most one item is the forecast.
func (s *Store) SetRoutineItems(items []RoutineItem) ([]RoutineItem, error) {
	routineMu.Lock()
	defer routineMu.Unlock()
	seen, forecast := map[string]bool{}, false
	var out []RoutineItem
	for _, it := range items {
		it.Label = strings.TrimSpace(it.Label)
		if it.Label == "" {
			continue
		}
		if it.Kind != routineForecast {
			it.Kind = ""
		} else if forecast {
			return nil, fmt.Errorf("%w: only one forecast item", ErrRoutine)
		} else {
			forecast = true
		}
		id := strings.Trim(routineIDRe.ReplaceAllString(strings.ToLower(strings.TrimSpace(it.ID)), "-"), "-")
		if id == "" {
			id = strings.Trim(routineIDRe.ReplaceAllString(strings.ToLower(it.Label), "-"), "-")
		}
		if id == "" {
			id = "item"
		}
		base := id
		for n := 2; seen[id]; n++ {
			id = fmt.Sprintf("%s-%d", base, n)
		}
		seen[id] = true
		it.ID = id
		out = append(out, it)
	}
	if out == nil {
		out = []RoutineItem{}
	}
	if err := os.MkdirAll(s.routinePath(""), 0o755); err != nil {
		return nil, err
	}
	b, _ := json.MarshalIndent(map[string]any{"items": out}, "", "  ")
	return out, writeAtomic(s.routinePath(routineConfig), append(b, '\n'))
}

func (s *Server) handleRoutineItems(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Items []RoutineItem `json:"items"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req) != nil {
		http.Error(w, "bad request", 400)
		return
	}
	items, err := s.store.SetRoutineItems(req.Items)
	if err != nil {
		code := 500
		if errors.Is(err, ErrRoutine) {
			code = 400
		}
		http.Error(w, err.Error(), code)
		return
	}
	writeJSON(w, items)
}

// ── new-word folders ──

// wordsInfo is the tracked and excluded lists plus what could be tracked: top-level folders and notes.
func (s *Server) wordsInfo() map[string]any {
	var cand []string
	if ents, err := os.ReadDir(s.store.Root); err == nil {
		for _, e := range ents {
			n := e.Name()
			if strings.HasPrefix(n, ".") || n == "assets" || n == "__pycache__" {
				continue
			}
			if e.IsDir() || strings.HasSuffix(n, ".md") {
				cand = append(cand, n)
			}
		}
	}
	sort.Strings(cand)
	return map[string]any{"tracked": nonNilStrings(s.store.TrackedWords()), "excluded": nonNilStrings(s.store.ExcludedWords()),
		"candidates": nonNilStrings(cand), "per_point": scoreWordsPer}
}

func nonNilStrings(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

func (s *Server) handleWords(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Tracked  *[]string `json:"tracked"`
		Excluded *[]string `json:"excluded"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req) != nil {
		http.Error(w, "bad request", 400)
		return
	}
	now := time.Now()
	s.store.RecordWords(now) // credit writing under the current lists before they change
	if req.Tracked != nil {
		if err := s.store.SetTrackedWords(cleanWordsPaths(*req.Tracked)); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
	}
	if req.Excluded != nil {
		if err := s.store.SetExcludedWords(cleanWordsPaths(*req.Excluded)); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
	}
	s.store.RecordWords(now) // baseline newly tracked folders: only later words count
	writeJSON(w, s.wordsInfo())
}

// ── rebuild and restart ──

// rendererSource is the renderer source folder: ~/.config/nvim links into the nvim-notes repo,
// whose renderer/ is next to it (NOTESVIEW_SRC overrides). "" when not found.
func rendererSource() string {
	if p := os.Getenv("NOTESVIEW_SRC"); p != "" {
		return p
	}
	if nv, err := filepath.EvalSymlinks(filepath.Join(configDir(), "..", "nvim")); err == nil {
		p := filepath.Join(filepath.Dir(nv), "renderer")
		if _, err := os.Stat(filepath.Join(p, "go.mod")); err == nil {
			return p
		}
	}
	return ""
}

// goBinary finds go: on PATH, or Homebrew's and the official installer's places (launchd's PATH is short).
func goBinary() string {
	if p, err := exec.LookPath("go"); err == nil {
		return p
	}
	for _, p := range []string{"/opt/homebrew/bin/go", "/usr/local/go/bin/go", "/usr/local/bin/go"} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// handleRebuild rebuilds notesview from source into the running binary's place (when "build"
// is set), then restarts the server by exec-ing the binary with the same arguments (same pid,
// so launchd's local.notesview-serve keeps tracking it, and it also works without launchd).
func (s *Server) handleRebuild(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Build bool `json:"build"`
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&req)
	if req.Build {
		src, goBin := rendererSource(), goBinary()
		if src == "" || goBin == "" {
			http.Error(w, "can't rebuild: renderer source or go not found", 500)
			return
		}
		exe, err := os.Executable()
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		if real, err := filepath.EvalSymlinks(exe); err == nil {
			exe = real
		}
		cmd := exec.Command(goBin, "build", "-o", exe+".new", ".")
		cmd.Dir = src
		if out, err := cmd.CombinedOutput(); err != nil {
			http.Error(w, "build failed:\n"+string(out), 500)
			return
		}
		// a stable signature keeps Full Disk Access (Screen Time, Messages importers) across rebuilds
		_ = exec.Command("codesign", "--force", "--sign", "NotesView Signing", "--identifier", "local.notesview", exe+".new").Run()
		if err := os.Rename(exe+".new", exe); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
	}
	exe, err := os.Executable()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	writeJSON(w, map[string]any{"ok": true, "restarting": true})
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	go func() {
		time.Sleep(300 * time.Millisecond)
		if err := syscall.Exec(exe, os.Args, os.Environ()); err != nil {
			os.Exit(1) // launchd (KeepAlive) brings it back
		}
	}()
}
