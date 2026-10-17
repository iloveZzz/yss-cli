package approval

import (
	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"path/filepath"
	"strings"
)

func assertDecisionContinuation2(s *kernel.SemanticSession, requirement map[string]any) (decisionValidation, error) {
	result := decisionValidation{}
	bad := func(m string) (decisionValidation, error) {
		return result, s.Reject("user-decision-continuation-invalid", m)
	}
	policy := kernel.ApMap(s.UserDecisionPolicy()["continuation"])
	if policy["capability"] != "role-independent-governance-v1" || !kernel.ApContains(policy["boundaries"], kernel.Text(requirement["boundary"])) {
		return bad("未授权的延续边界")
	}
	proof, err := s.Doc(kernel.Text(requirement["continuation_ref"]))
	if err != nil {
		return result, err
	}
	if err = s.ValidateSchema(".template-spec/process/schemas/approved-scope-continuation.schema.json", proof); err != nil {
		return result, err
	}
	if kernel.ApNumber(proof["schema_version"]) != 2 || proof["kind"] != "approved-scope-continuation-v2" || proof["boundary"] != requirement["boundary"] || kernel.ApMap(proof["subject"])["ref"] != requirement["subject_ref"] || !kernel.ApSetEqual(proof["scope"], requirement["scope"]) {
		return bad("延续版本、边界、资产或范围不匹配")
	}
	source := kernel.ApMap(proof["source"])
	if source["boundary"] != "delivery-scope" || source["continuation_ref"] != nil || kernel.Text(source["user_decision_ref"]) == "" {
		return bad("延续必须回到原始真实交付范围决定")
	}
	for _, scope := range kernel.ApStrings(requirement["scope"]) {
		if !kernel.ApContains(source["scope"], scope) {
			return bad("延续超出原始批准范围")
		}
	}
	decision, err := s.Doc(kernel.Text(source["user_decision_ref"]))
	if err != nil {
		return result, err
	}
	var original map[string]any
	count := 0
	for _, item := range kernel.ApRows(kernel.ApMap(decision["request"])["items"]) {
		if item["boundary"] == source["boundary"] && kernel.ApMap(item["subject"])["ref"] == source["subject_ref"] {
			original = item
			count++
		}
	}
	if count != 1 {
		return bad("原决定未唯一绑定交付授权")
	}
	child := kernel.NewSemanticSession(s.Ctx, s.Root, s.Args)
	child.GovernanceVersion = s.GovernanceVersion
	child.GovernancePolicy = s.GovernancePolicy
	child.GovernanceDigest = s.GovernanceDigest
	child.Report.Profile = s.Report.Profile
	child.Registry = s.Registry
	child.RuleSession = s
	child.ExternalViews = s.ExternalViews
	s.Children = append(s.Children, child)
	comparison := kernel.ApMap(proof["comparison"])
	frozen := append([]map[string]any{kernel.ApMap(comparison["before"])}, kernel.ApRows(proof["basis"])...)
	bindOriginal := func(asset map[string]any) error {
		ref, digest := kernel.Text(asset["ref"]), strings.TrimPrefix(kernel.Text(asset["digest"]), "sha256:")
		if ref == "" || !kernel.IsSHA256(digest) {
			return s.Reject("user-decision-continuation-invalid", "原批准资产绑定无效")
		}
		// Frozen copies substitute bytes only at the original approved digest.
		for _, copy := range frozen {
			if copy["digest"] == digest {
				if _, err := s.EvidenceAsset(copy); err != nil {
					return err
				}
				child.Aliases[ref] = kernel.Text(copy["ref"])
				return nil
			}
		}
		if _, err := s.EvidenceAsset(map[string]any{"ref": ref, "digest": digest}); err != nil {
			return err
		}
		return nil
	}
	if err = bindOriginal(kernel.ApMap(original["subject"])); err != nil {
		return result, err
	}
	for _, asset := range kernel.ApRows(original["basis"]) {
		if err = bindOriginal(asset); err != nil {
			return result, err
		}
	}
	mandate, err := child.Doc(kernel.Text(source["subject_ref"]))
	if err != nil {
		return result, err
	}
	if kernel.ApNumber(mandate["schema_version"]) != 2 || mandate["kind"] != "delivery-authorization" {
		return bad("原批准不是authorization2，不能混代重解释")
	}
	for _, asset := range kernel.ApRows(mandate["basis"]) {
		if err = bindOriginal(asset); err != nil {
			return result, err
		}
	}
	result, err = ValidateUserDecisionSemantic(child, decision, []map[string]any{source})
	if err != nil {
		return result, err
	}
	if err = validateAuthorization2(child, mandate); err != nil {
		return result, err
	}
	var target map[string]any
	count = 0
	for _, row := range kernel.ApRows(mandate["targets"]) {
		if row["boundary"] != requirement["boundary"] || row["subject_ref"] != requirement["subject_ref"] {
			continue
		}
		covered := true
		for _, scope := range kernel.ApStrings(requirement["scope"]) {
			covered = covered && kernel.ApContains(row["scope"], scope)
		}
		if covered {
			target = row
			count++
		}
	}
	if count != 1 {
		return bad("原授权未唯一覆盖当前边界、资产和范围")
	}
	before := kernel.ApMap(comparison["before"])
	after := kernel.ApMap(comparison["after"])
	originalBinding := kernel.ApFind(mandate["basis"], "ref", kernel.Text(target["subject_ref"]))
	if originalBinding == nil || originalBinding["digest"] != before["digest"] || !kernel.ApEqual(after, proof["subject"]) {
		return bad("比较未绑定原批准冻结字节和当前实际资产")
	}
	beforeRaw, err := s.EvidenceAsset(before)
	if err != nil {
		return result, err
	}
	afterRaw, err := s.EvidenceAsset(after)
	if err != nil {
		return result, err
	}
	if err = compareContinuation(s, target, comparison, beforeRaw, afterRaw); err != nil {
		return result, err
	}
	if err = validateImplementationLimits(s, target, afterRaw); err != nil {
		return result, err
	}
	if requirement["boundary"] == "implementation-scope" {
		if dependencies.ValidateImplementationManifestSemantic == nil {
			return result, s.Unavailable("CAPABILITY", "缺少实际实施合同核验")
		}
		if err = dependencies.ValidateImplementationManifestSemantic(s, kernel.Text(after["ref"]), kernel.ApStrings(proof["scope"])); err != nil {
			return result, err
		}
	}
	if dependencies.TrustedTaskImplementation == nil {
		return result, s.Unavailable("CAPABILITY", "缺少当前产物实施来源核验")
	}
	implementation, err := dependencies.TrustedTaskImplementation(s, map[string]any{"candidate": after, "basis": proof["basis"]})
	if err != nil {
		return result, err
	}
	review := kernel.ApMap(proof["review"])
	reviewRef := kernel.Text(kernel.ApMap(review["provenance"])["task_ref"])
	if err = s.Verify("task", reviewRef, nil); err != nil {
		return result, err
	}
	task, err := s.Doc(reviewRef)
	if err != nil {
		return result, err
	}
	context := kernel.ApMap(task["review_context"])
	completed := kernel.ApMap(s.Report.Coverage["task_execution"])
	if completed["task_ref"] != reviewRef || completed["completed"] != true || !kernel.ApContains([]string{"Reviewer", "Verifier"}, kernel.Text(task["execution_state"])) || !kernel.ApEqual(context["candidate"], after) || !kernel.ApEqual(context["review"], review) || !kernel.ApSetEqual(context["approval_scope"], proof["scope"]) {
		return bad("延续未绑定当前真实完成复核")
	}
	if !kernel.ApSetEqual(comparison["affected_check_ids"], context["check_ids"]) || !reviewCoversBoundary(s, context["check_ids"], kernel.Text(requirement["boundary"])) && requirement["boundary"] != "implementation-scope" {
		return bad("延续遗漏当前边界的实际必需检查")
	}
	if !kernel.ApEqual(proof["verification_results"], task["verification_results"]) {
		return bad("延续验证不是当前候选的实际审查结果")
	}
	if err = s.ValidateVerificationResults(proof["verification_results"], kernel.ApStrings(task["verification_commands"]), after); err != nil {
		return result, err
	}
	if err = preserveExternalRules(s, kernel.ApMap(mandate["external_rules"]), kernel.ApMap(proof["external_rules"])); err != nil {
		return result, err
	}
	if err = s.ValidateExternalRules(kernel.ApMap(proof["external_rules"]), after, kernel.ApStrings(proof["scope"]), kernel.ApMap(implementation["provenance"])); err != nil {
		return result, err
	}
	result.continued = true
	result.reviewRef = reviewRef
	return result, nil
}

