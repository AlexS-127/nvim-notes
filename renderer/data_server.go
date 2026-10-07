package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"
)

// Data API: labels and prompts for home (labels.go); the Data tab's views are added in data_view.go.

func (s *Server) dataRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/data/prompts", func(w http.ResponseWriter, r *http.Request) {
		now := time.Now()
		p := s.store.OpenPrompts(now, s.store.CurrentIdle(now))
		writeJSON(w, map[string]any{"prompts": p, "categories": labelCategories, "places": placeLabels})
	})
	mux.HandleFunc("/api/data/queue", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"queue": s.store.LabelQueue(time.Now()), "categories": labelCategories, "places": placeLabels})
	})
	decode := func(w http.ResponseWriter, r *http.Request, v any) bool {
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(v) != nil {
			http.Error(w, "bad request", 400)
			return false
		}
		return true
	}
	mux.HandleFunc("/api/data/checkin", s.post(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Prompted string `json:"prompted"`
			Focus    int    `json:"focus"`
		}
		if decode(w, r, &req) {
			s.dataReply(w, s.store.Checkin(req.Prompted, req.Focus, time.Now()))
		}
	}))
	mux.HandleFunc("/api/data/checkout", s.post(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Productivity, Energy, Mood, Sleep int
			Note                              string
		}
		if decode(w, r, &req) {
			s.dataReply(w, s.store.Checkout(req.Productivity, req.Energy, req.Mood, req.Sleep, req.Note, time.Now()))
		}
	}))
	mux.HandleFunc("/api/data/skip", s.post(func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Target, Key string }
		if decode(w, r, &req) {
			s.dataReply(w, s.store.Skip(req.Target, req.Key, time.Now()))
		}
	}))
	mux.HandleFunc("/api/data/label", s.post(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Kind, Hash, Category string
			Tokens               []string
		}
		if decode(w, r, &req) {
			s.dataReply(w, s.store.Label(req.Kind, req.Hash, req.Category, req.Tokens, time.Now()))
		}
	}))
	mux.HandleFunc("/api/data/grade", s.post(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Course, Item string
			Score, Max   float64
		}
		if decode(w, r, &req) {
			s.dataReply(w, s.store.Grade(req.Course, req.Item, req.Score, req.Max, time.Now()))
		}
	}))
}

func (s *Server) dataReply(w http.ResponseWriter, err error) {
	if err != nil {
		code := 500
		if errors.Is(err, ErrLabel) {
			code = 400
		}
		http.Error(w, err.Error(), code)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

// CurrentIdle is the machine's idle seconds from the newest sample in the last 2 minutes
// (the helper's input sensor or the server's sampler), -1 when unknown.
func (s *Store) CurrentIdle(now time.Time) float64 {
	recs := s.ReadSignals(now.Format(isoDate))
	cutoff := now.Add(-2 * time.Minute).Format(scoreStamp)
	for i := len(recs) - 1; i >= 0 && i >= len(recs)-400; i-- {
		r := recs[i]
		if at, _ := r["at"].(string); at < cutoff {
			break
		}
		src, _ := r["src"].(string)
		sensor, _ := r["sensor"].(string)
		if src == "sys" || (src == "sense" && sensor == "input") {
			if v, ok := r["idle"].(float64); ok {
				return v
			}
		}
	}
	return -1
}
