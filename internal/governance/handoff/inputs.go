package handoff

import "github.com/iloveZzz/yss-cli/internal/governance/kernel"

type Inputs struct {
	ContractDigest                 func(v any) string                                                                                                     `json:"-" yaml:"-"`
	ContractSame                   func(a, b any) bool                                                                                                    `json:"-" yaml:"-"`
	ContractWithout                func(v map[string]any, keys ...string) map[string]any                                                                  `json:"-" yaml:"-"`
	ContractBusinessTicketsMode    func(s *kernel.SemanticSession, ref, mode string) (map[string]any, error)                                              `json:"-" yaml:"-"`
	ContractSorted                 func(a []string) []string                                                                                              `json:"-" yaml:"-"`
	SourcePlanApprovalCheckpoint   func(s *kernel.SemanticSession, ref string, opts map[string]string) (string, error)                                    `json:"-" yaml:"-"`
	BackendBound                   func(s *kernel.SemanticSession, v any) ([]byte, error)                                                                 `json:"-" yaml:"-"`
	ContractJSON                   func(v any) (string, error)                                                                                            `json:"-" yaml:"-"`
	ContractCopy                   func(v map[string]any) map[string]any                                                                                  `json:"-" yaml:"-"`
	ContractRequired               func(s *kernel.SemanticSession, m map[string]any, keys ...string) error                                                `json:"-" yaml:"-"`
	ContractUnique                 func(v any, nonempty bool) bool                                                                                        `json:"-" yaml:"-"`
	ContractWithin                 func(candidate, parent string) bool                                                                                    `json:"-" yaml:"-"`
	ContractDate                   func(v any) bool                                                                                                       `json:"-" yaml:"-"`
	ContractUnion                  func(groups ...[]string) []string                                                                                      `json:"-" yaml:"-"`
	ContractBoundDoc               func(s *kernel.SemanticSession, v any) (map[string]any, error)                                                         `json:"-" yaml:"-"`
	ContractEvidence               func(s *kernel.SemanticSession, refs any) error                                                                        `json:"-" yaml:"-"`
	ContractApproval               func(s *kernel.SemanticSession, gate string, binding map[string]any, approvalRef string, opts map[string]string) error `json:"-" yaml:"-"`
	MapKeysAsAny                   func(v map[string]map[string]any) []string                                                                             `json:"-" yaml:"-"`
	ContractCollectBusinessTickets func(s *kernel.SemanticSession, ref, mode, reviewRef string) (out, report map[string]any, failure error)               `json:"-" yaml:"-"`
	ContractDesignTargets          func(s *kernel.SemanticSession, data map[string]any) (map[string]map[string]any, map[string]map[string]any, error)     `json:"-" yaml:"-"`
	ContractTraceRow               func(s *kernel.SemanticSession, row, source map[string]any, ids, seams map[string]map[string]any, slice string) error  `json:"-" yaml:"-"`
	ContractTree                   func(s *kernel.SemanticSession, prefix string) (string, []string, error)                                               `json:"-" yaml:"-"`
	ContractExistingUI             func(s *kernel.SemanticSession, ref string) (map[string]any, error)                                                    `json:"-" yaml:"-"`
	ContractVisualBaseline         func(s *kernel.SemanticSession, ref string) (map[string]any, error)                                                    `json:"-" yaml:"-"`
}

var dependencies Inputs

func Bind(value Inputs) { dependencies = value }

type domainFailure struct {
	cause  error
	report *kernel.SemanticReport
}

func (e *domainFailure) Error() string    { return e.cause.Error() }
func (e *domainFailure) Unwrap() error    { return e.cause }
func (e *domainFailure) ErrorResult() any { return e.report }
