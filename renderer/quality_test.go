package main

import (
	"fmt"
	"testing"
	"time"
)

func TestDataQuality(t *testing.T) {
	s := newTestStore(t, map[string]string{"inbox.md": "# Inbox\n"})
	day := "2026-10-07"
	var recs []map[string]any
	at := func(m, sec int) string { return fmt.Sprintf("%sT10:%02d:%02d", day, m, sec) }
	for m := 0; m < 30; m++ {
		for _, sec := range []int{0, 15, 30, 45} {
			recs = append(recs, map[string]any{"at": at(m, sec), "src": "sense", "sensor": "input", "keys": 40, "clicks": 1, "idle": float64(sec % 7)})
			recs = append(recs, map[string]any{"at": at(m, sec), "src": "sense", "sensor": "camera", "present": 0.2, "facing": 1, "perclos": 0})
		}
		for _, sec := range []int{0, 20, 40} {
			recs = append(recs, map[string]any{"at": at(m, sec), "src": "sense", "sensor": "mic", "db": -120, "speech": 0})
		}
	}
	writeSignals(t, s, day, recs...)
	got := map[string]QualityCheck{}
	for _, c := range s.DataQuality(day, time.Date(2026, 10, 8, 1, 0, 0, 0, time.Local)) {
		got[c.Sensor+"/"+c.Check] = c
	}
	if c := got["mic/variation"]; c.Status != "fail" {
		t.Errorf("stuck mic not caught: %+v", got)
	}
	if c := got["mic/range"]; c.Status != "fail" {
		t.Errorf("−120 dB not out of range: %+v", c)
	}
	if c := got["camera/sees you while typing"]; c.Status != "fail" || c.Value != 0.2 {
		t.Errorf("camera agreement: %+v", c)
	}
	if c := got["input/coverage"]; c.Status != "ok" {
		t.Errorf("input coverage: %+v", c)
	}
}
