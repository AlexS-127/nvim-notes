package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"
)

// Reading API (see reading.go). Reads are GET; changes go through Server.post.

func (s *Server) readingRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/reading", func(w http.ResponseWriter, r *http.Request) {
		a := Activity{Today: time.Now().Format(isoDate), Days: map[string]DayStat{}}
		a.AddReading(s.store, time.Now())
		writeJSON(w, map[string]any{"books": s.store.Books(), "summary": a.Reading})
	})
	mux.HandleFunc("/api/reading/add", s.post(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Title  string `json:"title"`
			Author string `json:"author"`
			Pages  int    `json:"pages"`
			At     int    `json:"at"` // already on this page: no points
		}
		if json.NewDecoder(r.Body).Decode(&req) != nil {
			http.Error(w, "bad request", 400)
			return
		}
		b, err := s.store.AddBook(req.Title, req.Author, req.Pages, time.Now())
		if err == nil && req.At > 0 {
			b, _, err = s.store.SetStartPage(strconv.Itoa(b.ID), req.At, time.Now())
		}
		s.readingReply(w, b, err)
	}))
	mux.HandleFunc("/api/reading/log", s.post(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID    int  `json:"id"`
			Pages int  `json:"pages"`
			To    *int `json:"to"`
			At    *int `json:"at"` // already on this page: moves the bookmark, no points
		}
		if json.NewDecoder(r.Body).Decode(&req) != nil {
			http.Error(w, "bad request", 400)
			return
		}
		to := -1
		if req.To != nil {
			to = max(0, *req.To)
		}
		ref := ""
		if req.ID > 0 {
			ref = strconv.Itoa(req.ID)
		}
		var b Book
		var e ReadEntry
		var err error
		if req.At != nil {
			b, e, err = s.store.SetStartPage(ref, *req.At, time.Now())
		} else {
			b, e, err = s.store.LogPages(ref, req.Pages, to, time.Now())
		}
		if err != nil {
			s.readingReply(w, b, err)
			return
		}
		s.store.FullActivity(time.Now(), 1) // the points show up in the score graph at once
		writeJSON(w, map[string]any{"book": b, "entry": e, "points": map[bool]int{false: max(0, e.Pages) / scoreReadPagesPer}[e.Baseline]})
	}))
	mux.HandleFunc("/api/reading/update", s.post(func(w http.ResponseWriter, r *http.Request) {
		var p BookPatch
		if json.NewDecoder(r.Body).Decode(&p) != nil {
			http.Error(w, "bad request", 400)
			return
		}
		b, err := s.store.UpdateBook(p, time.Now())
		s.readingReply(w, b, err)
	}))
	mux.HandleFunc("/api/reading/delete", s.post(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID int `json:"id"`
		}
		if json.NewDecoder(r.Body).Decode(&req) != nil {
			http.Error(w, "bad request", 400)
			return
		}
		if err := s.store.DeleteBook(req.ID); err != nil {
			s.readingReply(w, Book{}, err)
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
	}))
}

// readingReply sends the book, or the error (400 for a bad request, 500 for a failed write).
func (s *Server) readingReply(w http.ResponseWriter, b Book, err error) {
	if err != nil {
		code := http.StatusInternalServerError
		if errors.Is(err, ErrReading) {
			code = http.StatusBadRequest
		}
		http.Error(w, err.Error(), code)
		return
	}
	writeJSON(w, b)
}
