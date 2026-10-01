package main

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

const isoDate = "2006-01-02"

// naturalDateRe finds "@word" tokens at the start of the text or after
// whitespace or an opening bracket (so e-mail addresses are left alone).
var naturalDateRe = regexp.MustCompile(`(^|[\s(\[])@([A-Za-z0-9/+]+)`)

var weekdays = map[string]time.Weekday{
	"sun": time.Sunday, "mon": time.Monday, "tue": time.Tuesday, "wed": time.Wednesday,
	"thu": time.Thursday, "fri": time.Friday, "sat": time.Saturday,
	"sunday": time.Sunday, "monday": time.Monday, "tuesday": time.Tuesday, "tues": time.Tuesday,
	"wednesday": time.Wednesday, "thursday": time.Thursday, "thur": time.Thursday, "thurs": time.Thursday,
	"friday": time.Friday, "saturday": time.Saturday,
}

var months = map[string]time.Month{
	"jan": 1, "feb": 2, "mar": 3, "apr": 4, "may": 5, "jun": 6, "jul": 7, "aug": 8,
	"sep": 9, "sept": 9, "oct": 10, "nov": 11, "dec": 12,
	"january": 1, "february": 2, "march": 3, "april": 4, "june": 6, "july": 7, "august": 8,
	"september": 9, "october": 10, "november": 11, "december": 12,
}

var (
	monthDayRe = regexp.MustCompile(`^([a-z]+)(\d{1,2})$`)
	slashRe    = regexp.MustCompile(`^(\d{1,2})/(\d{1,2})$`)
	relRe      = regexp.MustCompile(`^\+(\d{1,3})([dw])$`)
)

func midnight(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}

// ResolveNaturalDate turns one token (without the @) into a date relative to
// now. It reports false for anything it does not recognise.
//
//	today, tomorrow
//	mon … sun     next occurrence, always after today
//	oct6, 10/6    that day this year, or next year if it has passed
//	+3d, +2w      days or weeks from today
func ResolveNaturalDate(tok string, now time.Time) (time.Time, bool) {
	today := midnight(now)
	tok = strings.ToLower(tok)
	switch tok {
	case "today":
		return today, true
	case "tomorrow", "tmr", "tmrw":
		return today.AddDate(0, 0, 1), true
	}
	if wd, ok := weekdays[tok]; ok {
		diff := (int(wd) - int(today.Weekday()) + 7) % 7
		if diff == 0 {
			diff = 7
		}
		return today.AddDate(0, 0, diff), true
	}
	if m := relRe.FindStringSubmatch(tok); m != nil {
		n, _ := strconv.Atoi(m[1])
		if m[2] == "w" {
			n *= 7
		}
		return today.AddDate(0, 0, n), true
	}
	var month time.Month
	var day int
	if m := monthDayRe.FindStringSubmatch(tok); m != nil {
		mo, ok := months[m[1]]
		if !ok {
			return time.Time{}, false
		}
		month = mo
		day, _ = strconv.Atoi(m[2])
	} else if m := slashRe.FindStringSubmatch(tok); m != nil {
		mo, _ := strconv.Atoi(m[1])
		if mo < 1 || mo > 12 {
			return time.Time{}, false
		}
		month = time.Month(mo)
		day, _ = strconv.Atoi(m[2])
	} else {
		return time.Time{}, false
	}
	if day < 1 || day > 31 {
		return time.Time{}, false
	}
	// Feb 29 may need a few years to come round again.
	for y := today.Year(); y <= today.Year()+4; y++ {
		d := time.Date(y, month, day, 0, 0, 0, 0, today.Location())
		if d.Month() != month {
			if month == time.February && day == 29 {
				continue
			}
			return time.Time{}, false
		}
		if !d.Before(today) {
			return d, true
		}
	}
	return time.Time{}, false
}

// ConvertNaturalDates rewrites every natural "@date" in text to @YYYY-MM-DD.
// Unknown forms are left exactly as they were.
func ConvertNaturalDates(text string, now time.Time) string {
	var b strings.Builder
	last := 0
	for _, m := range naturalDateRe.FindAllStringSubmatchIndex(text, -1) {
		tokStart, tokEnd := m[4], m[5]
		// "@fri-ish" or "@mon_x" is not a date
		if tokEnd < len(text) && strings.ContainsRune("-_@", rune(text[tokEnd])) {
			continue
		}
		d, ok := ResolveNaturalDate(text[tokStart:tokEnd], now)
		if !ok {
			continue
		}
		b.WriteString(text[last:tokStart])
		b.WriteString(d.Format(isoDate))
		last = tokEnd
	}
	b.WriteString(text[last:])
	return b.String()
}
