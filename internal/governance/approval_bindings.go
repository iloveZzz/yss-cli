package governance

import "github.com/iloveZzz/yss-cli/internal/governance/contracts"

import "github.com/iloveZzz/yss-cli/internal/governance/approval"

func init() {
	approval.Bind(approval.Inputs{
		AssertImplementationDecisionSemantic: contracts.AssertImplementationDecisionSemantic,
	})
}
