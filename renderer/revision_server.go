package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Revision API (see revision.go). Reads are GET; changes go through Server.post.

func (s *Server) revisionRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/revision", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, s.revisionInfo()) })
	id := func(w http.ResponseWriter, r *http.Request) (string, bool) {
		var req struct {
			ID string `json:"id"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<12)).Decode(&req) != nil || req.ID == "" {
			http.Error(w, "bad request", 400)
			return "", false
		}
		return req.ID, true
	}
	mux.HandleFunc("/api/revision/add", s.post(func(w http.ResponseWriter, r *http.Request) {
		if i, ok := id(w, r); ok {
			_, err := s.store.AddTopic(i, time.Now())
			s.revisionReply(w, err)
		}
	}))
	mux.HandleFunc("/api/revision/remove", s.post(func(w http.ResponseWriter, r *http.Request) {
		if i, ok := id(w, r); ok {
			s.revisionReply(w, s.store.RemoveTopic(i, time.Now()))
		}
	}))
	mux.HandleFunc("/api/revision/gen", s.post(func(w http.ResponseWriter, r *http.Request) {
		if i, ok := id(w, r); ok {
			s.revisionReply(w, s.store.RequestQuestions(i))
		}
	}))
	mux.HandleFunc("/api/revision/start", s.post(func(w http.ResponseWriter, r *http.Request) {
		if i, ok := id(w, r); ok {
			s.revisionReply(w, s.startRevision(i))
		}
	}))
	mux.HandleFunc("/api/revision/config", s.post(func(w http.ResponseWriter, r *http.Request) {
		var c RevisionConfig
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<14)).Decode(&c) != nil {
			http.Error(w, "bad request", 400)
			return
		}
		cur := s.store.RevisionSettings(time.Now())
		if c.Since == "" {
			c.Since = cur.Since
		}
		s.revisionReply(w, s.store.SaveRevisionSettings(c))
	}))
}

// revisionInfo is the Revision page: every topic, what is due, recent revisions, the config.
func (s *Server) revisionInfo() map[string]any {
	now := time.Now()
	s.store.RecordRevision(now)
	topics := s.store.Topics()
	type row struct {
		Topic
		Questions bool `json:"questions"`
	}
	rows := []row{}
	for _, t := range topics {
		_, err := os.Stat(s.store.QuestionsPath(t.ID))
		rows = append(rows, row{t, err == nil})
	}
	log := s.store.RevisionLog()
	recent := []RevisionEntry{}
	for i := len(log) - 1; i >= 0 && len(recent) < 50; i-- {
		recent = append(recent, log[i])
	}
	return map[string]any{"today": now.Format(isoDate), "topics": rows, "due": s.store.RevisionDue(now), "recent": recent,
		"config": s.store.RevisionSettings(now), "points": scoreRevisionPts, "claude": claudeBinary() != "",
		"min_total": revMinTotal}
}

func (s *Server) revisionReply(w http.ResponseWriter, err error) {
	if err != nil {
		code := 500
		if errors.Is(err, ErrRevision) {
			code = 400
		}
		http.Error(w, err.Error(), code)
		return
	}
	select { // new or changed topics: let the question writer look now
	case s.revKick <- struct{}{}:
	default:
	}
	s.store.FullActivity(time.Now(), 1)
	writeJSON(w, map[string]bool{"ok": true})
}

// startRevision opens a terminal window running the quiz for one topic: Ghostty if installed
// (the quiz is a terminal program), else Terminal.app.
func (s *Server) startRevision(id string) error {
	found := false
	for _, t := range s.store.Topics() {
		found = found || t.ID == id
	}
	if !found {
		return fmt.Errorf("%w: %s is not scheduled", ErrRevision, id)
	}
	script := filepath.Join(s.store.Root, "quiz.py")
	if _, err := os.Stat(script); err != nil {
		return fmt.Errorf("quiz not found: %s", script)
	}
	q := func(v string) string { return "'" + strings.ReplaceAll(v, "'", `'\''`) + "'" }
	cmd := fmt.Sprintf(`export NOTES_DIR=%s; python3 %s --revise %s; printf '\n[press Enter to close]'; read _`, q(s.store.Root), q(script), q(id))
	if _, err := os.Stat("/Applications/Ghostty.app"); err == nil {
		return exec.Command("open", "-na", "Ghostty.app", "--args", "-e", "/bin/zsh", "-lc", cmd).Run()
	}
	as := fmt.Sprintf(`tell application "Terminal" to do script %q`, "/bin/zsh -lc "+q(cmd))
	return exec.Command("osascript", "-e", as, "-e", `tell application "Terminal" to activate`).Run()
}
