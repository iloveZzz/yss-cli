package governance

import "github.com/iloveZzz/yss-cli/internal/governance/contracts"

import "github.com/iloveZzz/yss-cli/internal/governance/handoff"

func init() {
	handoff.Bind(handoff.Inputs{
		ContractDigest:                 contracts.ContractDigest,
		ContractSame:                   contracts.ContractSame,
		ContractWithout:                contracts.ContractWithout,
		ContractBusinessTicketsMode:    contracts.ContractBusinessTicketsMode,
		ContractSorted:                 contracts.ContractSorted,
		SourcePlanApprovalCheckpoint:   sourcePlanApprovalCheckpoint,
		BackendBound:                   contracts.BackendBound,
		ContractJSON:                   contracts.ContractJSON,
		ContractCopy:                   contracts.ContractCopy,
		ContractRequired:               contracts.ContractRequired,
		ContractUnique:                 contracts.ContractUnique,
		ContractWithin:                 contracts.ContractWithin,
		ContractDate:                   contracts.ContractDate,
		ContractUnion:                  contracts.ContractUnion,
		ContractBoundDoc:               contracts.ContractBoundDoc,
		ContractEvidence:               contracts.ContractEvidence,
		ContractApproval:               contracts.ContractApproval,
		MapKeysAsAny:                   contracts.MapKeysAsAny,
		ContractCollectBusinessTickets: contracts.ContractCollectBusinessTickets,
		ContractDesignTargets:          contracts.ContractDesignTargets,
		ContractTraceRow:               contracts.ContractTraceRow,
		ContractTree:                   contracts.ContractTree,
		ContractExistingUI:             contracts.ContractExistingUI,
		ContractVisualBaseline:         contracts.ContractVisualBaseline,
	})
}
