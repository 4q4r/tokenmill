package textnorm

import (
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ipv6Candidate matches colon-bearing tokens that could be IPv6 addresses.
var ipv6Candidate = regexp.MustCompile(`(?:[0-9A-Fa-f]{0,4}:){2,7}[0-9A-Fa-f.:]{1,4}`)

// HasNonCanonicalIPv6 reports whether the text contains an IPv6 address
// whose canonical RFC 5952 form differs from what is written.
func HasNonCanonicalIPv6(s string) bool {
	for _, candidate := range ipv6Candidate.FindAllString(s, -1) {
		ip := net.ParseIP(candidate)
		if ip == nil || ip.To16() == nil || !strings.Contains(candidate, ":") {
			continue
		}
		if strings.Contains(candidate, "%") {
			continue // zone identifiers are out of scope
		}
		if ip.String() != candidate {
			return true
		}
	}
	return false
}

// CanonicalizeIPv6 rewrites every IPv6 address to its RFC 5952 canonical
// text form (lowercase, no leading zeros, maximal :: compression). The
// canonical form denotes the identical address.
func CanonicalizeIPv6(s string) string {
	return ipv6Candidate.ReplaceAllStringFunc(s, func(candidate string) string {
		if strings.Contains(candidate, "%") {
			return candidate
		}
		ip := net.ParseIP(candidate)
		if ip == nil || ip.To16() == nil || !strings.Contains(candidate, ":") {
			return candidate
		}
		return ip.String()
	})
}

// isoCandidate matches RFC 3339 / ISO 8601 timestamps with an explicit zone.
var isoCandidate = regexp.MustCompile(`\d{4}-\d{2}-\d{2}[Tt]\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:[Zz]|[+-]\d{2}:?\d{2})`)

// HasNonCanonicalTimestamp reports whether the text contains an explicit-zone
// timestamp whose canonical UTC form differs from what is written.
func HasNonCanonicalTimestamp(s string) bool {
	return CanonicalizeTimestamps(s) != s
}

// CanonicalizeTimestamps rewrites explicit-zone RFC 3339 timestamps to one
// canonical UTC form: `2006-01-02T15:04:05Z` with trailing-zero second
// fractions trimmed. All variants of the same instant — lowercase t/z,
// numeric offsets, `.000` fractions — collapse to one representation.
func CanonicalizeTimestamps(s string) string {
	return isoCandidate.ReplaceAllStringFunc(s, func(candidate string) string {
		normalized := candidate
		normalized = strings.Replace(normalized, "t", "T", 1)
		normalized = strings.Replace(normalized, "z", "Z", 1)
		if len(normalized) >= 6 {
			offset := normalized[len(normalized)-6:]
			if (offset[0] == '+' || offset[0] == '-') && offset[3] != ':' {
				normalized = normalized[:len(normalized)-5] + ":" + normalized[len(normalized)-5:]
			}
		}
		parsed, err := time.Parse(time.RFC3339Nano, normalized)
		if err != nil {
			return candidate
		}
		return parsed.UTC().Format("2006-01-02T15:04:05Z07:00")
	})
}

// epochCandidate matches standalone 10-digit epoch seconds (2010–2033) and
// 13-digit epoch milliseconds (2001–2033).
var epochCandidate = regexp.MustCompile(`\b(1[3-9]\d{8}|1[67]\d{11})\b`)

// HasEpochTimestamps reports whether the text contains standalone epoch
// numbers inside the supported window.
func HasEpochTimestamps(s string) bool {
	return epochToISO(s) != s
}

// EpochToISO converts standalone epoch numbers to UTC ISO 8601 timestamps,
// which models read as dates instead of opaque digit strings. The supported
// window (2010–2033) keeps ordinary counters and identifiers out; both forms
// denote the identical instant.
func EpochToISO(s string) string {
	return epochToISO(s)
}

func epochToISO(s string) string {
	return epochCandidate.ReplaceAllStringFunc(s, func(token string) string {
		if len(token) == 10 {
			seconds, err := strconv.ParseInt(token, 10, 64)
			if err != nil {
				return token
			}
			return time.Unix(seconds, 0).UTC().Format("2006-01-02T15:04:05Z")
		}
		millis, err := strconv.ParseInt(token, 10, 64)
		if err != nil {
			return token
		}
		return time.UnixMilli(millis).UTC().Format("2006-01-02T15:04:05.000Z")
	})
}
