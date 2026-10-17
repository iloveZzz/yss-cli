package contracts

import (
	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
	"strings"
)

func contractBusinessReview2(s *kernel.SemanticSession, review map[string]any, ref, digest string) error {
	if kernel.ApNumber(review["schema_version"]) != 2 || review["kind"] != "business-ticket-review" || review["result"] != "passed" || review["subject_ref"] != ref || review["subject_digest"] != digest || len(kernel.ApRows(review["evidence"])) == 0 {
		return s.Reject("REVIEW_REQUIRED", "新政策需要当前业务集合的真实自检或适用复核记录2")
	}
	candidate := map[string]any{"ref": ref, "digest": strings.TrimPrefix(digest, "sha256:")}
	source, err := TrustedTaskImplementation(s, map[string]any{"candidate": candidate, "basis": review["basis"]})
	if err != nil {
		return err
	}
	task, err := s.Doc(kernel.Text(source["task_ref"]))
	if err != nil {
		return err
	}
	scope, implementation := kernel.ApStrings(source["scope"]), kernel.ApMap(source["provenance"])
	mode, err := s.RequiredReviewMode(candidate, implementation, scope, task["input_bindings"], review["basis"])
	if err != nil {
		return err
	}
	if err = s.ValidateReviewEvidenceDigest(kernel.ApMap(review["assessment"]), candidate, scope, mode, implementation, kernel.Text(source["task_ref"]), kernel.ApMap(source["candidate"])); err != nil {
		return err
	}
	rules := kernel.ApMap(review["external_rules"])
	if rules == nil {
		rules = map[string]any{"state": "none-known"}
	}
	return s.ValidateExternalRulesDigest(rules, candidate, scope, implementation)
}
