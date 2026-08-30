package textnorm

import (
	"regexp"
	"strings"
)

// punycode constants from RFC 3492, section 5.
const (
	punyBase  = 36
	punyTmin  = 1
	punyTmax  = 26
	punySkew  = 38
	punyDamp  = 700
	punyBias  = 72
	punyInitN = 128
)

// punyAdapt implements the bias adaptation function from RFC 3492, 6.1.
func punyAdapt(delta, numPoints int, firstTime bool) int {
	if firstTime {
		delta /= punyDamp
	} else {
		delta /= 2
	}
	delta += delta / numPoints
	for delta > ((punyBase - punyTmin) * punyTmax / 2) {
		delta /= punyBase - punyTmin
	}
	return delta + ((punyBase-punyTmin+1)*delta)/(delta+punySkew)
}

// punyDigit maps an ASCII code point to its Punycode digit value.
func punyDigit(b byte) (int, bool) {
	switch {
	case b >= 'a' && b <= 'z':
		return int(b - 'a'), true
	case b >= 'A' && b <= 'Z':
		return int(b - 'A'), true
	case b >= '0' && b <= '9':
		return int(b-'0') + 26, true
	default:
		return 0, false
	}
}

// punyDecodeBody decodes one Punycode body (RFC 3492, 6.2) into Unicode.
func punyDecodeBody(input string) ([]rune, bool) {
	n, i, bias := rune(punyInitN), 0, punyBias
	var output []rune

	b := strings.LastIndex(input, "-")
	if b > 0 {
		for _, r := range input[:b] {
			if r > 0x7F {
				return nil, false
			}
			output = append(output, r)
		}
	}
	in := 0
	if b >= 0 {
		in = b + 1
	}

	for in < len(input) {
		oldi := i
		w := 1
		for k := punyBase; ; k += punyBase {
			if in >= len(input) {
				return nil, false
			}
			digit, ok := punyDigit(input[in])
			in++
			if !ok {
				return nil, false
			}
			i += digit * w
			t := punyTmax
			if k <= bias {
				t = punyTmin
			} else if k-bias < punyTmax {
				t = k - bias
			}
			if digit < t {
				break
			}
			w *= punyBase - t
		}
		out := len(output) + 1
		delta := i - oldi
		if delta < 0 {
			return nil, false
		}
		bias = punyAdapt(delta, out, oldi == 0)
		char := n + rune(delta/out)
		if char > 0x10FFFF {
			return nil, false
		}
		pos := delta % out
		output = append(output, 0)
		copy(output[pos+1:], output[pos:])
		output[pos] = char
		n += rune(delta / out)
		i = pos
	}
	return output, true
}

// idnLabel matches Punycode (ACE) labels as used in internationalized
// domain names and URLs.
var idnLabel = regexp.MustCompile(`\b(?:xn--[0-9A-Za-z-]+\.)+[A-Za-z]{2,}\b|(?:[A-Za-z0-9-]+\.)+xn--[0-9A-Za-z-]+`)

// HasPunycodeLabels reports whether the text contains xn-- labels.
func HasPunycodeLabels(s string) bool {
	return strings.Contains(s, "xn--")
}

// DecodeIDNLabels unfolds every xn-- label into its Unicode form. Punycode
// is a reversible encoding per RFC 3492, so the displayed domain is the
// same name the ASCII form represents; malformed labels stay literal.
func DecodeIDNLabels(s string) string {
	if !HasPunycodeLabels(s) {
		return s
	}
	return idnLabel.ReplaceAllStringFunc(s, func(host string) string {
		labels := strings.Split(host, ".")
		out := make([]string, len(labels))
		for i, label := range labels {
			if strings.HasPrefix(label, "xn--") {
				decoded, ok := punyDecodeBody(label[4:])
				if !ok {
					return host
				}
				out[i] = string(decoded)
				continue
			}
			out[i] = label
		}
		return strings.Join(out, ".")
	})
}
