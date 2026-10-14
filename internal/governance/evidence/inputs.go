package evidence

import "github.com/iloveZzz/yss-cli/internal/governance/lifecycle"

import "github.com/iloveZzz/yss-cli/internal/governance/kernel"

type Inputs struct {
	ParseProgressionTarget          func(raw []byte) (lifecycle.ProgressionTarget, error)                                                                                                              `json:"-" yaml:"-"`
	ProgressionScope                func(s *kernel.SemanticSession, target string) error                                                                                                               `json:"-" yaml:"-"`
	GuidanceArtifact                func(cp map[string]any, id string, fallback ...string) string                                                                                                      `json:"-" yaml:"-"`
	GuidanceStrategicDeliveryRef    func(s *kernel.SemanticSession, cp map[string]any) (string, bool, error)                                                                                           `json:"-" yaml:"-"`
	BackendBound                    func(s *kernel.SemanticSession, v any) ([]byte, error)                                                                                                             `json:"-" yaml:"-"`
	BackendInspectDelivery          func(s *kernel.SemanticSession, ref string, opts map[string]string) (map[string]any, error)                                                                        `json:"-" yaml:"-"`
	ProgressionConsumers            func(s *kernel.SemanticSession, profile, cpRef string, cp map[string]any, target lifecycle.ProgressionTarget) (map[string]any, map[string]*kernel.SemanticSession) `json:"-" yaml:"-"`
	ProgressionBackendAuthorization func(s *kernel.SemanticSession, cpRef, terminalRef string, readOnly bool) (lifecycle.BackendAuthorization, error)                                                  `json:"-" yaml:"-"`
}

var dependencies Inputs

func Bind(value Inputs) { dependencies = value }
