package governance

import "github.com/iloveZzz/yss-cli/internal/governance/contracts"

import "github.com/iloveZzz/yss-cli/internal/governance/evidence"

func init() {
	evidence.Bind(evidence.Inputs{
		ParseProgressionTarget:          parseProgressionTarget,
		ProgressionScope:                progressionScope,
		GuidanceArtifact:                guidanceArtifact,
		GuidanceStrategicDeliveryRef:    guidanceStrategicDeliveryRef,
		BackendBound:                    contracts.BackendBound,
		BackendInspectDelivery:          contracts.BackendInspectDelivery,
		ProgressionConsumers:            progressionConsumers,
		ProgressionBackendAuthorization: progressionBackendAuthorization,
	})
}
