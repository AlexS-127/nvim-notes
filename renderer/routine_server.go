package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"
)

// Morning routine API (see routine.go). Reads are GET; changes go through Server.post.

func (s *Server) routineRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/routine", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, s.store.Routine(time.Now())) })
	mux.HandleFunc("/api/routine/tick", s.post(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Item string `json:"item"`
			Done bool   `json:"done"`
		}
		if json.NewDecoder(r.Body).Decode(&req) != nil {
			http.Error(w, "bad request", 400)
			return
		}
		st, err := s.store.TickRoutine(req.Item, req.Done, time.Now())
		s.routineReply(w, st, err)
	}))
	mux.HandleFunc("/api/routine/forecast", s.post(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Strike *int `json:"strike"`
		}
		if json.NewDecoder(r.Body).Decode(&req) != nil || req.Strike == nil {
			http.Error(w, "bad request", 400)
			return
		}
		now := time.Now()
		st, err := s.store.RoutineForecast(*req.Strike, s.store.scoreNow(now), now)
		s.routineReply(w, st, err)
	}))
	mux.HandleFunc("/api/routine/end", s.post(func(w http.ResponseWriter, r *http.Request) {
		st, err := s.store.EndRoutine(time.Now())
		s.routineReply(w, st, err)
	}))
}

// routineReply sends today's routine (re-recording the score so points show at once), or the error.
func (s *Server) routineReply(w http.ResponseWriter, st RoutineState, err error) {
	if err != nil {
		code := http.StatusInternalServerError
		if errors.Is(err, ErrRoutine) {
			code = http.StatusBadRequest
		}
		http.Error(w, err.Error(), code)
		return
	}
	s.store.FullActivity(time.Now(), 1)
	writeJSON(w, st)
}