func validateAuthorization2(s *kernel.SemanticSession, mandate map[string]any) error {
	if err := s.ValidateSchema(".template-spec/process/schemas/delivery-authorization.schema.json", mandate); err != nil {
		return err
	}
	if kernel.ApNumber(mandate["schema_version"]) != 2 || mandate["kind"] != "delivery-authorization" {
		return s.Reject("user-decision-authorization-invalid", "交付授权版本无效")
	}
	assets := map[string][]byte{}
	for _, asset := range kernel.ApRows(mandate["basis"]) {
		ref := kernel.Text(asset["ref"])
		if assets[ref] != nil {
			return s.Reject("user-decision-authorization-invalid", "原授权依据重复")
		}
		raw, err := s.EvidenceAsset(asset)
		if err != nil {
			return err
		}
		assets[ref] = raw
	}
	seen := map[string]bool{}
	for _, target := range kernel.ApRows(mandate["targets"]) {
		ref := kernel.Text(target["subject_ref"])
		key := kernel.Text(target["boundary"]) + "\x00" + ref
		if seen[key] || !kernel.ApUnique(target["scope"]) || assets[ref] == nil {
			return s.Reject("user-decision-authorization-invalid", "授权目标必须唯一且已形成具体依据")
		}
		seen[key] = true
		model, _, err := comparisonModel(ref, assets[ref])
		if err != nil {
			return s.Reject("user-decision-authorization-invalid", err.Error())
		}
		protected, err := protectedSelectors(target, model)
		if err != nil {
			return s.Reject("user-decision-authorization-invalid", err.Error())
		}
		if err = validateContinuationPaths(target, model, protected); err != nil {
			return s.Reject("user-decision-authorization-invalid", err.Error())
		}
		if err = validateImplementationLimits(s, target, assets[ref]); err != nil {
			return err
		}
	}
	if len(seen) == 0 {
		return s.Reject("user-decision-authorization-invalid", "授权目标不能为空")
	}
	// Captured rules remain constraints; their results are checked for the current target at execution.
	rules := kernel.ApMap(mandate["external_rules"])
	if !kernel.ApContains([]string{"none-known", "known-verifiable", "known-unverifiable"}, kernel.Text(rules["state"])) {
		return s.Reject("EXTERNAL_REQUIREMENT", "原授权缺少外部规则状态")
	}
	return nil
}
func validateImplementationLimits(s *kernel.SemanticSession, target map[string]any, raw []byte) error {
	if target["boundary"] != "implementation-scope" {
		return nil
	}
	model, _, err := comparisonModel(kernel.Text(target["subject_ref"]), raw)
	if err != nil {
		return err
	}
	doc := kernel.ApMap(model)
	if doc["kind"] != "implementation-scope" {
		return s.Reject("user-decision-continuation-invalid", "实施范围清单无效")
	}
	limits := kernel.ApMap(target["implementation_limits"])
	if len(limits) != len(kernel.ApStrings(target["scope"])) {
		return s.Reject("user-decision-continuation-invalid", "实施授权缺少逐Ticket仓库和路径范围")
	}
	for _, ticket := range kernel.ApStrings(target["scope"]) {
		matches := []map[string]any{}
		for _, slice := range kernel.ApRows(doc["slices"]) {
			if slice["ticket_ref"] == ticket {
				matches = append(matches, slice)
			}
		}
		limit := kernel.ApMap(limits[ticket])
		if len(matches) != 1 || !kernel.ApUnique(limit["repositories"]) || !kernel.ApUnique(limit["allowed_write_paths"]) || !kernel.ApSetEqual(matches[0]["repositories"], limit["repositories"]) || !kernel.ApSetEqual(matches[0]["allowed_write_paths"], limit["allowed_write_paths"]) {
			return s.Reject("user-decision-continuation-invalid", "实际实施仓库或写范围不在原授权内")
		}
		for _, root := range kernel.ApStrings(limit["repositories"]) {
			if !filepath.IsAbs(root) || filepath.Clean(root) != root {
				return s.Reject("user-decision-continuation-invalid", "实施仓库根未明确")
			}
		}
		for _, path := range kernel.ApStrings(limit["allowed_write_paths"]) {
			if err = safefs.ValidateRef(strings.TrimSuffix(path, "/")); err != nil {
				return err
			}
		}
	}
	return nil
}
func preserveExternalRules(s *kernel.SemanticSession, original, current map[string]any) error {
	if original["state"] == "none-known" {
		return nil
	}
	if original["state"] == "known-unverifiable" {
		return s.Reject("EXTERNAL_REQUIREMENT", "原已知外部规则仍不可核验")
	}
	if original["state"] != "known-verifiable" || current["state"] != "known-verifiable" {
		return s.Reject("EXTERNAL_REQUIREMENT", "延续不能删除已知外部要求")
	}
	for _, rule := range kernel.ApRows(original["requirements"]) {
		found := false
		for _, row := range kernel.ApRows(current["requirements"]) {
			found = found || (kernel.ApEqual(rule["rule"], row["rule"]) && kernel.ApEqual(rule["scope"], row["scope"]) && rule["source"] == row["source"])
		}
		if !found {
			return s.Reject("EXTERNAL_REQUIREMENT", "当前延续缺少原已知规则")
		}
	}
	return nil
}
