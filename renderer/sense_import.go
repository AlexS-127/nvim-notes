package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Importers: data the Mac already keeps, copied into .signals as counts with "src":"import" and a
// "kind". Each importer remembers how far it got (.signals/import_state.json), so running it again
// only adds new records; the server runs them all at start and every hour (importLoop).
//   power      pmset -g log: sleep, wake, display on/off (lid, sleep window)          no permission
//   screentime knowledgeC.db: app usage minutes (Mac and, when shared, iPhone), pickups  Full Disk Access
//   messages   Messages chat.db and Mail Envelope Index: incoming messages per hour   Full Disk Access
//   weather    Open-Meteo (no key) daily temperature, rain, daylight for your city    network
//   health     Health Auto Export files in a folder you choose: heart rate, HRV, sleep,
//              resting HR, steps, workouts, wrist temperature, SpO2 (Apple Watch)    none
// Without the permission an importer records {kind: "state", importer, state: "denied"}.

const importStateFile = "import_state.json"

type importState struct {
	Power      string          `json:"power"`      // newest pmset event stamp imported
	ScreenTime float64         `json:"screentime"` // newest knowledgeC start (Mac absolute time)
	Messages   int64           `json:"messages"`   // newest chat.db date (ns since 2001)
	Mail       int64           `json:"mail"`       // newest Envelope Index date_received (unix)
	Weather    map[string]bool `json:"weather"`    // days imported
	Health     map[string]int  `json:"health"`     // file → size imported
	States     map[string]string `json:"states"`   // importer → on/denied/off/absent
}

var importMu sync.Mutex

func (s *Store) loadImportState() importState {
	var st importState
	if b, err := os.ReadFile(s.signalPath(importStateFile)); err == nil {
		_ = json.Unmarshal(b, &st)
	}
	if st.Weather == nil {
		st.Weather = map[string]bool{}
	}
	if st.Health == nil {
		st.Health = map[string]int{}
	}
	if st.States == nil {
		st.States = map[string]string{}
	}
	return st
}

func (s *Store) saveImportState(st importState) {
	b, _ := json.MarshalIndent(st, "", "  ")
	_ = os.MkdirAll(s.signalPath(""), 0o755)
	_ = writeAtomic(s.signalPath(importStateFile), append(b, '\n'))
}

// setImportState records an importer's state when it changes.
func (s *Store) setImportState(st *importState, name, state string) {
	if st.States[name] != state {
		st.States[name] = state
		appendSignal(s.Root, map[string]any{"src": "import", "kind": "state", "importer": name, "state": state})
	}
}

// RunImports runs every importer that is switched on.
func (s *Store) RunImports(now time.Time) {
	importMu.Lock()
	defer importMu.Unlock()
	cfg := s.Sensors()
	st := s.loadImportState()
	run := func(name string, f func(*importState, SensorConfig, time.Time) error) {
		if !cfg.Sensors[name] {
			s.setImportState(&st, name, "off")
			return
		}
		if err := f(&st, cfg, now); err != nil {
			if os.IsPermission(err) || strings.Contains(err.Error(), "authorization denied") || strings.Contains(err.Error(), "not permitted") {
				s.setImportState(&st, name, "denied")
			} else if os.IsNotExist(err) {
				s.setImportState(&st, name, "absent")
			} else {
				fmt.Fprintln(os.Stderr, "import", name+":", err)
			}
			return
		}
		s.setImportState(&st, name, "on")
	}
	run("pmset", s.importPower)
	run("screentime", s.importScreenTime)
	run("messages", s.importMessages)
	run("weather", s.importWeather)
	run("health", s.importHealth)
	s.saveImportState(st)
}

// importLoop runs the importers at start and every hour.
func (s *Server) importLoop() {
	time.Sleep(30 * time.Second)
	for {
		s.store.RunImports(time.Now())
		time.Sleep(time.Hour)
	}
}

// ── power (pmset) ──

