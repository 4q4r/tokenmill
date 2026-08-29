package codec

// FeatureMetadata describes a codec and the safety policy used by integration
// layers. The registry is metadata-only: it does not construct or enable
// codecs by itself.
type FeatureMetadata struct {
	ID             string
	Description    string
	Losslessness   string
	ModelSafe      bool
	DefaultEnabled bool
}

var featureMetadata = []FeatureMetadata{
	{
		ID:             "block-pack",
		Description:    "Exact repeated-block packing with a self-contained dictionary",
		Losslessness:   "byte-lossless",
		ModelSafe:      false,
		DefaultEnabled: false,
	},
	{
		ID:             "csv-canonical",
		Description:    "CSV/TSV canonicalization with delimiter, quote, and line-ending metadata",
		Losslessness:   "byte-lossless",
		ModelSafe:      false,
		DefaultEnabled: false,
	},
	{
		ID:             "diff-log-fold",
		Description:    "Exact repeated-line and repeated-block folding with embedded bytes",
		Losslessness:   "byte-lossless",
		ModelSafe:      false,
		DefaultEnabled: false,
	},
	{
		ID:             "jcs",
		Description:    "Canonical JSON object ordering and compact separators with preserved number lexemes",
		Losslessness:   "data-lossless",
		ModelSafe:      true,
		DefaultEnabled: true,
	},
	{
		ID:             "json-number",
		Description:    "Exact decimal JSON-number canonicalization without changing strings or layout",
		Losslessness:   "data-lossless",
		ModelSafe:      true,
		DefaultEnabled: true,
	},
	{
		ID:             "markdown-whitespace",
		Description:    "Protected Markdown paragraph whitespace normalization with an exact sidecar",
		Losslessness:   "byte-lossless",
		ModelSafe:      false,
		DefaultEnabled: false,
	},
	{
		ID:             "opaque-dict",
		Description:    "Exact self-contained dictionary for repeated opaque values and URLs",
		Losslessness:   "byte-lossless",
		ModelSafe:      false,
		DefaultEnabled: false,
	},
	{
		ID:             "symbol-table",
		Description:    "Exact repeated-token abbreviation with a self-contained dictionary",
		Losslessness:   "byte-lossless",
		ModelSafe:      false,
		DefaultEnabled: false,
	},
}

// Features returns a deterministic copy of the registered codec metadata.
func Features() []FeatureMetadata {
	features := make([]FeatureMetadata, len(featureMetadata))
	copy(features, featureMetadata)
	return features
}

// FeatureRegistry is a descriptive alias for callers that prefer registry
// terminology.
func FeatureRegistry() []FeatureMetadata { return Features() }

// LookupFeature returns metadata for an exact feature ID.
func LookupFeature(id string) (FeatureMetadata, bool) {
	for _, feature := range featureMetadata {
		if feature.ID == id {
			return feature, true
		}
	}
	return FeatureMetadata{}, false
}
