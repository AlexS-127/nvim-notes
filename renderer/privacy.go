package main

import (
	"compress/gzip"
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// The privacy audit: .signals and .features may only hold numbers, booleans, categories, hashes
// and a few identifiers (app names, bundle ids, folder names, note paths for revision). It scans
// every string and flags anything that looks like typed text, a URL, a file path or image data.
// `notesview data audit`, the Data tab, and a test that plants bad records.

// auditKeys are the string fields that may appear, with a check for each value.
var (
	hexRe      = regexp.MustCompile(`^[0-9a-f]{8,16}$`)
	stampRe    = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}(T\d{2}:\d{2}:\d{2})?$`)
	wordRe     = regexp.MustCompile(`^[A-Za-z0-9 ._+\-]{0,48}$`)       // app names, categories, states
	bundleRe   = regexp.MustCompile(`^[A-Za-z0-9._\-]{0,96}$`)           // com.example.app
	notePathRe = regexp.MustCompile(`^[A-Za-z0-9 ._\-]+(/[A-Za-z0-9 ._\-]+)*\.md$`) // revision: act200/accruals.md
	urlRe      = regexp.MustCompile(`(?i)(https?://|www\.|\.com/|file://)`)
)

func auditValue(key, v string) bool {
	if v == "" {
		return true
	}
	if urlRe.MatchString(v) {
		return false
	}
	switch key {
	case "at", "due", "day", "prompted", "end", "start", "built":
		return stampRe.MatchString(v)
	case "title_hash", "domain_hash", "net_hash", "note", "hash":
		return hexRe.MatchString(v) || v == "none"
	case "bundle":
		return bundleRe.MatchString(v)
	case "revision":
		return notePathRe.MatchString(v)
	case "stream": // Screen Time stream names: app/usage, display/isBacklit, device/isLocked
		return regexp.MustCompile(`^[a-z]+/[A-Za-z]+$`).MatchString(v)
	}
	return wordRe.MatchString(v) && len(strings.Fields(v)) <= 6
}

// AuditIssue is one suspicious value.
type AuditIssue struct {
	File  string `json:"file"`
	Line  int    `json:"line"`
	Key   string `json:"key"`
	Value string `json:"value"` // shortened
}

func auditRecord(rec map[string]any, prefix string, add func(key, val string)) {
	for k, v := range rec {
		key := k
		if prefix != "" {
			key = prefix
		}
		switch x := v.(type) {
		case string:
			if !auditValue(key, x) {
				add(key, x)
			}
		case map[string]any:
			// nested objects (feature rows): keys are channel names; check the values with their own key
			for kk, vv := range x {
				if sv, ok := vv.(string); ok && !auditValue(kk, sv) {
					add(k+"."+kk, sv)
				}
				if !wordRe.MatchString(kk) {
					add(k+" (key)", kk)
				}
			}
		case []any:
			for _, e := range x {
				if sv, ok := e.(string); ok && !auditValue(key, sv) {
					add(key, sv)
				}
			}
		}
	}
}

// Audit scans .signals/*.jsonl and .features/*.jsonl.gz.
func (s *Store) Audit() ([]AuditIssue, int) {
	var issues []AuditIssue
	lines := 0
	scan := func(path string, r *bufio.Scanner) {
		n := 0
		for r.Scan() {
			n++
			lines++
			var rec map[string]any
			if json.Unmarshal(r.Bytes(), &rec) != nil {
				continue
			}
			auditRecord(rec, "", func(k, v string) {
				if len(issues) < 200 {
					if len(v) > 40 {
						v = v[:40] + "…"
					}
					issues = append(issues, AuditIssue{File: filepath.Base(path), Line: n, Key: k, Value: v})
				}
			})
		}
	}
	sig, _ := filepath.Glob(s.signalPath("*.jsonl"))
	for _, p := range sig {
		f, err := os.Open(p)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 64*1024), 4<<20)
		scan(p, sc)
		f.Close()
	}
	feats, _ := filepath.Glob(filepath.Join(s.Root, featuresDir, "*.jsonl.gz"))
	for _, p := range feats {
		f, err := os.Open(p)
		if err != nil {
			continue
		}
		if zr, err := gzip.NewReader(f); err == nil {
			sc := bufio.NewScanner(zr)
			sc.Buffer(make([]byte, 64*1024), 8<<20)
			scan(p, sc)
		}
		f.Close()
	}
	sort.SliceStable(issues, func(i, j int) bool { return issues[i].File < issues[j].File })
	return issues, lines
}

// auditSummary is one line for the CLI and the Data tab.
func auditSummary(issues []AuditIssue, lines int) string {
	if len(issues) == 0 {
		return fmt.Sprintf("clean: %d lines checked, only numbers, categories and hashes", lines)
	}
	return fmt.Sprintf("%d suspicious values in %d lines", len(issues), lines)
}
