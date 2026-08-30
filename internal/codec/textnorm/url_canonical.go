package textnorm

import (
	"net/url"
	"regexp"
	"strings"
)

// trackingParams are URL parameters that carry analytics/session data but
// never change the page content the model needs to reason about.
var trackingParams = map[string]struct{}{
	"utm_source": {}, "utm_medium": {}, "utm_campaign": {}, "utm_term": {},
	"utm_content": {}, "utm_id": {}, "fbclid": {}, "gclid": {}, "msclkid": {},
	"session_id": {}, "phpsessid": {}, "jsessionid": {}, "sid": {},
	"_ga": {}, "mc_eid": {}, "yclid": {}, "vbref": {},
}

var defaultPorts = map[string]string{"https": "443", "http": "80"}

// HasCanonicalizableURL reports whether the text contains a URL that
// canonicalization would simplify (tracking params, default port, scheme
// or host case).
func HasCanonicalizableURL(s string) bool {
	urlRe := regexp.MustCompile(`https?://[^\s<>"'` + "`" + `]+`)
	for _, candidate := range urlRe.FindAllString(s, -1) {
		if canonicalizeSingleURL(candidate) != candidate {
			return true
		}
	}
	return false
}

// CanonicalizeURL rewrites every URL to its canonical form: lowercase
// scheme and host, default port stripped, tracking params removed, query
// params sorted, dot-segments resolved. The URL fetches the same resource.
func CanonicalizeURL(s string) string {
	urlRe := regexp.MustCompile(`https?://[^\s<>"'` + "`" + `]+`)
	return urlRe.ReplaceAllStringFunc(s, canonicalizeSingleURL)
}

func canonicalizeSingleURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	// Lowercase scheme and host.
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	parsed.Host = strings.ToLower(parsed.Host)

	// Strip default port.
	if port, ok := defaultPorts[parsed.Scheme]; ok && parsed.Port() == port {
		parsed.Host = parsed.Hostname()
	}

	// Resolve dot-segments in the path.
	parsed.Path = resolveDotSegments(parsed.Path)

	// Filter tracking params and sort the rest.
	query := parsed.Query()
	filtered := make(map[string][]string)
	for key, values := range query {
		if _, tracked := trackingParams[strings.ToLower(key)]; tracked {
			continue
		}
		filtered[key] = values
	}
	parsed.RawQuery = encodeSortedQuery(filtered)

	return parsed.String()
}

func resolveDotSegments(path string) string {
	var out []string
	for _, segment := range strings.Split(path, "/") {
		switch segment {
		case ".":
			continue
		case "..":
			if len(out) > 1 {
				out = out[:len(out)-1]
			}
		default:
			out = append(out, segment)
		}
	}
	return strings.Join(out, "/")
}

func encodeSortedQuery(params map[string][]string) string {
	if len(params) == 0 {
		return ""
	}
	keys := make([]string, 0, len(params))
	for key := range params {
		keys = append(keys, key)
	}
	sortStrings(keys)
	var parts []string
	for _, key := range keys {
		for _, value := range params[key] {
			parts = append(parts, url.QueryEscape(key)+"="+url.QueryEscape(value))
		}
	}
	return strings.Join(parts, "&")
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
