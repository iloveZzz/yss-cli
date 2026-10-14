package governance

import "github.com/iloveZzz/yss-cli/internal/governance/contracts"
import "github.com/iloveZzz/yss-cli/internal/governance/evidence"

func init() {
	contracts.Bind(contracts.Inputs{
		ContractExecutionResult:         evidence.ContractExecutionResult,
		ContractVerificationRef:         evidence.ContractVerificationRef,
		ParseProgressionTarget:          parseProgressionTarget,
		ProgressionScope:                progressionScope,
		ProgressionTaskAssetCheckpoint:  progressionTaskAssetCheckpoint,
		HasLocalImplementationInputs:    hasLocalImplementationInputs,
		ProgressionTaskEntry:            progressionTaskEntry,
		ProgressionBackendAuthorization: progressionBackendAuthorization,
	})
}
