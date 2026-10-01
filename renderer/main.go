// notesview: a self-contained markdown viewer for a folder of notes.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// tasksPath is the pseudo-path that selects the Tasks view.
const tasksPath = "@tasks"

func defaultDir() string {
	if d := os.Getenv("NOTES_DIR"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "notes")
}

func defaultPort() int {
	if p, err := strconv.Atoi(os.Getenv("NOTESVIEW_PORT")); err == nil && p > 0 {
		return p
	}
	return 7777
}

// version is the notesview release. Release builds override it with
// -ldflags "-X main.version=…". Bump it whenever the Neovim config starts
// relying on something new (see NOTESVIEW_MIN_VERSION in nvim/init.lua).
var version = "0.2.0"

func usage() {
	fmt.Fprintln(os.Stderr, `usage:
  notesview serve   [--dir DIR] [--port N]
  notesview open    [PATH | --tasks] [--line N] [--dir DIR] [--port N]
  notesview scroll  PATH --line N [--port N]
  notesview tasks   [--json] [--all] [--dir DIR]       list open tasks
  notesview date    TEXT…                              convert @tomorrow, @fri, @oct6, @10/6, @+3d
  notesview capture [--dir DIR] TEXT…                  add "- [ ] TEXT" to inbox.md
  notesview daily   [--date YYYY-MM-DD] [--dir DIR]    create today's daily note (with carry-over)
  notesview lecture CLASS [--dir DIR]                  create today's lecture note for a class
  notesview classes [--json]                           list classes from config.toml
  notesview doctor                                     check the installation
  notesview --version`)
	os.Exit(2)
}

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	cmd, args := os.Args[1], os.Args[2:]
	switch cmd {
	case "--version", "-version", "-v", "version":
		fmt.Println("notesview", version)
		return
	case "-h", "--help", "help":
		usage()
	case "tasks", "date", "capture", "daily", "lecture", "classes", "doctor":
		os.Exit(runCommand(cmd, args, os.Stdout, os.Stderr))
	}
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	dir := fs.String("dir", defaultDir(), "notes folder")
	port := fs.Int("port", defaultPort(), "port (127.0.0.1 only)")
	line := fs.Int("line", 0, "source line to show")
	tasks := fs.Bool("tasks", false, "show the Tasks view")
	// allow the positional PATH before or after flags
	var pos []string
	for len(args) > 0 {
		_ = fs.Parse(args)
		args = fs.Args()
		if len(args) > 0 {
			pos = append(pos, args[0])
			args = args[1:]
		}
	}
	switch cmd {
	case "serve":
		store, err := NewStore(*dir)
		if err != nil {
			fatal(err)
		}
		fatal(NewServer(store, *port).ListenAndServe())
	case "open", "scroll":
		path := ""
		if len(pos) > 0 {
			path = pos[0]
		}
		if *tasks {
			path = tasksPath
		}
		fatal(runClient(cmd, path, *line, *dir, *port))
	default:
		usage()
	}
}

