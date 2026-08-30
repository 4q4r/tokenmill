package textnorm

import (
	"encoding/base32"
	"regexp"
	"strings"
)

// base32Decode is the stdlib decoder used for tolerant base32 validation.
var base32Decode = base32.StdEncoding.DecodeString

// base32Run matches a run that looks like base32 payload with optional
// embedded whitespace (A–Z, 2–7, padding '='), at least 64 characters.
var base32Run = regexp.MustCompile(`[A-Z2-7=\r\n\t ]{64,}`)

// decodeBase32Tolerant decodes s under standard base32 with padding
// tolerance; any successful decode counts.
func decodeBase32Tolerant(s string) ([]byte, bool) {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return nil, false
	}
	if data, err := base32Decode(trimmed); err == nil && len(data) > 0 {
		return data, true
	}
	padded := trimmed
	if pad := len(padded) % 8; pad != 0 {
		padded += strings.Repeat("=", 8-pad)
	}
	if data, err := base32Decode(padded); err == nil && len(data) > 0 {
		return data, true
	}
	return nil, false
}

// CompactBase32 removes whitespace inside decodable base32 runs. The check
// mirrors base64 compaction: strip only when the run decodes identically
// before and after stripping.
func CompactBase32(s string) string {
	if !strings.ContainsAny(s, " \t\r\n") {
		return s
	}
	return rewriteEncodedRuns(s, base32Run, func(run string) string {
		original, ok := decodeBase32Tolerant(run)
		if !ok {
			return run
		}
		stripped := stripRunWhitespace(run)
		if stripped == run {
			return run
		}
		strippedDecoded, ok := decodeBase32Tolerant(stripped)
		if !ok || string(strippedDecoded) != string(original) {
			return run
		}
		return stripped
	})
}

// HasCompactableBase32 reports whether a base32 run would compact.
func HasCompactableBase32(s string) bool {
	return CompactBase32(s) != s
}

// cdataBlock matches one CDATA section.
var cdataBlock = regexp.MustCompile(`<!\[CDATA\[(.*?)\]\]>`)

// HasCDATA reports whether the text contains a CDATA section.
func HasCDATA(s string) bool {
	return strings.Contains(s, "<![CDATA[")
}

// UnwrapCDATA replaces each CDATA section with the literal text it carries.
// XML parsers deliver exactly that text to applications, so the visible and
// semantic content is unchanged. Sections containing the terminator sequence
// are impossible by construction and therefore never matched.
func UnwrapCDATA(s string) string {
	if !HasCDATA(s) {
		return s
	}
	return cdataBlock.ReplaceAllString(s, "$1")
}

// headerLine matches a plausible HTTP header line: known-name colon value.
var headerLine = regexp.MustCompile(`(?m)^([A-Za-z0-9-]+):[ \t]*(.*?)[ \t]*$`)

// canonicalHeaderNames maps lower-case header names to their canonical
// spelling. Only names from this fixed set are rewritten, so ordinary prose
// lines like `note: something` can never change.
var canonicalHeaderNames = map[string]string{
	"accept":            "Accept",
	"accept-encoding":   "Accept-Encoding",
	"accept-language":   "Accept-Language",
	"authorization":     "Authorization",
	"cache-control":     "Cache-Control",
	"connection":        "Connection",
	"content-length":    "Content-Length",
	"content-type":      "Content-Type",
	"cookie":            "Cookie",
	"date":              "Date",
	"etag":              "ETag",
	"expires":           "Expires",
	"host":              "Host",
	"if-modified-since": "If-Modified-Since",
	"if-none-match":     "If-None-Match",
	"last-modified":     "Last-Modified",
	"location":          "Location",
	"origin":            "Origin",
	"referer":           "Referer",
	"retry-after":       "Retry-After",
	"server":            "Server",
	"set-cookie":        "Set-Cookie",
	"user-agent":        "User-Agent",
	"www-authenticate":  "WWW-Authenticate",
	"x-request-id":      "X-Request-ID",
	"x-trace-id":        "X-Trace-Id",
}

// HasLooseHeaders reports whether a canonicalizable header line is present.
func HasLooseHeaders(s string) bool {
	for _, match := range headerLine.FindAllStringSubmatch(s, -1) {
		name := strings.ToLower(match[1])
		if _, known := canonicalHeaderNames[name]; known {
			return true
		}
	}
	return false
}

// NormalizeHeaders canonicalizes the spelling of known HTTP header names and
// trims whitespace around their values. HTTP/1.1 header names are
// case-insensitive, so the canonical form carries identical meaning.
func NormalizeHeaders(s string) string {
	return headerLine.ReplaceAllStringFunc(s, func(line string) string {
		match := headerLine.FindStringSubmatch(line)
		if match == nil {
			return line
		}
		name := strings.ToLower(match[1])
		canonical, known := canonicalHeaderNames[name]
		if !known {
			return line
		}
		return canonical + ": " + strings.TrimSpace(match[2])
	})
}
