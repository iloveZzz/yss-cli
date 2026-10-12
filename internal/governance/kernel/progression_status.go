package kernel

import (
	"path"
	"strings"
)

func isProgressionEvidence(ref string) bool {
	return strings.EqualFold(path.Base(ref), ProgressionFile)
}

func RejectProgressionEvidence(s *SemanticSession, ref string) error {
	if isProgressionEvidence(ref) {
		return s.Reject("PROGRESSION_EVIDENCE", "推进目标意图不得作为批准或交接证据："+ref)
	}
	return nil
}