var pmsetLineRe = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}) [+-]\d{4} (Sleep|Wake|DarkWake|Notification)\s+\t?(.*)$`)

// parsePmset turns `pmset -g log` into power events newer than `after` (local stamps).
func parsePmset(out []byte, after string) []map[string]any {
	var recs []map[string]any
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 64*1024), 4<<20)
	for sc.Scan() {
		m := pmsetLineRe.FindStringSubmatch(sc.Text())
		if m == nil {
			continue
		}
		at := strings.Replace(m[1], " ", "T", 1)
		if at <= after {
			continue
		}
		msg := strings.TrimSpace(m[3])
		ev := ""
		switch m[2] {
		case "Sleep":
			ev = "sleep"
		case "Wake":
			ev = "wake"
		case "DarkWake":
			continue // maintenance wakes with the lid closed: not you
		case "Notification":
			switch {
			case strings.HasPrefix(msg, "Display is turned on"):
				ev = "display_on"
			case strings.HasPrefix(msg, "Display is turned off"):
				ev = "display_off"
			default:
				continue
			}
		}
		rec := map[string]any{"src": "import", "kind": "power", "ev": ev, "at": at}
		low := strings.ToLower(msg)
		switch {
		case strings.Contains(low, "lid") || strings.Contains(low, "clamshell"):
			rec["cause"] = "lid"
		case strings.Contains(low, "idle"):
			rec["cause"] = "idle"
		case strings.Contains(low, "user"), strings.Contains(low, "hid activity"):
			rec["cause"] = "user"
		}
		recs = append(recs, rec)
	}
	return recs
}

func (s *Store) importPower(st *importState, _ SensorConfig, _ time.Time) error {
	if runtime.GOOS != "darwin" {
		return os.ErrNotExist
	}
	out, err := exec.Command("pmset", "-g", "log").Output()
	if err != nil {
		return err
	}
	for _, r := range parsePmset(out, st.Power) {
		appendSignal(s.Root, r)
		st.Power = r["at"].(string)
	}
	return nil
}

// ── sqlite helper (the system's sqlite3, read-only, on a copy so the live database is untouched) ──

func sqliteRows(db, query string) ([][]string, error) {
	if _, err := os.Stat(db); err != nil {
		return nil, err
	}
	tmp, err := os.MkdirTemp("", "notesview-import-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	cp := filepath.Join(tmp, "db")
	for _, suf := range []string{"", "-wal", "-shm"} { // copy with the write-ahead log so recent rows are there
		b, err := os.ReadFile(db + suf)
		if err != nil {
			if suf == "" {
				return nil, err
			}
			continue
		}
		if err := os.WriteFile(cp+suf, b, 0o600); err != nil {
			return nil, err
		}
	}
	out, err := exec.Command("sqlite3", "-readonly", "-separator", "\t", cp, query).Output()
	if err != nil {
		return nil, fmt.Errorf("sqlite: %w", err)
	}
	var rows [][]string
	for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if l != "" {
			rows = append(rows, strings.Split(l, "\t"))
		}
	}
	return rows, nil
}

// macEpoch is 2001-01-01 UTC (Core Data / Apple absolute time).
var macEpoch = time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)

// ── Screen Time (knowledgeC) ──

func (s *Store) importScreenTime(st *importState, _ SensorConfig, _ time.Time) error {
	home, _ := os.UserHomeDir()
	db := filepath.Join(home, "Library", "Application Support", "Knowledge", "knowledgeC.db")
	q := fmt.Sprintf(`SELECT ZOBJECT.ZSTREAMNAME, COALESCE(ZOBJECT.ZVALUESTRING,''), ZOBJECT.ZSTARTDATE, ZOBJECT.ZENDDATE, COALESCE(ZSOURCE.ZDEVICEID,'')
		FROM ZOBJECT LEFT JOIN ZSOURCE ON ZOBJECT.ZSOURCE = ZSOURCE.Z_PK
		WHERE ZOBJECT.ZSTREAMNAME IN ('/app/usage','/display/isBacklit','/device/isLocked') AND ZOBJECT.ZSTARTDATE > %f
		ORDER BY ZOBJECT.ZSTARTDATE LIMIT 50000`, st.ScreenTime)
	rows, err := sqliteRows(db, q)
	if err != nil {
		return err
	}
	for _, r := range rows {
		if len(r) < 5 {
			continue
		}
		start, _ := strconv.ParseFloat(r[2], 64)
		end, _ := strconv.ParseFloat(r[3], 64)
		at := macEpoch.Add(time.Duration(start * float64(time.Second))).Local()
		rec := map[string]any{"src": "import", "kind": "screentime", "stream": strings.TrimPrefix(r[0], "/"), "at": at.Format(scoreStamp),
			"sec": int(end - start), "device": map[bool]string{true: "mac", false: "other"}[r[4] == ""]}
		if r[0] == "/app/usage" {
			rec["bundle"] = r[1]
			rec["cat"] = bundleCategory(r[1])
		}
		appendSignal(s.Root, rec)
		st.ScreenTime = max(st.ScreenTime, start)
	}
	return nil
}

// bundleCategory is a rough category for an app bundle id (the window and browser sensors refine it).
func bundleCategory(id string) string {
	id = strings.ToLower(id)
	for _, c := range []struct{ cat, keys string }{
		{"social", "instagram,twitter,tiktok,reddit,facebook,snapchat,threads,linkedin,bereal"},
		{"entertainment", "youtube,netflix,spotify,music,tv,twitch,hulu,disney,game,steam,podcasts"},
		{"comms", "mail,messages,mobilesms,whatsapp,discord,slack,teams,zoom,telegram,signal,facetime"},
		{"browser", "safari,chrome,firefox,arc,brave,edge,browser"},
		{"code", "ghostty,terminal,iterm,xcode,vscode,code,cursor,zed,github,dbeaver,postman"},
		{"study", "notesview,word,excel,powerpoint,pages,numbers,keynote,notion,obsidian,anki,quizlet,canvas,claude,preview,books"},
		{"news", "news,nytimes,bbc"},
		{"shopping", "amazon,ebay,shop"},
	} {
		for _, k := range strings.Split(c.keys, ",") {
			if strings.Contains(id, k) {
				return c.cat
			}
		}
	}
	return "other"
}

// ── message counts (Messages and Mail): counts per hour only, never content or senders ──

func (s *Store) importMessages(st *importState, _ SensorConfig, _ time.Time) error {
	home, _ := os.UserHomeDir()
	counted := map[string]map[string]int{} // hour stamp → app → n
	add := func(t time.Time, app string) {
		h := t.Local().Format("2006-01-02T15") + ":00:00"
		if counted[h] == nil {
			counted[h] = map[string]int{}
		}
		counted[h][app]++
	}
	var firstErr error
	rows, err := sqliteRows(filepath.Join(home, "Library", "Messages", "chat.db"),
		fmt.Sprintf("SELECT date FROM message WHERE is_from_me = 0 AND date > %d ORDER BY date LIMIT 100000", st.Messages))
	if err != nil {
		firstErr = err
	}
	for _, r := range rows {
		ns, _ := strconv.ParseInt(r[0], 10, 64)
		if ns < 1e12 { // older databases store seconds
			ns *= 1e9
		}
		add(macEpoch.Add(time.Duration(ns)), "messages")
		st.Messages = max(st.Messages, ns)
	}
	if m, _ := filepath.Glob(filepath.Join(home, "Library", "Mail", "V*", "MailData", "Envelope Index")); len(m) > 0 {
		rows, err := sqliteRows(m[len(m)-1], fmt.Sprintf("SELECT date_received FROM messages WHERE date_received > %d ORDER BY date_received LIMIT 100000", st.Mail))
		if err == nil {
			firstErr = nil
		}
		for _, r := range rows {
			sec, _ := strconv.ParseInt(r[0], 10, 64)
			add(time.Unix(sec, 0), "mail")
			st.Mail = max(st.Mail, sec)
		}
	}
	hours := make([]string, 0, len(counted))
	for h := range counted {
		hours = append(hours, h)
	}
	sort.Strings(hours)
	for _, h := range hours {
		for app, n := range counted[h] {
			appendSignal(s.Root, map[string]any{"src": "import", "kind": "messages", "app": app, "n": n, "at": h})
		}
	}
	return firstErr
}

// ── weather (Open-Meteo, no key) ──

var weatherURL = "https://api.open-meteo.com/v1/forecast"

func (s *Store) importWeather(st *importState, cfg SensorConfig, now time.Time) error {
	if cfg.Lat == 0 && cfg.Lon == 0 {
		return os.ErrNotExist // no city set on the Data tab yet
	}
	q := url.Values{}
	q.Set("latitude", strconv.FormatFloat(cfg.Lat, 'f', 2, 64)) // city level
	q.Set("longitude", strconv.FormatFloat(cfg.Lon, 'f', 2, 64))
	q.Set("daily", "temperature_2m_max,temperature_2m_min,precipitation_sum,daylight_duration,sunshine_duration,cloud_cover_mean")
	q.Set("past_days", "7")
	q.Set("forecast_days", "1")
	q.Set("timezone", "auto")
	c := http.Client{Timeout: 15 * time.Second}
	resp, err := c.Get(weatherURL + "?" + q.Encode())
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var d struct {
		Daily map[string][]any `json:"daily"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&d); err != nil {
		return err
	}
	days := d.Daily["time"]
	today := now.Format(isoDate)
	for i, v := range days {
		day, _ := v.(string)
		if day == "" || (st.Weather[day] && day != today) || day > today {
			continue
		}
		rec := map[string]any{"src": "import", "kind": "weather", "day": day, "at": day + "T12:00:00"}
		for k, vals := range d.Daily {
			if k != "time" && i < len(vals) && vals[i] != nil {
				rec[k] = vals[i]
			}
		}
		if day != today { // today's values change until it's over: imported again tomorrow
			st.Weather[day] = true
		}
		appendSignal(s.Root, rec)
	}
	return nil
}

