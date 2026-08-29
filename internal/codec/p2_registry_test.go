package codec

import (
	"reflect"
	"sort"
	"testing"
)

func TestFeatureRegistryIsDeterministicAndConservative(t *testing.T) {
	features := Features()
	want := map[string]FeatureMetadata{
		"block-pack": {
			ID:             "block-pack",
			Description:    "Exact repeated-block packing with a self-contained dictionary",
			Losslessness:   "byte-lossless",
			ModelSafe:      false,
			DefaultEnabled: false,
		},
		"csv-canonical": {
			ID:             "csv-canonical",
			Description:    "CSV/TSV canonicalization with delimiter, quote, and line-ending metadata",
			Losslessness:   "byte-lossless",
			ModelSafe:      false,
			DefaultEnabled: false,
		},
		"diff-log-fold": {
			ID:             "diff-log-fold",
			Description:    "Exact repeated-line and repeated-block folding with embedded bytes",
			Losslessness:   "byte-lossless",
			ModelSafe:      false,
			DefaultEnabled: false,
		},
		"jcs": {
			ID:             "jcs",
			Description:    "Canonical JSON object ordering and compact separators with preserved number lexemes",
			Losslessness:   "data-lossless",
			ModelSafe:      true,
			DefaultEnabled: true,
		},
		"json-number": {
			ID:             "json-number",
			Description:    "Exact decimal JSON-number canonicalization without changing strings or layout",
			Losslessness:   "data-lossless",
			ModelSafe:      true,
			DefaultEnabled: true,
		},
		"markdown-whitespace": {
			ID:             "markdown-whitespace",
			Description:    "Protected Markdown paragraph whitespace normalization with an exact sidecar",
			Losslessness:   "byte-lossless",
			ModelSafe:      false,
			DefaultEnabled: false,
		},
		"opaque-dict": {
			ID:             "opaque-dict",
			Description:    "Exact self-contained dictionary for repeated opaque values and URLs",
			Losslessness:   "byte-lossless",
			ModelSafe:      false,
			DefaultEnabled: false,
		},
		"symbol-table": {
			ID:             "symbol-table",
			Description:    "Exact repeated-token abbreviation with a self-contained dictionary",
			Losslessness:   "byte-lossless",
			ModelSafe:      false,
			DefaultEnabled: false,
		},
	}
	if len(features) != len(want) {
		t.Fatalf("Features returned %d entries, want %d", len(features), len(want))
	}

	ids := make([]string, 0, len(features))
	for _, feature := range features {
		ids = append(ids, feature.ID)
		expected, ok := want[feature.ID]
		if !ok {
			t.Fatalf("unexpected feature %q", feature.ID)
		}
		if feature != expected {
			t.Fatalf("feature %q = %+v, want %+v", feature.ID, feature, expected)
		}
	}
	if !sort.StringsAreSorted(ids) {
		t.Fatalf("feature IDs are not sorted deterministically: %v", ids)
	}

	first := Features()
	second := Features()
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("Features is not deterministic:\nfirst=%v\nsecond=%v", first, second)
	}
	first[0].ID = "mutated"
	if Features()[0].ID == "mutated" {
		t.Fatal("Features must return a copy, not mutable registry storage")
	}

	for _, id := range ids {
		feature, ok := LookupFeature(id)
		if !ok || feature.ID != id {
			t.Fatalf("LookupFeature(%q) = (%v, %v)", id, feature, ok)
		}
	}
	if _, ok := LookupFeature("does-not-exist"); ok {
		t.Fatal("unknown feature should not be found")
	}
}
