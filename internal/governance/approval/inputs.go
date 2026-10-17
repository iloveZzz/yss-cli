package approval

import "github.com/iloveZzz/yss-cli/internal/governance/kernel"

type Inputs struct {
	ValidateImplementationManifestSemantic func(*kernel.SemanticSession, string, []string) error                 `json:"-" yaml:"-"`
	TrustedTaskImplementation              func(*kernel.SemanticSession, map[string]any) (map[string]any, error) `json:"-" yaml:"-"`
	AssertImplementationDecisionSemantic   func(s *kernel.SemanticSession, state map[string]any) error           `json:"-" yaml:"-"`
}

var dependencies Inputs

func Bind(value Inputs) { dependencies = value }