// ── Health (Health Auto Export JSON, for the Apple Watch) ──

// healthExport is the Health Auto Export JSON format (version 2): metrics with timed samples.
type healthExport struct {
	Data struct {
		Metrics []struct {
			Name  string           `json:"name"`
			Units string           `json:"units"`
			Data  []map[string]any `json:"data"`
		} `json:"metrics"`
		Workouts []map[string]any `json:"workouts"`
	} `json:"data"`
}

// healthMetrics are the metrics kept, by Health Auto Export name.
var healthMetrics = map[string]string{
	"heart_rate": "hr", "heart_rate_variability": "hrv", "resting_heart_rate": "rhr", "step_count": "steps",
	"sleep_analysis": "sleep", "apple_sleeping_wrist_temperature": "wrist_temp", "blood_oxygen_saturation": "spo2",
	"respiratory_rate": "resp", "active_energy": "active_kcal", "apple_stand_hour": "stand", "walking_heart_rate_average": "walk_hr",
	"time_in_daylight": "daylight_min", "apple_exercise_time": "exercise_min",
}

// parseHealthTime reads Health Auto Export dates ("2026-10-06 10:00:00 -0400").
func parseHealthTime(v any) (time.Time, bool) {
	s, _ := v.(string)
	for _, f := range []string{"2006-01-02 15:04:05 -0700", time.RFC3339, "2006-01-02 15:04:05"} {
		if t, err := time.Parse(f, s); err == nil {
			return t.Local(), true
		}
	}
	return time.Time{}, false
}

