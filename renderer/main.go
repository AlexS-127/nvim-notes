// notesview: a self-contained markdown viewer for a folder of notes.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
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

func usage() {
	fmt.Fprintln(os.Stderr, `usage:
  notesview serve [--dir DIR] [--port N]
  notesview open  [PATH | --tasks] [--line N] [--dir DIR] [--port N]
  notesview scroll PATH --line N [--port N]`)
	os.Exit(2)
}

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	cmd, args := os.Args[1], os.Args[2:]
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
