package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Control of the NotesViewSense helper (macapp/Sense.swift) through its launch agent
// local.notesview-sense: it starts at login, restarts after a crash, and Stop unloads it (it stays
// stopped until Start or the next login). Used by the home controls (start page z, the Activity
// card, the Data tab) via POST /api/data/helper and `notesview data helper`.

const helperLabel = "local.notesview-sense"

func helperTarget() string { return fmt.Sprintf("gui/%d/%s", os.Getuid(), helperLabel) }

func helperPlist() string {
	return filepath.Join(os.Getenv("HOME"), "Library", "LaunchAgents", helperLabel+".plist")
}

var launchPidRe = regexp.MustCompile(`(?m)^\s*pid = (\d+)`)

// HelperState is the helper's state: installed (agent file present), loaded (agent active), running (pid).
type HelperState struct {
	Installed bool `json:"installed"`
	Loaded    bool `json:"loaded"`
	Running   bool `json:"running"`
	PID       int  `json:"pid,omitempty"`
}

func HelperStatus() HelperState {
	var st HelperState
	_, err := os.Stat(helperPlist())
	st.Installed = err == nil
	out, err := exec.Command("launchctl", "print", helperTarget()).Output()
	if err == nil {
		st.Loaded = true
		if m := launchPidRe.FindSubmatch(out); m != nil {
			st.PID, _ = strconv.Atoi(string(m[1]))
			st.Running = st.PID > 0
		}
	}
	if !st.Running { // started by hand (opened the app) rather than by launchd
		if b, err := exec.Command("pgrep", "-f", "NotesViewSense.app/Contents/MacOS/NotesViewSense").Output(); err == nil {
			if pid, err := strconv.Atoi(strings.Fields(string(b))[0]); err == nil {
				st.Running, st.PID = true, pid
			}
		}
	}
	return st
}

// HelperControl starts, stops or restarts the helper.
func HelperControl(action string) (HelperState, error) {
	st := HelperStatus()
	if !st.Installed {
		return st, fmt.Errorf("%w: the helper's launch agent isn't installed (run ~/dotfiles/bootstrap.sh)", ErrLabel)
	}
	domain := fmt.Sprintf("gui/%d", os.Getuid())
	run := func(args ...string) error {
		if out, err := exec.Command("launchctl", args...).CombinedOutput(); err != nil {
			return fmt.Errorf("launchctl %s: %s", args[0], strings.TrimSpace(string(out)))
		}
		return nil
	}
	var err error
	switch action {
	case "start":
		if !st.Loaded {
			err = run("bootstrap", domain, helperPlist())
		} else if !st.Running {
			err = run("kickstart", helperTarget())
		}
	case "stop":
		if st.Loaded {
			err = run("bootout", helperTarget())
		}
		_ = exec.Command("pkill", "-f", "NotesViewSense.app/Contents/MacOS/NotesViewSense").Run() // one opened by hand too
	case "restart":
		if st.Loaded {
			err = run("kickstart", "-k", helperTarget())
		} else {
			_ = exec.Command("pkill", "-f", "NotesViewSense.app/Contents/MacOS/NotesViewSense").Run()
			err = run("bootstrap", domain, helperPlist())
		}
	default:
		return st, fmt.Errorf("%w: action is start, stop or restart", ErrLabel)
	}
	if err != nil {
		return HelperStatus(), err
	}
	return HelperStatus(), nil
}
