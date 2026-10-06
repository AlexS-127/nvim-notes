package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"
)

// Calendar API (see calendar.go). Reads are GET; changes go through Server.post.

func (s *Server) calendarRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/calendar", s.handleCalendar)
	mux.HandleFunc("/api/calendar/upcoming", func(w http.ResponseWriter, r *http.Request) {
		now := time.Now()
		writeJSON(w, map[string]any{"now": now.Format(time.RFC3339), "classes": nonNilEvents(s.store.Upcoming(now, 5)), "points": scoreCheckinPts, "early_min": int(checkinEarly.Minutes()), "calendars": len(s.store.Calendars())})
	})
	mux.HandleFunc("/api/calendar/attendance", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, s.store.Attendance(time.Now()))
	})
	mux.HandleFunc("/api/calendar/import", s.post(s.handleCalendarImport))
	mux.HandleFunc("/api/calendar/update", s.post(s.handleCalendarUpdate))
	mux.HandleFunc("/api/calendar/delete", s.post(s.handleCalendarDelete))
	mux.HandleFunc("/api/calendar/checkin", s.post(s.handleCalendarCheckin))
}

func nonNilEvents(e []CalEvent) []CalEvent {
	if e == nil {
		return []CalEvent{}
	}
	return e
}

// handleCalendar returns the imported calendars and attendance per class.
func (s *Server) handleCalendar(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	writeJSON(w, map[string]any{
		"now": now.Format(time.RFC3339), "points": scoreCheckinPts, "early_min": int(checkinEarly.Minutes()),
		"calendars":  s.store.Calendars(),
		"attendance": s.store.Attendance(now),
		"summary":    s.store.AttendanceSummary(now),
	})
}

func (s *Server) handleCalendarImport(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name  string `json:"name"`
		ICS   string `json:"ics"`
		Class *bool  `json:"class"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, calMaxImport+1<<20)).Decode(&req); err != nil {
		http.Error(w, "bad request", 400)
		return
	}
	class := req.Class == nil || *req.Class
	m, err := s.store.ImportCalendar(req.Name, []byte(req.ICS), class, time.Now())
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	writeJSON(w, m)
}

func (s *Server) handleCalendarUpdate(w http.ResponseWriter, r *http.Request) {
	var p CalendarPatch
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		http.Error(w, "bad request", 400)
		return
	}
	m, err := s.store.UpdateCalendar(p)
	if err != nil {
		http.Error(w, err.Error(), 404)
		return
	}
	writeJSON(w, m)
}

func (s *Server) handleCalendarDelete(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", 400)
		return
	}
	if err := s.store.DeleteCalendar(req.ID); err != nil {
		http.Error(w, err.Error(), 404)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) handleCalendarCheckin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", 400)
		return
	}
	ev, err := s.store.CheckIn(req.ID, time.Now())
	if err != nil {
		code := http.StatusInternalServerError
		if errors.Is(err, ErrCheckin) {
			code = http.StatusConflict
		}
		http.Error(w, err.Error(), code)
		return
	}
	s.store.FullActivity(time.Now(), 1) // the points show up in the score graph at once
	writeJSON(w, map[string]any{"event": ev, "points": scoreCheckinPts})
}
