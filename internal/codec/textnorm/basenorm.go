package textnorm

import (
	"regexp"
	"strings"
)

// base64urlAlphabet matches base64url payloads (with `-` and `_`).
var base64urlAlphabet = regexp.MustCompile(`[A-Za-z0-9_-]{16,}`)

// HasBase64URL reports whether the text contains base64url payloads.
func HasBase64URL(s string) bool {
	return base64urlAlphabet.MatchString(s)
}

// NormalizeBase64URL converts base64url alphabet characters (`-` → `+`,
// `_` → `/`) to standard base64. Both alphabets decode to the same bytes.
func NormalizeBase64URL(s string) string {
	return base64urlAlphabet.ReplaceAllStringFunc(s, func(run string) string {
		if !strings.ContainsAny(run, "-_") {
			return run
		}
		return strings.NewReplacer("-", "+", "_", "/").Replace(run)
	})
}

// base32Lower matches lowercase base32 payloads (non-standard case).
var base32Lower = regexp.MustCompile(`\b[a-z2-7]{16,}\b`)

// HasLowercaseBase32 reports whether the text contains lowercase base32.
func HasLowercaseBase32(s string) bool {
	return base32Lower.MatchString(s)
}

// NormalizeBase32Case uppercases lowercase base32 payloads. Base32 decoding
// is case-insensitive per RFC 4648, so the payload is unchanged.
func NormalizeBase32Case(s string) string {
	return base32Lower.ReplaceAllStringFunc(s, strings.ToUpper)
}

// semverLeadZero matches version strings with leading zeros in components.
var semverLeadZero = regexp.MustCompile(`\bv(\d{1,2})\.0*(\d{1,2})\.0*(\d{1,2})\b`)

// HasNonCanonicalSemver reports whether the text contains version strings
// with leading zeros after the `v` prefix.
func HasNonCanonicalSemver(s string) bool {
	return semverLeadZero.MatchString(s)
}

// CanonicalizeSemver strips leading zeros from `v`-prefixed semver
// components (`v01.02.03` → `v1.2.3`). The version value is unchanged.
func CanonicalizeSemver(s string) string {
	return semverLeadZero.ReplaceAllStringFunc(s, func(v string) string {
		match := semverLeadZero.FindStringSubmatch(v)
		if match == nil {
			return v
		}
		major, _ := strconvUnpad(match[1])
		minor, _ := strconvUnpad(match[2])
		patch, _ := strconvUnpad(match[3])
		return "v" + major + "." + minor + "." + patch
	})
}

func strconvUnpad(s string) (string, bool) {
	trimmed := strings.TrimLeft(s, "0")
	if trimmed == "" {
		return "0", true
	}
	return trimmed, true
}
