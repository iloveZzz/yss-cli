package contracts

import "github.com/iloveZzz/yss-cli/internal/governance/lifecycle"

import "github.com/iloveZzz/yss-cli/internal/governance/kernel"

type Inputs struct {
	ContractExecutionResult         func(*kernel.SemanticSession, map[string]any, map[string]string) error                                            `json:"-" yaml:"-"`
	ContractVerificationRef         func(s *kernel.SemanticSession, ref string) (string, error)                                                       `json:"-" yaml:"-"`
	ParseProgressionTarget          func(raw []byte) (lifecycle.ProgressionTarget, error)                                                             `json:"-" yaml:"-"`
	ProgressionScope                func(s *kernel.SemanticSession, target string) error                                                              `json:"-" yaml:"-"`
	ProgressionTaskAssetCheckpoint  func(s *kernel.SemanticSession, assetRef string) (string, error)                                                  `json:"-" yaml:"-"`
	HasLocalImplementationInputs    func(s *kernel.SemanticSession) (bool, error)                                                                     `json:"-" yaml:"-"`
	ProgressionTaskEntry            func(s *kernel.SemanticSession, task map[string]any) error                                                        `json:"-" yaml:"-"`
	ProgressionBackendAuthorization func(s *kernel.SemanticSession, cpRef, terminalRef string, readOnly bool) (lifecycle.BackendAuthorization, error) `json:"-" yaml:"-"`
}

var dependencies Inputs

func Bind(value Inputs) { dependencies = value }
