package technique

import (
	"reflect"
	"sort"
	"testing"
)

func TestDefinitionsUseRealCodecIDsAndExplicitAliases(t *testing.T) {
	wantCodecIDs := map[string]string{
		"ansi":                "ansi-strip",
		"block":               "block-factor",
		"block-pack":          "block-pack",
		"cr":                  "cr-render",
		"csv-canonical":       "csv-canonical",
		"dedup":               "dedup-sha256",
		"diff-log-fold":       "diff-log-fold",
		"jcs":                 "jcs",
		"json-compact":        "json-compact",
		"json-number":         "json-number",
		"jton":                "jton-zen",
		"markdown-whitespace": "markdown-whitespace",
		"opaque-dict":         "opaque-dict",
		"path-dict":           "path-dict",
		"rle":                 "exact-rle",
		"stacktrace-dict":     "stacktrace-dict",
		"symbol-table":        "symbol-table",
		"table-tsv":           "table-tsv",
		"text-norm":           "text-norm",
		"html-entity":         "html-entity",
		"base64-compact":      "base64-compact",
		"url-decode":          "url-decode",
		"hex-compact":         "hex-compact",
		"prefix-fold":         "prefix-fold",
		"unicode-unescape":    "unicode-unescape",
		"uuid-compact":        "uuid-compact",
		"smart-punct":         "smart-punct",
		"mojibake-fix":        "mojibake-fix",
		"idn-decode":          "idn-decode",
		"ipv6-norm":           "ipv6-norm",
		"csv-unquote":         "csv-unquote",
		"sql-minify":          "sql-minify",
		"iso-norm":            "iso-norm",
		"epoch-to-iso":        "epoch-to-iso",
		"md-link-ref":         "md-link-ref",
		"header-norm":         "header-norm",
		"nfkc-fold":           "nfkc-fold",
		"trailing-ws":         "trailing-ws",
		"blank-run":           "blank-run",
		"color-compact":       "color-compact",
		"xml-minify":          "xml-minify",
		"range-fold":          "range-fold",
		"xml-cdata":           "xml-cdata",
	}
	wantAliases := map[string][]string{
		"stacktrace-dict": {"stacktrace"},
	}

	definitions := Definitions()
	if len(definitions) != len(wantCodecIDs) {
		t.Fatalf("Definitions returned %d entries, want %d", len(definitions), len(wantCodecIDs))
	}
	ids := make([]string, 0, len(definitions))
	seenNames := make(map[string]struct{})
	for _, definition := range definitions {
		ids = append(ids, definition.ID)
		wantCodecID, ok := wantCodecIDs[definition.ID]
		if !ok {
			t.Fatalf("unexpected canonical technique %q", definition.ID)
		}
		if definition.CodecID != wantCodecID {
			t.Fatalf("technique %q codec ID = %q, want %q", definition.ID, definition.CodecID, wantCodecID)
		}
		if !reflect.DeepEqual(definition.Aliases, wantAliases[definition.ID]) {
			t.Fatalf("technique %q aliases = %v, want %v", definition.ID, definition.Aliases, wantAliases[definition.ID])
		}
		for _, name := range definitionNames(definition) {
			if _, duplicate := seenNames[name]; duplicate {
				t.Fatalf("technique name %q is registered more than once", name)
			}
			seenNames[name] = struct{}{}
			resolved, ok := Lookup(name)
			if !ok || resolved.ID != definition.ID || resolved.CodecID != definition.CodecID {
				t.Fatalf("Lookup(%q) = %+v, %v; want %q/%q", name, resolved, ok, definition.ID, definition.CodecID)
			}
		}
	}
	if !sort.StringsAreSorted(ids) {
		t.Fatalf("canonical IDs are not sorted: %v", ids)
	}
	if Has("not-a-real-technique") {
		t.Fatal("unknown technique must not resolve")
	}
}

func TestRegistryAccessorsReturnIndependentDeterministicCopies(t *testing.T) {
	firstDefinitions := Definitions()
	secondDefinitions := Definitions()
	if !reflect.DeepEqual(firstDefinitions, secondDefinitions) {
		t.Fatalf("Definitions is not deterministic: %v != %v", firstDefinitions, secondDefinitions)
	}
	aliasIndex := -1
	for i, definition := range firstDefinitions {
		if len(definition.Aliases) > 0 {
			aliasIndex = i
			break
		}
	}
	if aliasIndex < 0 {
		t.Fatal("test requires an explicit alias in the registry")
	}
	firstDefinitions[aliasIndex].ID = "mutated"
	firstDefinitions[aliasIndex].Aliases[0] = "mutated"
	if Definitions()[aliasIndex].ID == "mutated" || Definitions()[aliasIndex].Aliases[0] == "mutated" {
		t.Fatal("Definitions must return deep copies")
	}

	firstIDs := IDs()
	secondIDs := IDs()
	if !reflect.DeepEqual(firstIDs, secondIDs) {
		t.Fatalf("IDs is not deterministic: %v != %v", firstIDs, secondIDs)
	}
	firstIDs[0] = "mutated"
	if IDs()[0] == "mutated" {
		t.Fatal("IDs must return a copy")
	}
}
