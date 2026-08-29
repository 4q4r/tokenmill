package technique

// Definition maps a canonical replay-directive ID to the real codec ID and
// any explicitly supported aliases. Every alias resolves to the same codec ID.
type Definition struct {
	ID      string
	CodecID string
	Aliases []string
}

var definitions = []Definition{
	{ID: "ansi", CodecID: "ansi-strip"},
	{ID: "base64-compact", CodecID: "base64-compact"},
	{ID: "block", CodecID: "block-factor"},
	{ID: "block-pack", CodecID: "block-pack"},
	{ID: "cr", CodecID: "cr-render"},
	{ID: "csv-canonical", CodecID: "csv-canonical"},
	{ID: "dedup", CodecID: "dedup-sha256"},
	{ID: "diff-log-fold", CodecID: "diff-log-fold"},
	{ID: "html-entity", CodecID: "html-entity"},
	{ID: "jcs", CodecID: "jcs"},
	{ID: "json-compact", CodecID: "json-compact"},
	{ID: "json-number", CodecID: "json-number"},
	{ID: "jton", CodecID: "jton-zen"},
	{ID: "markdown-whitespace", CodecID: "markdown-whitespace"},
	{ID: "opaque-dict", CodecID: "opaque-dict"},
	{ID: "path-dict", CodecID: "path-dict"},
	{ID: "rle", CodecID: "exact-rle"},
	{ID: "stacktrace-dict", CodecID: "stacktrace-dict", Aliases: []string{"stacktrace"}},
	{ID: "symbol-table", CodecID: "symbol-table"},
	{ID: "table-tsv", CodecID: "table-tsv"},
	{ID: "text-norm", CodecID: "text-norm"},
}

var names = buildNames()

var nameIndex = buildIndex()

func buildNames() []string {
	result := make([]string, len(definitions))
	for i, definition := range definitions {
		result[i] = definition.ID
	}
	return result
}

func buildIndex() map[string]Definition {
	index := make(map[string]Definition)
	for _, definition := range definitions {
		for _, name := range definitionNames(definition) {
			if _, exists := index[name]; exists {
				panic("duplicate technique name: " + name)
			}
			index[name] = definition
		}
	}
	return index
}

func definitionNames(definition Definition) []string {
	result := make([]string, 0, 2+len(definition.Aliases))
	for _, name := range append([]string{definition.ID, definition.CodecID}, definition.Aliases...) {
		alreadyPresent := false
		for _, existing := range result {
			if existing == name {
				alreadyPresent = true
				break
			}
		}
		if !alreadyPresent {
			result = append(result, name)
		}
	}
	return result
}

// Definitions returns deep copies of the canonical technique definitions.
func Definitions() []Definition {
	result := make([]Definition, len(definitions))
	for i, definition := range definitions {
		result[i] = cloneDefinition(definition)
	}
	return result
}

// IDs returns the sorted canonical directive IDs.
func IDs() []string {
	return append([]string(nil), names...)
}

// Has reports whether name is a canonical ID, real codec ID, or explicit alias.
func Has(name string) bool {
	_, ok := nameIndex[name]
	return ok
}

// Lookup resolves a canonical ID, real codec ID, or explicit alias.
func Lookup(name string) (Definition, bool) {
	definition, ok := nameIndex[name]
	if !ok {
		return Definition{}, false
	}
	return cloneDefinition(definition), true
}

func cloneDefinition(definition Definition) Definition {
	definition.Aliases = append([]string(nil), definition.Aliases...)
	return definition
}
