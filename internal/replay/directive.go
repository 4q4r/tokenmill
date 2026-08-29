package replay

import (
	"fmt"
	"strings"

	techniquevocab "github.com/tokenmill/tokenmill/internal/technique"
)

// DirectiveKind is the action requested by a prompt directive.
type DirectiveKind string

const (
	None             DirectiveKind = "none"
	IgnoreTechniques DirectiveKind = "ignore_techniques"
	IgnoreAll        DirectiveKind = "ignore_all"

	// Descriptive aliases keep call sites readable without changing the
	// canonical directive values.
	DirectiveNone             = None
	DirectiveIgnoreTechniques = IgnoreTechniques
	DirectiveIgnoreAll        = IgnoreAll
)

// Directive is the result of parsing the first non-empty prompt line.
// Error is populated for a recognized marker with an invalid value. The parser
// checks technique names against the canonical technique registry and returns a
// typed error for unknown names.
type Directive struct {
	Kind       DirectiveKind
	Techniques []string
	Error      error
}

// TechniqueRegistry resolves technique names for callers that need to validate
// a directive against a different, explicitly supplied registry.
type TechniqueRegistry interface {
	HasTechnique(string) bool
}

// TechniqueRegistryFunc adapts a function to TechniqueRegistry.
type TechniqueRegistryFunc func(string) bool

// HasTechnique implements TechniqueRegistry.
func (f TechniqueRegistryFunc) HasTechnique(name string) bool {
	return f != nil && f(name)
}

// Err returns the typed parsing error, if any.
func (d Directive) Err() error {
	return d.Error
}

// Validate checks the directive's kind and techniques.
func (d Directive) Validate() error {
	if d.Error != nil {
		return d.Error
	}
	switch d.Kind {
	case None:
		if len(d.Techniques) != 0 {
			return directiveError("", "none directive cannot contain techniques")
		}
	case IgnoreAll:
		if len(d.Techniques) != 0 {
			return directiveError("all", "ignore-all directive cannot contain techniques")
		}
	case IgnoreTechniques:
		if len(d.Techniques) == 0 {
			return directiveError("", "at least one technique is required")
		}
		for _, technique := range d.Techniques {
			if !validTechniqueName(technique) {
				return directiveError(technique, "invalid technique name")
			}
		}
	default:
		return directiveError(string(d.Kind), "unknown directive kind")
	}
	return nil
}

// ValidateAgainst checks technique names against an explicitly supplied
// registry. ParseDirective already validates against the canonical technique
// registry; this method is useful for a narrower integration registry.
func (d Directive) ValidateAgainst(registry TechniqueRegistry) error {
	if err := d.Validate(); err != nil {
		return err
	}
	if d.Kind != IgnoreTechniques {
		return nil
	}
	if registry == nil {
		return directiveError("", "technique registry is required")
	}
	for _, technique := range d.Techniques {
		if !registry.HasTechnique(technique) {
			return directiveError(technique, "unknown technique")
		}
	}
	return nil
}

// Clone returns a copy whose technique slice is independent of the original.
func (d Directive) Clone() Directive {
	if d.Techniques != nil {
		d.Techniques = append([]string(nil), d.Techniques...)
	}
	return d
}

// ParseDirective recognizes an exact TMIGNORE or TMINGORE marker only when it
// is the standalone first non-empty line of prompt. The prompt itself is
// never rewritten; the marker remains part of the caller's text.
func ParseDirective(prompt string) Directive {
	for _, line := range strings.Split(prompt, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if isFenceLine(line) {
			return Directive{Kind: None}
		}

		value, recognized := directiveValue(line)
		if !recognized {
			return Directive{Kind: None}
		}
		return parseDirectiveValue(value)
	}
	return Directive{Kind: None}
}

func directiveValue(line string) (string, bool) {
	for _, marker := range []string{"TMIGNORE=", "TMINGORE="} {
		if strings.HasPrefix(line, marker) {
			return strings.TrimSpace(strings.TrimPrefix(line, marker)), true
		}
	}
	return "", false
}

func parseDirectiveValue(value string) Directive {
	if value == "all" {
		return Directive{Kind: IgnoreAll}
	}
	if value == "" {
		return invalidDirective(value, "directive value must not be empty")
	}

	rawTechniques := strings.Split(value, ",")
	techniques := make([]string, len(rawTechniques))
	for i, rawTechnique := range rawTechniques {
		technique := strings.TrimSpace(rawTechnique)
		if technique == "" {
			return invalidDirective(value, "technique name must not be empty")
		}
		techniques[i] = technique
	}
	directive := Directive{Kind: IgnoreTechniques, Techniques: techniques}
	if err := directive.Validate(); err != nil {
		return Directive{Kind: None, Error: err}
	}
	for _, technique := range techniques {
		if !techniquevocab.Has(technique) {
			return invalidDirective(technique, "unknown technique")
		}
	}
	return directive
}

func invalidDirective(value, reason string) Directive {
	return Directive{Kind: None, Error: directiveError(value, reason)}
}

func directiveError(value, reason string) *DirectiveError {
	return &DirectiveError{Value: value, Reason: reason}
}

// DirectiveError reports an invalid value on an explicit prompt directive.
type DirectiveError struct {
	Value  string
	Reason string
}

func (e *DirectiveError) Error() string {
	if e.Value == "" {
		return fmt.Sprintf("replay directive is invalid: %s", e.Reason)
	}
	return fmt.Sprintf("replay directive value %q is invalid: %s", e.Value, e.Reason)
}

func isFenceLine(line string) bool {
	return strings.HasPrefix(line, "```") || strings.HasPrefix(line, "~~~")
}

func validTechniqueName(technique string) bool {
	if technique == "" || technique == "all" {
		return false
	}
	for _, character := range []byte(technique) {
		if (character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			character == '-' || character == '_' || character == '.' {
			continue
		}
		return false
	}
	return true
}
