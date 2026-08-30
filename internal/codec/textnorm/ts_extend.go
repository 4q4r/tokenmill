package textnorm

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// rfc2822Date matches RFC 2822 dates: `Mon, 29 Aug 2026 10:00:00 GMT`.
var rfc2822Date = regexp.MustCompile(
	`(?:Mon|Tue|Wed|Thu|Fri|Sat|Sun), \d{2} [A-Z][a-z]{2} \d{4} \d{2}:\d{2}:\d{2} [+-]\d{4}|GMT`)

// ampmTime matches `10:00 PM` / `10:00:30 AM` (optional seconds).
var ampmTime = regexp.MustCompile(`\b(\d{1,2}):(\d{2})(?::(\d{2}))?\s*([APap])[Mm]\b`)

// tzNameOffset matches `(GMT+3)` / `(UTC-5)` style zone annotations.
var tzNameOffset = regexp.MustCompile(`\((?:GMT|UTC)([+-]\d{1,2})\)`)

// isoBasic matches basic-format ISO timestamps without separators.
var isoBasic = regexp.MustCompile(`\b(\d{4})(\d{2})(\d{2})T(\d{2})(\d{2})(\d{2})Z\b`)

// HasExtendedTimestampForms reports whether the text contains timestamp
// formats that ts-canonical normalization would rewrite.
func HasExtendedTimestampForms(s string) bool {
	return rfc2822Date.MatchString(s) ||
		ampmTime.MatchString(s) ||
		tzNameOffset.MatchString(s) ||
		isoBasic.MatchString(s) ||
		strings.Contains(s, " GMT") ||
		strings.Contains(s, " UTC)") ||
		HasNonCanonicalTimestamp(s)
}

// CanonicalizeTimestampsExtended rewrites all recognized timestamp formats
// (RFC 2822, AM/PM, GMT/UTC names, basic ISO, plus the RFC 3339 variants
// already handled) to canonical `2006-01-02T15:04:05Z` form. All forms
// denote the same instant.
func CanonicalizeTimestampsExtended(s string) string {
	s = canonicalizeAMPM(s)
	s = canonicalizeTZNames(s)
	s = canonicalizeBasicISO(s)
	s = canonicalizeRFC2822(s)
	return CanonicalizeTimestamps(s)
}

func canonicalizeAMPM(s string) string {
	return ampmTime.ReplaceAllStringFunc(s, func(m string) string {
		match := ampmTime.FindStringSubmatch(m)
		hour, _ := strconv.Atoi(match[1])
		minute := match[2]
		second := match[3]
		period := strings.ToUpper(match[4])
		if period == "P" && hour < 12 {
			hour += 12
		}
		if period == "A" && hour == 12 {
			hour = 0
		}
		result := pad2(hour) + ":" + minute
		if second != "" {
			result += ":" + second
		}
		return result
	})
}

func canonicalizeTZNames(s string) string {
	return tzNameOffset.ReplaceAllStringFunc(s, func(m string) string {
		match := tzNameOffset.FindStringSubmatch(m)
		hours, _ := strconv.Atoi(match[1])
		sign := "+"
		if hours < 0 {
			sign = "-"
			hours = -hours
		}
		return sign + pad2(hours) + ":00"
	})
}

func canonicalizeBasicISO(s string) string {
	return isoBasic.ReplaceAllStringFunc(s, func(m string) string {
		match := isoBasic.FindStringSubmatch(m)
		return match[1] + "-" + match[2] + "-" + match[3] + "T" +
			match[4] + ":" + match[5] + ":" + match[6] + "Z"
	})
}

var rfc2822Layout = "Mon, 02 Jan 2006 15:04:05 MST"

func canonicalizeRFC2822(s string) string {
	loc := rfc2822Date.FindString(s)
	if loc == "" {
		return s
	}
	parsed, err := time.Parse(rfc2822Layout, loc)
	if err != nil {
		return s
	}
	return strings.Replace(s, loc, parsed.UTC().Format("2006-01-02T15:04:05Z"), 1)
}

func pad2(v int) string {
	if v < 10 {
		return "0" + strconv.Itoa(v)
	}
	return strconv.Itoa(v)
}
