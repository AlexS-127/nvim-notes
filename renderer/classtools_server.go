package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Class page tools API: Rebuild banks (classbanks.go) and Check notes (notecheck.go). A rebuild or a
// check runs in the background, one of each kind per class at a time; the page polls the GET routes
// while one is running. Jobs live in server memory only (a restart forgets them; resetRebuilding
// puts back topics a stopped rebuild left behind).

// ClassJob is a background rebuild or check.
type ClassJob struct {
	Kind     string            `json:"kind"` // rebuild or check
	Mode     string            `json:"mode,omitempty"`
	Total    int               `json:"total"`
	Done     int               `json:"done"`
	Started  string            `json:"started"`
	Finished string            `json:"finished,omitempty"`
	Running  bool              `json:"running"`
	Error    string            `json:"error,omitempty"`
	Failed   map[string]string `json:"failed,omitempty"` // rebuild: topic → why
	Found    int               `json:"found,omitempty"`  // check: suggestions
}

var (
	classJobsMu sync.Mutex
	classJobs   = map[string]*ClassJob{} // kind + ":" + subject
)

func classJob(kind, subject string) *ClassJob {
	classJobsMu.Lock()
	defer classJobsMu.Unlock()
	if j := classJobs[kind+":"+subject]; j != nil {
		c := *j
		return &c
	}
	return nil
}

// startClassJob registers a job unless one of that kind is already running for the class.
func startClassJob(kind, subject string, j *ClassJob) bool {
	classJobsMu.Lock()
	defer classJobsMu.Unlock()
	if old := classJobs[kind+":"+subject]; old != nil && old.Running {
		return false
	}
	j.Kind, j.Running, j.Started = kind, true, time.Now().Format(scoreStamp)
	classJobs[kind+":"+subject] = j
	return true
}

func updateClassJob(j *ClassJob, f func(*ClassJob)) {
	classJobsMu.Lock()
	defer classJobsMu.Unlock()
	f(j)
}

func badSubject(sub string) bool {
	return sub == "" || strings.ContainsAny(sub, "/\\") || strings.HasPrefix(sub, ".")
}

func (s *Server) classToolRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/classes/banks", func(w http.ResponseWriter, r *http.Request) {
		sub := r.URL.Query().Get("subject")
		if badSubject(sub) {
			http.Error(w, "bad folder", 400)
			return
		}
		writeJSON(w, map[string]any{"rows": s.store.BankHealth(sub, time.Now()), "job": classJob("rebuild", sub), "claude": claudeBinary() != ""})
	})
	mux.HandleFunc("/api/classes/check", func(w http.ResponseWriter, r *http.Request) {
		sub := r.URL.Query().Get("subject")
		if badSubject(sub) {
			http.Error(w, "bad folder", 400)
			return
		}
		c, changed := s.store.CheckView(sub)
		writeJSON(w, map[string]any{"check": c, "changed": changed, "notes": len(s.store.graphNotes(sub)), "job": classJob("check", sub), "claude": claudeBinary() != ""})
	})
	type req struct {
		Subject string   `json:"subject"`
		IDs     []string `json:"ids"`
		Mode    string   `json:"mode"`
		Replace string   `json:"replace"`
	}
	read := func(w http.ResponseWriter, r *http.Request) (req, bool) {
		var q req
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&q) != nil || badSubject(q.Subject) {
			http.Error(w, "bad request", 400)
			return q, false
		}
		return q, true
	}
	mux.HandleFunc("/api/classes/banks/rebuild", s.post(func(w http.ResponseWriter, r *http.Request) {
		q, ok := read(w, r)
		if !ok {
			return
		}
		if q.Mode != "full" {
			q.Mode = "repair"
		}
		if len(q.IDs) == 0 {
			http.Error(w, "pick the topics to rebuild", 400)
			return
		}
		if claudeBinary() == "" {
			http.Error(w, "claude not found", 500)
			return
		}
		j := &ClassJob{Mode: q.Mode, Total: len(q.IDs)}
		if !startClassJob("rebuild", q.Subject, j) {
			http.Error(w, "a rebuild of this class is already running", 409)
			return
		}
		go func() {
			res := s.store.RebuildBanks(q.Subject, q.IDs, q.Mode, func(done int) { updateClassJob(j, func(j *ClassJob) { j.Done = done }) })
			updateClassJob(j, func(j *ClassJob) {
				j.Running, j.Done, j.Finished = false, len(q.IDs), time.Now().Format(scoreStamp)
				for id, err := range res {
					if err != nil {
						if j.Failed == nil {
							j.Failed = map[string]string{}
						}
						j.Failed[id] = strings.TrimPrefix(err.Error(), "revision: ")
					}
				}
			})
			select { // new banks: the folder's cross-topic questions are rewritten next
			case s.revKick <- struct{}{}:
			default:
			}
		}()
		writeJSON(w, map[string]bool{"ok": true})
	}))
	mux.HandleFunc("/api/classes/check/run", s.post(func(w http.ResponseWriter, r *http.Request) {
		q, ok := read(w, r)
		if !ok {
			return
		}
		if claudeBinary() == "" {
			http.Error(w, "claude not found", 500)
			return
		}
		j := &ClassJob{Total: len(q.IDs)}
		if j.Total == 0 {
			j.Total = len(s.store.graphNotes(q.Subject))
		}
		if !startClassJob("check", q.Subject, j) {
			http.Error(w, "a check of this class is already running", 409)
			return
		}
		go func() {
			n, err := s.store.CheckNotes(q.Subject, q.IDs, time.Now())
			updateClassJob(j, func(j *ClassJob) {
				j.Running, j.Done, j.Found, j.Finished = false, j.Total, n, time.Now().Format(scoreStamp)
				if err != nil {
					j.Error = err.Error()
				}
			})
		}()
		writeJSON(w, map[string]bool{"ok": true})
	}))
	resolve := func(apply bool) http.HandlerFunc {
		return s.post(func(w http.ResponseWriter, r *http.Request) {
			q, ok := read(w, r)
			if !ok {
				return
			}
			n, err := s.store.ResolveFindings(q.Subject, q.IDs, apply, q.Replace, time.Now())
			if err != nil {
				code := 500
				if errors.Is(err, ErrRevision) {
					code = 409
				}
				http.Error(w, strings.TrimPrefix(err.Error(), "revision: "), code)
				return
			}
			writeJSON(w, map[string]int{"done": n})
		})
	}
	mux.HandleFunc("/api/classes/check/apply", resolve(true))
	mux.HandleFunc("/api/classes/check/dismiss", resolve(false))
}
