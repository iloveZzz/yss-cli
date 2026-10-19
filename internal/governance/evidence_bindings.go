package governance

import "github.com/iloveZzz/yss-cli/internal/governance/contracts"

import "github.com/iloveZzz/yss-cli/internal/governance/evidence"

func init() {
	evidence.Bind(evidence.Inputs{
		BackendBound:                    contracts.BackendBound,
		BackendInspectDelivery:          contracts.BackendInspectDelivery,
		ProgressionBackendAuthorization: progressionBackendAuthorization,
	})
}
