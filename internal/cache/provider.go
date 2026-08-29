package cache

import (
	"strings"
)

// ModelFamily groups model identifiers by their prompt-caching behavior.
// Membership is decided by well-known model-name prefixes only; anything
// unrecognized stays unknown so adapters never fabricate provider metadata.
type ModelFamily string

const (
	// FamilyAnthropic models take explicit ephemeral cache breakpoints.
	FamilyAnthropic ModelFamily = "anthropic"
	// FamilyOpenAI models cache prefixes implicitly; explicit markers are
	// unnecessary.
	FamilyOpenAI ModelFamily = "openai"
	// FamilyGoogle models cache prefixes implicitly; explicit markers are
	// unnecessary.
	FamilyGoogle ModelFamily = "google"
	// FamilyUnknown means the caching model is not recognized. Adapters must
	// not place metadata for unknown families.
	FamilyUnknown ModelFamily = "unknown"
)

// MaxAnthropicBreakpoints is the documented per-request limit of explicit
// cache breakpoints for the Anthropic family.
const MaxAnthropicBreakpoints = 4

// DetectModelFamily maps a model identifier to its prompt-caching family.
func DetectModelFamily(model string) ModelFamily {
	name := strings.ToLower(strings.TrimSpace(model))
	switch {
	case strings.HasPrefix(name, "claude"):
		return FamilyAnthropic
	case strings.HasPrefix(name, "gpt"), strings.HasPrefix(name, "chatgpt"),
		strings.HasPrefix(name, "o1"), strings.HasPrefix(name, "o3"), strings.HasPrefix(name, "o4"):
		return FamilyOpenAI
	case strings.HasPrefix(name, "gemini"):
		return FamilyGoogle
	default:
		return FamilyUnknown
	}
}

// PlanBreakpoints returns a copy of messages with CacheControl metadata placed
// according to the model family's caching model. The input slice is never
// mutated.
//
//   - FamilyAnthropic: at most MaxAnthropicBreakpoints ephemeral breakpoints,
//     spread deterministically over the stable region — every message in the
//     last stableTail positions is treated as volatile and never marked.
//   - FamilyOpenAI and FamilyGoogle: implicit prefix caching; no metadata is
//     placed because providers ignore or reject explicit markers.
//   - FamilyUnknown: no metadata is placed (fail-safe).
func PlanBreakpoints(family ModelFamily, messages []Message, stableTail int) []Message {
	out := make([]Message, len(messages))
	copy(out, messages)
	if family != FamilyAnthropic {
		return out
	}
	limit := len(out) - stableTail
	if limit <= 0 {
		return out
	}
	// Spread up to MaxAnthropicBreakpoints breakpoints evenly over the stable
	// boundaries 2..limit (prefix lengths). Boundary 1 — a prefix of only the
	// first message — is deliberately skipped: it adds little cache value.
	count := MaxAnthropicBreakpoints
	if limit-1 < count {
		count = limit - 1
	}
	for i := 1; i <= count; i++ {
		position := 1 + (limit-1)*i/count
		index := position - 1
		if index <= 0 || index >= limit {
			continue
		}
		if out[index].CacheControl != nil {
			continue
		}
		out[index].CacheControl = &Breakpoint{Position: index, Type: "ephemeral"}
	}
	return out
}
