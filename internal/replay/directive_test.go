package replay

import (
	"errors"
	"reflect"
	"testing"
)

func TestParseDirectiveRecognizesCanonicalAndCompatibilityMarkers(t *testing.T) {
	tests := []struct {
		name      string
		prompt    string
		wantKind  DirectiveKind
		wantTechs []string
	}{
		{
			name:      "canonical",
			prompt:    "TMIGNORE=dedup\nkeep this prompt",
			wantKind:  IgnoreTechniques,
			wantTechs: []string{"dedup"},
		},
		{
			name:      "compatibility alias",
			prompt:    "TMINGORE=dedup",
			wantKind:  IgnoreTechniques,
			wantTechs: []string{"dedup"},
		},
		{
			name:     "ignore all",
			prompt:   "TMIGNORE=all",
			wantKind: IgnoreAll,
		},
		{
			name:      "ordered techniques",
			prompt:    "TMIGNORE=dedup,ansi",
			wantKind:  IgnoreTechniques,
			wantTechs: []string{"dedup", "ansi"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := ParseDirective(test.prompt)
			if got.Kind != test.wantKind {
				t.Fatalf("kind = %q, want %q", got.Kind, test.wantKind)
			}
			if !reflect.DeepEqual(got.Techniques, test.wantTechs) {
				t.Fatalf("techniques = %#v, want %#v", got.Techniques, test.wantTechs)
			}
			if err := got.Err(); err != nil {
				t.Fatalf("directive error = %v", err)
			}
		})
	}
}

func TestParseDirectiveOnlyUsesTheStandaloneFirstNonEmptyLine(t *testing.T) {
	tests := []struct {
		name   string
		prompt string
	}{
		{name: "embedded", prompt: "say TMIGNORE=dedup here"},
		{name: "later line", prompt: "a user prompt\nTMIGNORE=dedup"},
		{name: "fenced", prompt: "```text\nTMIGNORE=dedup\n```"},
		{name: "case mismatched canonical marker", prompt: "tmignore=dedup"},
		{name: "case mismatched alias marker", prompt: "tMINGORE=dedup"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := ParseDirective(test.prompt)
			if got.Kind != None {
				t.Fatalf("kind = %q, want %q", got.Kind, None)
			}
			if err := got.Err(); err != nil {
				t.Fatalf("unexpected directive error = %v", err)
			}
		})
	}
}

func TestParseDirectiveDoesNotChangePrompt(t *testing.T) {
	prompt := "\nTMIGNORE=dedup,ansi\nuser text"
	original := prompt
	_ = ParseDirective(prompt)
	if prompt != original {
		t.Fatalf("prompt changed from %q to %q", original, prompt)
	}
}

func TestParseDirectiveReportsTypedErrors(t *testing.T) {
	tests := []string{
		"TMIGNORE=dedup,",
		"TMIGNORE=,ansi",
		"TMIGNORE=dedup,,ansi",
		"TMIGNORE=",
		"TMIGNORE=all,dedup",
		"TMIGNORE=dedup,not valid",
		"TMIGNORE=not-a-real-technique",
	}

	for _, prompt := range tests {
		t.Run(prompt, func(t *testing.T) {
			got := ParseDirective(prompt)
			if got.Kind != None {
				t.Fatalf("kind = %q, want %q", got.Kind, None)
			}
			var directiveErr *DirectiveError
			if !errors.As(got.Err(), &directiveErr) {
				t.Fatalf("error = %v, want *DirectiveError", got.Err())
			}
		})
	}
}

func TestDirectiveValidationUsesExplicitTechniqueRegistry(t *testing.T) {
	directive := Directive{
		Kind:       IgnoreTechniques,
		Techniques: []string{"dedup", "custom-technique"},
	}
	if directive.Kind != IgnoreTechniques {
		t.Fatalf("kind = %q, want %q", directive.Kind, IgnoreTechniques)
	}
	if err := directive.Validate(); err != nil {
		t.Fatalf("syntax validation: %v", err)
	}

	var directiveErr *DirectiveError
	if err := directive.ValidateAgainst(TechniqueRegistryFunc(func(name string) bool {
		return name == "dedup"
	})); !errors.As(err, &directiveErr) {
		t.Fatalf("registry validation error = %v, want *DirectiveError", err)
	}
	if err := directive.ValidateAgainst(TechniqueRegistryFunc(func(name string) bool {
		return name == "dedup" || name == "custom-technique"
	})); err != nil {
		t.Fatalf("registry validation: %v", err)
	}
	if err := directive.ValidateAgainst(nil); !errors.As(err, &directiveErr) {
		t.Fatalf("nil registry validation error = %v, want *DirectiveError", err)
	}
}

func TestDirectiveValidateRejectsInvalidKindsAndEmptyTechniques(t *testing.T) {
	tests := []Directive{
		{Kind: IgnoreTechniques},
		{Kind: DirectiveKind("unsupported")},
	}
	for _, directive := range tests {
		var directiveErr *DirectiveError
		if err := directive.Validate(); !errors.As(err, &directiveErr) {
			t.Fatalf("Validate(%q) error = %v, want *DirectiveError", directive.Kind, err)
		}
	}
}
