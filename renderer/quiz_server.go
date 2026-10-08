package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"time"
)

// Quiz API for the viewer's Quiz tab (web/quiz.js); the engine is quiz.go / quiz_session.go.
//   GET  /api/quiz                 subjects to practise, notes due for revision
//   GET  /api/quiz/session?id=     a session's state (to resume after a reload)
//   POST /api/quiz/start           {mode: practice|revise, subject, group, dir, weakest, id, solo}
//   POST /api/quiz/answer          {session, response | choices | reveal | grade, percent | skip}
//   POST /api/quiz/override        {session}: "Actually right?"
//   POST /api/quiz/next            {session}: confirm the last answer, next card
//   POST /api/quiz/end             {session}: finish (a revision records what was answered enough)
// Every reply is {state, result?}.

func (s *Server) quizRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/quiz", func(w http.ResponseWriter, r *http.Request) {
		now := time.Now()
		s.store.RecordRevision(now)
		type due struct {
			Topic
			Questions bool `json:"questions"`
		}
		ds := []due{}
		for _, t := range s.store.RevisionDue(now) {
			_, err := os.Stat(s.store.QuestionsPath(t.ID))
			ds = append(ds, due{t, err == nil})
		}
		subs := s.store.QuizSubjects()
		if subs == nil {
			subs = []QuizSubject{}
		}
		writeJSON(w, map[string]any{"subjects": subs, "due": ds, "points": scoreRevisionPts})
	})
	mux.HandleFunc("/api/quiz/session", func(w http.ResponseWriter, r *http.Request) {
		q := getQuizSession(r.URL.Query().Get("id"))
		if q == nil {
			http.Error(w, "no such quiz session (it may have expired)", 404)
			return
		}
		q.mu.Lock()
		defer q.mu.Unlock()
		if q.cur == nil && q.pending == nil && !q.done {
			q.advance(s.store, time.Now())
		}
		writeJSON(w, map[string]any{"state": q.State()})
	})
	mux.HandleFunc("/api/quiz/start", s.post(func(w http.ResponseWriter, r *http.Request) {
		var req QuizStart
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<12)).Decode(&req) != nil {
			http.Error(w, "bad request", 400)
			return
		}
		q, err := s.store.StartQuiz(req, time.Now())
		if err != nil {
			s.quizError(w, err)
			return
		}
		q.mu.Lock()
		defer q.mu.Unlock()
		writeJSON(w, map[string]any{"state": q.State()})
	}))
	type sessReq struct {
		Session string `json:"session"`
		QuizAnswer
	}
	with := func(f func(q *QuizSession, req sessReq) (*QuizResult, error)) http.HandlerFunc {
		return s.post(func(w http.ResponseWriter, r *http.Request) {
			var req sessReq
			if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req) != nil {
				http.Error(w, "bad request", 400)
				return
			}
			q := getQuizSession(req.Session)
			if q == nil {
				http.Error(w, "no such quiz session (it may have expired)", 404)
				return
			}
			q.mu.Lock()
			defer q.mu.Unlock()
			res, err := f(q, req)
			if err != nil {
				s.quizError(w, err)
				return
			}
			writeJSON(w, map[string]any{"state": q.State(), "result": res})
		})
	}
	mux.HandleFunc("/api/quiz/answer", with(func(q *QuizSession, req sessReq) (*QuizResult, error) {
		return s.store.AnswerQuiz(q, req.QuizAnswer, time.Now())
	}))
	mux.HandleFunc("/api/quiz/override", with(func(q *QuizSession, req sessReq) (*QuizResult, error) {
		return s.store.OverrideQuiz(q, time.Now())
	}))
	mux.HandleFunc("/api/quiz/next", with(func(q *QuizSession, req sessReq) (*QuizResult, error) {
		s.store.NextQuiz(q, time.Now())
		return nil, nil
	}))
	mux.HandleFunc("/api/quiz/end", with(func(q *QuizSession, req sessReq) (*QuizResult, error) {
		s.store.EndQuiz(q, time.Now())
		return nil, nil
	}))
}

func (s *Server) quizError(w http.ResponseWriter, err error) {
	code := 500
	if errors.Is(err, ErrRevision) {
		code = 400
	}
	http.Error(w, err.Error(), code)
}