// parseHealth turns one export file into records (one per sample; sleep stages as intervals).
func parseHealth(b []byte) ([]map[string]any, error) {
	var h healthExport
	if err := json.Unmarshal(b, &h); err != nil {
		return nil, err
	}
	var out []map[string]any
	for _, m := range h.Data.Metrics {
		key, ok := healthMetrics[m.Name]
		if !ok {
			continue
		}
		for _, d := range m.Data {
			t, ok := parseHealthTime(d["date"])
			if !ok {
				t, ok = parseHealthTime(d["startDate"])
			}
			if !ok {
				continue
			}
			rec := map[string]any{"src": "import", "kind": "health", "metric": key, "at": t.Format(scoreStamp)}
			for _, f := range []string{"qty", "Avg", "Min", "Max"} {
				if v, ok := d[f].(float64); ok {
					rec[strings.ToLower(f)] = v
				}
			}
			if key == "sleep" { // aggregated sleep: hours per stage, and the window
				for _, f := range []string{"asleep", "core", "deep", "rem", "awake", "inBed", "totalSleep"} {
					if v, ok := d[f].(float64); ok {
						rec[strings.ToLower(f)] = v
					}
				}
				if e, ok := parseHealthTime(d["sleepEnd"]); ok {
					rec["end"] = e.Format(scoreStamp)
				}
				if s0, ok := parseHealthTime(d["sleepStart"]); ok {
					rec["start"] = s0.Format(scoreStamp)
				}
			}
			out = append(out, rec)
		}
	}
	for _, w := range h.Data.Workouts {
		t, ok := parseHealthTime(w["start"])
		if !ok {
			continue
		}
		rec := map[string]any{"src": "import", "kind": "health", "metric": "workout", "at": t.Format(scoreStamp), "name": w["name"]}
		if e, ok := parseHealthTime(w["end"]); ok {
			rec["min"] = int(e.Sub(t).Minutes())
		}
		out = append(out, rec)
	}
	return out, nil
}

func (s *Store) importHealth(st *importState, cfg SensorConfig, _ time.Time) error {
	if cfg.HealthDir == "" {
		return os.ErrNotExist // the watch isn't set up yet
	}
	files, err := filepath.Glob(filepath.Join(cfg.HealthDir, "*.json"))
	if err != nil || len(files) == 0 {
		return os.ErrNotExist
	}
	sort.Strings(files)
	for _, f := range files {
		info, err := os.Stat(f)
		if err != nil || st.Health[filepath.Base(f)] == int(info.Size()) {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		recs, err := parseHealth(b)
		if err != nil {
			continue
		}
		for _, r := range recs {
			appendSignal(s.Root, r)
		}
		st.Health[filepath.Base(f)] = int(info.Size())
	}
	return nil
}
