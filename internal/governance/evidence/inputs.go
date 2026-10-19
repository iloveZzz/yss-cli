package evidence

import "github.com/iloveZzz/yss-cli/internal/governance/lifecycle"

import "github.com/iloveZzz/yss-cli/internal/governance/kernel"

type Inputs struct {
	BackendBound                    func(s *kernel.SemanticSession, v any) ([]byte, error)                                                            `json:"-" yaml:"-"`
	BackendInspectDelivery          func(s *kernel.SemanticSession, ref string, opts map[string]string) (map[string]any, error)                       `json:"-" yaml:"-"`
	ProgressionBackendAuthorization func(s *kernel.SemanticSession, cpRef, terminalRef string, readOnly bool) (lifecycle.BackendAuthorization, error) `json:"-" yaml:"-"`
}

var dependencies Inputs

func Bind(value Inputs) { dependencies = value }
