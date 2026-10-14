package approval

import "github.com/iloveZzz/yss-cli/internal/governance/kernel"

type Inputs struct {
	AssertImplementationDecisionSemantic func(s *kernel.SemanticSession, state map[string]any) error `json:"-" yaml:"-"`
}

var dependencies Inputs

func Bind(value Inputs) { dependencies = value }
