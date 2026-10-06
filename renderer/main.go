// notesview: a self-contained markdown viewer for a folder of notes.
package main

import (
	"bufio"
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
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// tasksPath is the pseudo-path that selects the Tasks view.
const tasksPath = "@tasks"

// activityPath is the pseudo-path that selects the Activity view.
const activityPath = "@activity"

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
var version = "0.5.0"

func usage() {
	fmt.Fprintln(os.Stderr, `usage:
  notesview serve    [--dir DIR] [--port N]
  notesview open     [PATH | --tasks] [--line N] [--dir DIR] [--port N]
  notesview scroll   PATH --line N [--port N]
  notesview tasks    [--json] [--all] [--dir DIR]      list open tasks
  notesview date     TEXT…                             convert @tomorrow, @fri, @oct6, @10/6, @+3d in TEXT
  notesview due      [--json] WHEN                     resolve one due date ("fri" → 2026-10-02, Fri Oct 2)
  notesview capture  [-i] [--folder TAG] [--due WHEN] [--difficulty 1-3] [--dir DIR] [TEXT…]
                                                       add "- [ ] TEXT" to inbox.md (-i asks step by step;
                                                       --parse only reports what TEXT already answers)
  notesview folders  [--json] [--dir DIR]              list category and topic folders
  notesview resolve  [--json] [--dir DIR] TARGET       what a [[TARGET]] link points to
  notesview daily    [--date YYYY-MM-DD] [--dir DIR]   create a daily note (today's with carry-over)
  notesview words    [track|untrack DIR…] [--dir DIR]  list, add or remove the folders whose new words count
                                                       (DIR is relative to the notes folder, or one note)
  notesview words    [exclude|include PATH…]           skip (or count again) a note or folder inside a tracked one
  notesview calendar [list|import|remove|next|today|checkin|attendance]
                                                       .ics calendars and class check-in:
                                                       import [--name N] [--not-class] FILE.ics…, next [--json],
                                                       checkin [ID], today, attendance, remove ID
  notesview doctor                                     check the installation
  notesview themes                                     list themes (* = current)
  notesview theme    NAME                              choose a theme (writes config.json)
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
	case "tasks", "date", "due", "capture", "folders", "resolve", "daily", "doctor", "themes", "theme", "fonts", "font", "words", "calendar":
		os.Exit(runCommand(cmd, args, os.Stdout, os.Stderr))
	}
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	dir := fs.String("dir", defaultDir(), "notes folder")
	port := fs.Int("port", defaultPort(), "port (127.0.0.1 only)")
	line := fs.Int("line", 0, "source line to show")
	tasks := fs.Bool("tasks", false, "show the Tasks view")
	activity := fs.Bool("activity", false, "show the Activity view")
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
		if *activity {
			path = activityPath
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
	return runCommandIO(cmd, args, os.Stdin, stdout, stderr)
}

func runCommandIO(cmd string, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir := fs.String("dir", defaultDir(), "notes folder")
	asJSON := fs.Bool("json", false, "print JSON")
	all := fs.Bool("all", false, "include done and moved tasks")
	dateFlag := fs.String("date", "", "date (YYYY-MM-DD), default today")
	folder := fs.String("folder", "", "folder tag or path for the captured task")
	due := fs.String("due", "", "due date for the captured task (fri, oct6, +3d, …)")
	diff := fs.Int("difficulty", 0, "difficulty for the captured task (1-3, 3 hardest)")
	calName := fs.String("name", "", "calendar import: name of the calendar (default: the file name)")
	notClass := fs.Bool("not-class", false, "calendar import: the events are not classes (no check-in)")
	interactive := fs.Bool("i", false, "capture step by step")
	parse := fs.Bool("parse", false, "capture: only report which steps the text already answers (JSON)")
	now := time.Now()
	fail := func(err error) int {
		fmt.Fprintln(stderr, "notesview:", err)
		return 1
	}
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
	switch cmd {
	case "doctor":
		return runDoctor(stdout)
	case "themes":
		cur, warn := LoadConfig()
		for _, t := range themes {
			mark := " "
			if t.Name == cur.Theme {
				mark = "*"
			}
			fmt.Fprintf(stdout, "%s %-12s %s\n", mark, t.Name, t.Desc)
		}
		if warn != "" {
			fmt.Fprintln(stderr, "warning:", warn)
		}
		return 0
	case "fonts":
		cur, _ := LoadConfig()
		curKey := fontKey(cur.Font)
		if curKey == "" {
			curKey = "default"
		}
		custom := true
		for _, f := range fonts {
			mark := " "
			if f.Name == curKey {
				mark, custom = "*", false
			}
			fmt.Fprintf(stdout, "%s %-15s %s\n", mark, f.Name, f.Desc)
		}
		if custom {
			fmt.Fprintf(stdout, "* %-15s custom (%s)\n", cur.Font, fontStack(cur.Font))
		}
		fmt.Fprintln(stdout, "\nAny installed family name also works: notesview font \"Gill Sans\"")
		return 0
	case "font":
		cur, _ := LoadConfig()
		cur.Font = text
		if fontKey(text) == "default" {
			cur.Font = ""
		}
		b, _ := json.MarshalIndent(cur, "", "  ")
		if err := os.MkdirAll(configDir(), 0o755); err != nil {
			return fail(err)
		}
		if err := os.WriteFile(configPath(), append(b, '\n'), 0o644); err != nil {
			return fail(err)
		}
		fmt.Fprintf(stdout, "font set to %s (%s)\n", map[bool]string{true: "default", false: text}[cur.Font == ""], configPath())
		return 0
	case "theme":
		t, ok := findTheme(text)
		if !ok {
			return fail(fmt.Errorf("unknown theme %q (available: %s)", text, themeNames()))
		}
		cur, _ := LoadConfig()
		cur.Theme = t.Name
		b, _ := json.MarshalIndent(cur, "", "  ")
		if err := os.MkdirAll(configDir(), 0o755); err != nil {
			return fail(err)
		}
		if err := os.WriteFile(configPath(), append(b, '\n'), 0o644); err != nil {
			return fail(err)
		}
		fmt.Fprintf(stdout, "theme set to %s (%s)\n", t.Name, configPath())
		return 0
	case "due":
		d, ok := ParseDue(text, now)
		if !ok {
			if *asJSON {
				printJSON(stdout, map[string]any{"ok": false, "input": text})
			}
			return fail(fmt.Errorf("can't read due date %q (try fri, tomorrow, oct6, 10/6, +3d)", text))
		}
		if *asJSON {
			return printJSON(stdout, map[string]any{"ok": true, "date": d.Format(isoDate), "label": DueLabel(d)})
		}
		fmt.Fprintf(stdout, "%s\t%s\n", d.Format(isoDate), DueLabel(d))
		return 0
	}
	store, err := NewStore(*dir)
	if err != nil {
		return fail(err)
	}
	switch cmd {
	case "tasks":
		tasks := store.CollectTasks(TaskQuery{All: *all, Now: now, NewestFirst: true})
		if *asJSON {
			return printJSON(stdout, tasks)
		}
		for _, t := range tasks {
			d := ""
			if t.Due != "" {
				d = " (due " + t.Due + ")"
			}
			fmt.Fprintf(stdout, "%s:%d: [%s/%s] %s%s\n", t.File, t.Line, t.Label(), t.Group, t.Display, d)
		}
	case "folders":
		list := store.Folders().List
		if *asJSON {
			if list == nil {
				list = []Folder{}
			}
			return printJSON(stdout, list)
		}
		for _, f := range list {
			fmt.Fprintf(stdout, "#%-30s %s\n", f.Tag, f.Path)
		}
	case "resolve":
		r := store.ResolveLink(text)
		if *asJSON {
			return printJSON(stdout, r)
		}
		fmt.Fprintln(stdout, r.Kind, r.Path)
	case "capture":
		if *parse {
			return printJSON(stdout, ParseCaptureText(text, store.Folders(), now))
		}
		var line string
		if *interactive {
			in := bufio.NewReader(stdin)
			ui := &captureUI{in: in, out: stdout, pick: terminalPicker(in, stdout)}
			line, err = runCaptureInteractive(store, text, ui, now)
			if err == errCancelled {
				fmt.Fprintln(stdout, "Nothing captured.")
				return 1
			}
		} else {
			line, err = store.Capture(CaptureOpts{Text: text, Folder: *folder, Due: *due, Diff: *diff}, now)
		}
		if err != nil {
			return fail(err)
		}
		if *asJSON {
			return printJSON(stdout, map[string]string{"line": line, "path": filepath.Join(store.Root, "inbox.md")})
		}
		fmt.Fprintln(stdout, "Added to inbox:", line)
	case "calendar":
		return runCalendarCommand(store, fs.Args(), *calName, *notClass, *asJSON, stdout, stderr, now)
	case "words":
		tracked, excluded := store.TrackedWords(), store.ExcludedWords()
		if len(fs.Args()) > 0 {
			sub, rest := fs.Args()[0], fs.Args()[1:]
			if (sub != "track" && sub != "untrack" && sub != "exclude" && sub != "include") || len(rest) == 0 {
				return fail(fmt.Errorf("usage: notesview words [track|untrack|exclude|include PATH…]"))
			}
			store.RecordWords(now) // credit writing under the current list before it changes
			if sub == "exclude" || sub == "include" {
				set := map[string]bool{}
				for _, e := range excluded {
					set[e] = true
				}
				for _, d := range rest {
					d = strings.Trim(filepath.ToSlash(filepath.Clean(d)), "/")
					if sub == "exclude" {
						if !inTracked(d, tracked) {
							return fail(fmt.Errorf("%s: not inside a tracked folder or note (tracked: %s)", d, strings.Join(tracked, ", ")))
						}
						if _, err := os.Stat(filepath.Join(store.Root, filepath.FromSlash(d))); err != nil {
							if _, err := os.Stat(filepath.Join(store.Root, filepath.FromSlash(d)+".md")); err != nil {
								return fail(fmt.Errorf("%s: no such folder or note in %s", d, store.Root))
							}
						}
						set[d] = true
					} else {
						delete(set, d)
					}
				}
				excluded = excluded[:0]
				for d := range set {
					excluded = append(excluded, d)
				}
				sort.Strings(excluded)
				if err := store.SetExcludedWords(excluded); err != nil {
					return fail(err)
				}
				store.RecordWords(now) // drop or baseline the affected notes now
				return printWords(stdout, store, tracked, excluded, now, *asJSON)
			}
			set := map[string]bool{}
			for _, t := range tracked {
				set[t] = true
			}
			for _, d := range rest {
				d = strings.Trim(filepath.ToSlash(filepath.Clean(d)), "/")
				if sub == "track" {
					if _, err := os.Stat(filepath.Join(store.Root, filepath.FromSlash(d))); err != nil {
						if _, err := os.Stat(filepath.Join(store.Root, filepath.FromSlash(d)+".md")); err != nil {
							return fail(fmt.Errorf("%s: no such folder or note in %s", d, store.Root))
						}
					}
					set[d] = true
				} else {
					delete(set, d)
				}
			}
			tracked = tracked[:0]
			for d := range set {
				tracked = append(tracked, d)
			}
			sort.Strings(tracked)
			if err := store.SetTrackedWords(tracked); err != nil {
				return fail(err)
			}
			store.RecordWords(now) // baseline newly tracked folders now: only later words count
		}
		return printWords(stdout, store, tracked, excluded, now, *asJSON)
	case "daily":
		date := now
		if *dateFlag != "" {
			if date, err = time.ParseInLocation(isoDate, *dateFlag, time.Local); err != nil {
				return fail(fmt.Errorf("--date: %w", err))
			}
		}
		// carry-over only happens when today's note is created
		res, err := store.EnsureDaily(date, date.Format(isoDate) == now.Format(isoDate))
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
	}
	return 0
}

func printWords(stdout io.Writer, store *Store, tracked, excluded []string, now time.Time, asJSON bool) int {
	per := store.WordsPerDay()
	if asJSON {
		return printJSON(stdout, map[string]any{"tracked": tracked, "excluded": excluded, "per_day": per})
	}
	fmt.Fprintf(stdout, "tracked: %s\n", strings.Join(tracked, ", "))
	if len(excluded) > 0 {
		fmt.Fprintf(stdout, "excluded: %s\n", strings.Join(excluded, ", "))
	}
	fmt.Fprintf(stdout, "today: %d new words\n", per[now.Format(isoDate)])
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
		target := baseURL(port) + "/?app=1"
		if p, ok := resp["path"].(string); ok && p != "" {
			if p == tasksPath {
				target += "#/tasks"
			} else if p == activityPath {
				target += "#/activity"
			} else {
				target += "#/note/" + (&url.URL{Path: p}).EscapedPath()
			}
		}
		openViewerWindow(target)
	}
	return nil
}
