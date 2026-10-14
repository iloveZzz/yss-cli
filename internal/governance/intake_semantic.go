package governance

import (
	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
)

func init() { kernel.RegisterSemanticValidator("intake-observation", verifyIntakeObservationSemantic) }

func verifyIntakeObservationSemantic(s *kernel.SemanticSession, ref string, opts map[string]string) error {
	observation, err := s.Doc(opts["observation"])
	if err != nil {
		return err
	}
	if kernel.Text(observation["root"]) != s.Root || observation["kind"] != "read-only-intake-observation" || !kernel.ApEqual(observation["before"], observation["after"]) {
		return s.Reject("INTAKE_OBSERVATION", "观测身份或前后状态不一致")
	}
	actual, err := s.IntakeSnapshot(s.Root, 0)
	if err != nil {
		return err
	}
	if !kernel.ApEqual(actual, observation["after"]) {
		return s.Reject("INTAKE_OBSERVATION", "仓库实际差异与只读声明冲突")
	}
	s.IntakeInputs[s.Root] = actual
	return nil
}
