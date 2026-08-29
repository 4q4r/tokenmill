package codec

import "github.com/tokenmill/tokenmill/internal/technique"

// TechniqueIDs delegates the canonical directive vocabulary to the
// standard-library-only technique package.
func TechniqueIDs() []string {
	return technique.IDs()
}

// HasTechnique delegates canonical IDs, real codec IDs, and explicit aliases
// to the shared technique registry.
func HasTechnique(id string) bool {
	return technique.Has(id)
}