// runCommand runs the note commands that work on files directly (no
// server needed). Flags come first; everything after them (or after "--")
// is the text, taken verbatim. It returns the exit code.
func runCommand(cmd string, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir := fs.String("dir", defaultDir(), "notes folder")
	asJSON := fs.Bool("json", false, "print JSON")
	all := fs.Bool("all", false, "include done and moved tasks")
	dateFlag := fs.String("date", "", "date (YYYY-MM-DD), default today")
	now := time.Now()
	if cmd == "date" { // no flags: the text may well start with "- [ ]"
		if len(args) > 0 && args[0] == "--" {
			args = args[1:]
		}
		fmt.Fprintln(stdout, ConvertNaturalDates(strings.Join(args, " "), now))
		return 0
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	text := strings.Join(fs.Args(), " ")
	fail := func(err error) int {
		fmt.Fprintln(stderr, "notesview:", err)
		return 1
	}
	if cmd == "doctor" {
		return runDoctor(stdout)
	}
	cfg, err := LoadConfig(ConfigPath())
	if err != nil {
		fmt.Fprintln(stderr, "notesview: warning:", err)
		cfg = &Config{}
	}
	if cmd == "classes" {
		if *asJSON {
			return printJSON(stdout, cfg.Classes)
		}
		for _, c := range cfg.Classes {
			fmt.Fprintf(stdout, "%-10s %-12s classes/%s\n", c.ID, c.Name, c.Folder)
		}
		return 0
	}
	store, err := NewStore(*dir)
	if err != nil {
		return fail(err)
	}
	switch cmd {
	case "tasks":
		tasks := store.CollectTasks(cfg, TaskQuery{All: *all, Now: now})
		if *asJSON {
			return printJSON(stdout, tasks)
		}
		for _, t := range tasks {
			label := t.Kind
			if t.ClassName != "" {
				label = t.ClassName
			}
			due := ""
			if t.Due != "" {
				due = " (due " + t.Due + ")"
			}
			fmt.Fprintf(stdout, "%s:%d: [%s/%s] %s%s\n", t.File, t.Line, label, t.Group, t.Display, due)
		}
	case "capture":
		line, err := store.Capture(text, now)
		if err != nil {
			return fail(err)
		}
		if *asJSON {
			return printJSON(stdout, map[string]string{"line": line, "path": filepath.Join(store.Root, "inbox.md")})
		}
		fmt.Fprintln(stdout, "Captured:", strings.TrimPrefix(line, "- [ ] "))
	case "daily":
		date := now
		if *dateFlag != "" {
			if date, err = time.ParseInLocation(isoDate, *dateFlag, time.Local); err != nil {
				return fail(fmt.Errorf("--date: %w", err))
			}
		}
		// carry-over only happens when today's note is created
		res, err := store.EnsureDaily(cfg, date, date.Format(isoDate) == now.Format(isoDate))
		if err != nil {
			return fail(err)
		}
		if res.Warning != "" {
			fmt.Fprintln(stderr, "notesview: warning:", res.Warning)
		}
		if *asJSON {
			return printJSON(stdout, res)
		}
		fmt.Fprintln(stdout, filepath.Join(store.Root, filepath.FromSlash(res.Path)))
	case "lecture":
		c, ok := cfg.ClassByID(strings.TrimSpace(text))
		if !ok {
			return fail(fmt.Errorf("unknown class %q (classes are listed in %s)", text, ConfigPath()))
		}
		rel, err := store.EnsureLecture(c, now)
		if err != nil {
			return fail(err)
		}
		fmt.Fprintln(stdout, filepath.Join(store.Root, filepath.FromSlash(rel)))
	}
	return 0
}

func printJSON(w io.Writer, v any) int {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return 1
	}
	return 0
}

func fatal(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "notesview:", err)
		os.Exit(1)
	}
}

func baseURL(port int) string { return fmt.Sprintf("http://127.0.0.1:%d", port) }

func post(port int, endpoint string, body any) (map[string]any, error) {
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", baseURL(port)+endpoint, bytes.NewReader(b))
	req.Header.Set("X-Notesview", "1")
	req.Header.Set("Content-Type", "application/json")
	c := &http.Client{Timeout: 3 * time.Second}
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("server said %s", resp.Status)
	}
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return out, nil
}

func serverUp(port int) bool {
	c := &http.Client{Timeout: 500 * time.Millisecond}
	resp, err := c.Get(baseURL(port) + "/api/status")
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == 200
}

func ensureServer(dir string, port int) error {
	if serverUp(port) {
		return nil
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	logf, _ := os.OpenFile(filepath.Join(os.TempDir(), "notesview.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	c := exec.Command(self, "serve", "--dir", dir, "--port", strconv.Itoa(port))
	c.Stdout, c.Stderr = logf, logf
	c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := c.Start(); err != nil {
		return err
	}
	go c.Wait()
	for i := 0; i < 40; i++ {
		if serverUp(port) {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("server did not start (see %s)", filepath.Join(os.TempDir(), "notesview.log"))
}

func runClient(cmd, path string, line int, dir string, port int) error {
	if err := ensureServer(dir, port); err != nil {
		return err
	}
	if cmd == "scroll" {
		_, err := post(port, "/api/scroll", showMsg{Path: path, Line: line})
		return err
	}
	if path != "" {
		// the running server may use a different folder than --dir; hand it
		// an absolute path and let it decide.
		if abs, err := filepath.Abs(path); err == nil && !strings.HasPrefix(path, "/") {
			if _, serr := os.Stat(abs); serr == nil {
				path = abs
			}
		}
	}
	resp, err := post(port, "/api/show", showMsg{Path: path, Line: line})
	if err != nil {
		return err
	}
	if launch, _ := resp["launch"].(bool); launch {
		target := baseURL(port) + "/"
		if p, ok := resp["path"].(string); ok && p != "" {
			if p == tasksPath {
				target += "#/tasks"
			} else {
				target += "#/note/" + (&url.URL{Path: p}).EscapedPath()
			}
		}
		openViewerWindow(target)
	}
	return nil
}
