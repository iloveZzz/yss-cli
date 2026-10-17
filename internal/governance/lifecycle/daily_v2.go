package lifecycle

import (
	"sort"
	"strings"
	"time"

	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
)

func (d *dailySession) verifyRoleIndependentReview(value any, bindingField, digest string) error {
	review, ok := kernel.Object(value)
	if !ok || review["result"] != "passed" || !kernel.ApIsArray(review["blocking_findings"]) || len(kernel.ApArray(review["blocking_findings"])) != 0 || review[bindingField] != digest {
		return d.rejected("复核未通过、存在阻塞项或未绑定当前候选")
	}
	if err := d.evidenceRef(kernel.Text(review["ref"]), kernel.Text(review["digest"])); err != nil {
		return err
	}
	actual, err := d.factRecord(kernel.Text(review["ref"]))
	if err != nil {
		return err
	}
	for _, key := range []string{"result", "blocking_findings", "assessment", bindingField} {
		if !kernel.ApEqual(actual[key], review[key]) {
			return d.rejected("复核原始事实与登记不符: " + key)
		}
	}
	implementation := kernel.ApMap(kernel.ApMap(d.record["implementation"])["provenance"])
	scope := kernel.ApStrings(kernel.ApMap(d.record["scope"])["paths"])
	candidate := map[string]any{"ref": d.taskRef, "digest": strings.TrimPrefix(digest, "sha256:")}
	implementationCandidate := map[string]any{"ref": d.taskRef, "digest": strings.TrimPrefix(d.report.CandidateDigest, "sha256:")}
	mode, err := d.s.RequiredReviewModeAtRoot(candidate, implementation, scope, d.repo, d.reviewBindings())
	if err != nil {
		return err
	}
	if rules, exists := d.record["external_rules"]; exists {
		if err = d.s.ValidateExternalRulesDigest(kernel.ApMap(rules), candidate, scope, implementation); err != nil {
			return err
		}
	}
	assessment := kernel.ApMap(review["assessment"])
	if err := d.s.ValidateReviewEvidenceDigest(assessment, candidate, scope, mode, implementation, d.taskRef, implementationCandidate); err != nil {
		return err
	}
	original, err := d.s.EvidenceFact(kernel.Text(kernel.ApMap(assessment["provenance"])["execution_ref"]))
	if err != nil {
		return err
	}
	if original["status"] != nil && original["status"] != "passed" {
		return d.rejected("原始复核记录不是passed")
	}
	return nil
}

func (d *dailySession) reviewBindings() []any {
	refs := []string{}
	for ref := range d.basis {
		if ref != "delivery-policy" && !strings.HasPrefix(ref, "api-baseline:") && !strings.HasPrefix(ref, "api-current:") {
			refs = append(refs, ref)
		}
	}
	sort.Strings(refs)
	bindings := []any{}
	for _, ref := range refs {
		bindings = append(bindings, map[string]any{"ref": ref, "digest": strings.TrimPrefix(d.basis[ref], "sha256:")})
	}
	return bindings
}

func (d *dailySession) recordVersion() error {
	if d.s.GovernanceVersion == 2 {
		if kernel.ApNumber(d.record["schema_version"]) != 2 || kernel.ApNumber(d.record["policy_version"]) != 2 {
			return d.s.Reject("DAILY_VERSION", "新日常记录必须明确schema_version2/policy_version2")
		}
		d.report.SchemaVersion = 2
	} else {
		for _, key := range []string{"schema_version", "policy_version"} {
			if value, present := d.record[key]; present && kernel.ApNumber(value) != 1 {
				return d.s.Reject("DAILY_VERSION", "旧日常政策不能消费新或未知记录")
			}
		}
	}
	return nil
}

func (d *dailySession) verifyImplementation() error {
	p := kernel.ApMap(kernel.ApMap(d.record["implementation"])["provenance"])
	actual, err := d.s.ValidateProvenance(p, d.taskRef, strings.TrimPrefix(d.report.CandidateDigest, "sha256:"))
	if err != nil {
		return err
	}
	if kernel.ApNumber(actual["exit_code"]) != 0 || actual["result"] != "passed" || actual["status"] != nil && actual["status"] != "passed" || !kernel.SameStringSet(actual["scope"], kernel.ApMap(d.record["scope"])["paths"]) {
		return d.rejected("当前实施来源未实际通过或范围不一致")
	}
	return nil
}

// Sequence consumes the original events; a matching freeze hash alone is not a performed freeze.
func (d *dailySession) verifyAPISequence(api map[string]any) error {
	events := []map[string]any{}
	for _, phase := range []string{"draft", "freeze"} {
		row, ok := kernel.Object(api[phase])
		argv, valid := dailyStrings(row["argv"])
		exit, exitValid := kernel.Integer(row["exit_code"])
		if !ok || !valid || len(argv) == 0 || !exitValid || exit != 0 || row["api_digest"] != d.report.APIContractDigest {
			return d.rejected("API " + phase + "缺少当前实际执行事件")
		}
		if err := d.executionLog(row, row["argv"], d.repo, "api_digest", d.report.APIContractDigest); err != nil {
			return err
		}
		raw, err := d.factRecord(kernel.Text(row["log_ref"]))
		if err != nil {
			return err
		}
		if raw["phase"] != phase {
			return d.rejected("API事件原始phase不符: " + phase)
		}
		events = append(events, raw)
	}
	lint, err := d.factRecord(kernel.Text(kernel.ApMap(api["lint"])["log_ref"]))
	if err != nil {
		return err
	}
	review, err := d.s.EvidenceFact(kernel.Text(kernel.ApMap(kernel.ApMap(kernel.ApMap(api["review"])["assessment"])["provenance"])["execution_ref"]))
	if err != nil {
		return err
	}
	implementation, err := d.s.EvidenceFact(kernel.Text(kernel.ApMap(kernel.ApMap(d.record["implementation"])["provenance"])["execution_ref"]))
	if err != nil {
		return err
	}
	if implementation["api_digest"] != d.report.APIContractDigest {
		return d.rejected("实际实现未绑定当前API Freeze")
	}
	ordered := []map[string]any{events[0], lint, review, events[1], implementation}
	var previous time.Time
	for _, event := range ordered {
		at, err := time.Parse(time.RFC3339Nano, kernel.Text(event["executed_at"]))
		if err != nil || at.After(time.Now().Add(time.Minute)) || (!previous.IsZero() && at.Before(previous)) {
			return d.rejected("API原始事件不满足Draft、lint、内容复核、Freeze、实现顺序")
		}
		previous = at
	}
	for _, row := range kernel.ApRows(api["contract_tests"]) {
		log, err := d.factRecord(kernel.Text(row["log_ref"]))
		if err != nil {
			return err
		}
		at, err := time.Parse(time.RFC3339Nano, kernel.Text(log["executed_at"]))
		if err != nil || at.Before(previous) {
			return d.rejected("契约测试早于当前API实现")
		}
	}
	return nil
}
