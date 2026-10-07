package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParsePmset(t *testing.T) {
	log := `2026-10-06 01:12:03 -0400 Sleep               	Entering Sleep state due to 'Clamshell Sleep':TCPKeepAlive=active Using Batt (Charge:80%) 2 secs
2026-10-06 03:00:00 -0400 DarkWake            	DarkWake from Deep Idle [CDN] : due to RTC/Maintenance
2026-10-06 08:47:08 -0400 Wake                	Wake from Deep Idle [CDNVA] : due to smc.70070000 lid SMC.OutboxNotEmpty/HID Activity
2026-10-06 08:47:09 -0400 Notification        	Display is turned on
2026-10-06 09:00:00 -0400 Assertions          	PID 1 Created
`
	recs := parsePmset([]byte(log), "")
	if len(recs) != 3 || recs[0]["ev"] != "sleep" || recs[0]["cause"] != "lid" || recs[1]["ev"] != "wake" || recs[2]["ev"] != "display_on" {
		t.Fatalf("records %v", recs)
	}
	if again := parsePmset([]byte(log), "2026-10-06T08:47:08"); len(again) != 1 {
		t.Fatalf("resume after: %v", again)
	}
}

func TestParseHealth(t *testing.T) {
	js := `{"data":{"metrics":[
 {"name":"heart_rate","units":"count/min","data":[{"date":"2026-10-06 10:00:00 -0400","Min":58,"Avg":64.5,"Max":71}]},
 {"name":"heart_rate_variability","units":"ms","data":[{"date":"2026-10-06 03:10:00 -0400","qty":48.2}]},
 {"name":"sleep_analysis","units":"hr","data":[{"date":"2026-10-06 00:00:00 -0400","core":3.9,"deep":1.1,"rem":1.6,"awake":0.3,"totalSleep":6.6,"sleepStart":"2026-10-06 00:40:00 -0400","sleepEnd":"2026-10-06 07:30:00 -0400"}]},
 {"name":"something_else","units":"x","data":[{"date":"2026-10-06 10:00:00 -0400","qty":1}]}],
 "workouts":[{"name":"Running","start":"2026-10-06 18:00:00 -0400","end":"2026-10-06 18:32:00 -0400"}]}}`
	recs, err := parseHealth([]byte(js))
	if err != nil || len(recs) != 4 {
		t.Fatal(recs, err)
	}
	if recs[0]["metric"] != "hr" || recs[0]["avg"] != 64.5 || recs[1]["qty"] != 48.2 || recs[2]["totalsleep"] != 6.6 || recs[3]["min"] != 32 {
		t.Fatalf("records %v", recs)
	}
}

func TestImportWeatherAndHealth(t *testing.T) {
	s := newTestStore(t, map[string]string{"inbox.md": "# Inbox\n"})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("latitude") != "33.75" {
			t.Errorf("coordinates %v", r.URL.Query())
		}
		w.Write([]byte(`{"daily":{"time":["2026-10-05","2026-10-06"],"temperature_2m_max":[24.1,22.0],"precipitation_sum":[0,3.2],"daylight_duration":[42000,41900]}}`))
	}))
	defer srv.Close()
	old := weatherURL
	weatherURL = srv.URL
	defer func() { weatherURL = old }()
	hd := t.TempDir()
	os.WriteFile(filepath.Join(hd, "export.json"), []byte(`{"data":{"metrics":[{"name":"resting_heart_rate","data":[{"date":"2026-10-06 08:00:00 -0400","qty":55}]}]}}`), 0o644)
	cfg := s.Sensors()
	cfg.Lat, cfg.Lon, cfg.HealthDir = 33.749, -84.388, hd
	cfg.Sensors["pmset"], cfg.Sensors["screentime"], cfg.Sensors["messages"] = false, false, false
	s.SaveSensors(cfg)
	now := time.Date(2026, 10, 6, 15, 0, 0, 0, time.Local)
	s.RunImports(now)
	s.RunImports(now) // a second run adds nothing new for past days or unchanged files
	b, _ := os.ReadFile(s.signalPath("2026-10-05.jsonl"))
	if strings.Count(string(b), `"kind":"weather"`) != 1 {
		t.Fatalf("weather 10-05: %s", b)
	}
	b, _ = os.ReadFile(s.signalPath("2026-10-06.jsonl"))
	if strings.Count(string(b), `"metric":"rhr"`) != 1 || strings.Count(string(b), `"kind":"weather"`) != 2 {
		t.Fatalf("10-06: %s", b)
	}
	if st := s.loadImportState(); st.States["weather"] != "on" || st.States["health"] != "on" || st.States["pmset"] != "off" {
		t.Fatalf("states %v", st.States)
	}
}

func TestBundleCategory(t *testing.T) {
	for id, want := range map[string]string{"com.burbn.instagram": "social", "com.google.ios.youtube": "entertainment", "com.apple.MobileSMS": "comms", "com.mitchellh.ghostty": "code", "local.notesview.app": "study", "com.example.x": "other"} {
		if got := bundleCategory(id); got != want {
			t.Errorf("%s: %s, want %s", id, got, want)
		}
	}
}
