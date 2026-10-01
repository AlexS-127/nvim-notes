package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const shellMarker = "# >>> nvim-notes >>>"

type checkResult struct {
	level string // ok, warn, fail
	name  string
	info  string
	fix   string
}

// runDoctor checks the pieces install.sh sets up and prints a fix for each
// problem. It returns 1 if anything failed.
func runDoctor(w io.Writer) int {
	home, _ := os.UserHomeDir()
	xdg := os.Getenv("XDG_CONFIG_HOME")
	if xdg == "" {
		xdg = filepath.Join(home, ".config")
	}
	const reinstall = "run ./install.sh from your nvim-notes checkout"
	var res []checkResult
	add := func(level, name, info, fix string) { res = append(res, checkResult{level, name, info, fix}) }

	// binary
	if p, err := exec.LookPath("notesview"); err != nil {
		add("fail", "notesview on PATH", "not found", "add ~/.local/bin to PATH (open a new shell after install.sh) or "+reinstall)
	} else {
		out, err := exec.Command(p, "--version").Output()
		v := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(out)), "notesview"))
		switch {
		case err != nil || v == "":
			add("fail", "notesview on PATH", p+" is too old to report a version", reinstall)
		case versionLess(v, version):
			add("fail", "notesview on PATH", fmt.Sprintf("%s is %s, older than this one (%s)", p, v, version), reinstall)
		default:
			add("ok", "notesview on PATH", p+" ("+v+")", "")
		}
	}

	// symlinks
	link := func(name, path string, wantDir bool) {
		st, err := os.Lstat(path)
		if err != nil {
			add("fail", name, path+" is missing", reinstall)
			return
		}
		if st.Mode()&os.ModeSymlink == 0 {
			add("warn", name, path+" is your own file, not linked to the repo", "fine if intended; "+reinstall+" to link it (your file is backed up)")
			return
		}
		target, _ := os.Readlink(path)
		real, err := os.Stat(path)
		if err != nil || real.IsDir() != wantDir {
			add("fail", name, path+" → "+target+" is broken", reinstall)
			return
		}
		add("ok", name, path+" → "+target, "")
	}
	link("Neovim config", filepath.Join(xdg, "nvim"), true)
	if _, err := os.Stat(filepath.Join(xdg, "nvim", "init.lua")); err != nil {
		add("fail", "Neovim init.lua", "no init.lua in "+filepath.Join(xdg, "nvim"), reinstall)
	}
	link("custom.css", filepath.Join(configDir(), "custom.css"), false)
	link("config.toml", ConfigPath(), false)
	if cfg, err := LoadConfig(ConfigPath()); err != nil {
		add("fail", "config.toml syntax", err.Error(), "fix the TOML (see the comments at the top of the file)")
	} else if len(cfg.Classes) == 0 {
		add("warn", "classes", "no classes in "+ConfigPath(), "add [[class]] blocks to config.toml")
	} else {
		var ids []string
		for _, c := range cfg.Classes {
			ids = append(ids, c.ID)
		}
		add("ok", "classes", strings.Join(ids, ", "), "")
	}

	// notes folder
	dir := defaultDir()
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		add("fail", "notes folder", dir+" does not exist", "mkdir -p "+dir+" (or set NOTES_DIR)")
	} else if f, err := os.CreateTemp(dir, ".notesview-doctor-*"); err != nil {
		add("fail", "notes folder", dir+" is not writable", "check the folder's permissions")
	} else {
		f.Close()
		os.Remove(f.Name())
		add("ok", "notes folder", dir, "")
	}

	// shell block
	rcs := []string{filepath.Join(home, ".zshrc"), filepath.Join(home, ".bashrc")}
	found := ""
	for _, rc := range rcs {
		if b, err := os.ReadFile(rc); err == nil && strings.Contains(string(b), shellMarker) {
			found = rc
			if !strings.Contains(string(b), "inbox()") {
				add("fail", "shell setup", rc+" has an old nvim-notes block without the inbox function", reinstall)
				found = "old"
			}
			break
		}
	}
	if found == "" {
		add("fail", "shell setup", "no nvim-notes block in ~/.zshrc or ~/.bashrc", reinstall+", then open a new shell")
	} else if found != "old" {
		add("ok", "shell setup", found+" (notes alias, inbox function)", "")
	}

	if _, err := exec.LookPath("nvim"); err != nil {
		add("warn", "nvim on PATH", "not found", reinstall)
	}

	// global capture shortcut
	if runtime.GOOS == "darwin" {
		app := ""
		for _, p := range []string{"/Applications/Hammerspoon.app", filepath.Join(home, "Applications", "Hammerspoon.app")} {
			if _, err := os.Stat(p); err == nil {
				app = p
			}
		}
		if app == "" {
			add("fail", "Hammerspoon", "not installed", "brew install --cask hammerspoon, or "+reinstall)
		} else {
			add("ok", "Hammerspoon", app, "")
		}
		initLua := filepath.Join(home, ".hammerspoon", "init.lua")
		if b, err := os.ReadFile(initLua); err != nil || !strings.Contains(string(b), `require("nvim_notes")`) {
			add("fail", "Hammerspoon config", initLua+" does not load nvim_notes", reinstall)
		} else if _, err := os.Stat(filepath.Join(home, ".hammerspoon", "nvim_notes.lua")); err != nil {
			add("fail", "Hammerspoon config", "~/.hammerspoon/nvim_notes.lua is missing", reinstall)
		} else {
			add("ok", "Hammerspoon config", "Ctrl+Option+I captures to the inbox", "")
		}
	} else {
		add("ok", "global shortcut", "skipped on "+runtime.GOOS+" (macOS only); use the inbox shell function", "")
	}

	failed := 0
	marks := map[string]string{"ok": "✓", "warn": "!", "fail": "✗"}
	for _, r := range res {
		fmt.Fprintf(w, "%s %-20s %s\n", marks[r.level], r.name, r.info)
		if r.fix != "" {
			fmt.Fprintf(w, "  %-20s fix: %s\n", "", r.fix)
		}
		if r.level == "fail" {
			failed++
		}
	}
	if failed > 0 {
		fmt.Fprintf(w, "\n%d problem(s) found.\n", failed)
		return 1
	}
	fmt.Fprintln(w, "\nAll good.")
	return 0
}

// versionLess compares dotted versions ("0.2.0", "v0.10.1"). Anything that
// is not a version (such as "dev") counts as current.
func versionLess(a, b string) bool {
	pa, oka := parseVersion(a)
	pb, okb := parseVersion(b)
	if !oka || !okb {
		return false
	}
	for i := 0; i < 3; i++ {
		if pa[i] != pb[i] {
			return pa[i] < pb[i]
		}
	}
	return false
}

func parseVersion(v string) ([3]int, bool) {
	var out [3]int
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "-+ "); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	if len(parts) == 0 || len(parts) > 3 {
		return out, false
	}
	for i, p := range parts {
		n := 0
		if p == "" {
			return out, false
		}
		for _, r := range p {
			if r < '0' || r > '9' {
				return out, false
			}
			n = n*10 + int(r-'0')
		}
		out[i] = n
	}
	return out, true
}
