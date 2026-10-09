package governance

import "github.com/iloveZzz/yss-cli/internal/safefs"

// The frozen package proves its captured checkpoint. Currentness of the source
// is established by original approvals and stable assets, not mutable progress.
func guidanceCurrentSpecBaseline(source *semanticSession, cpRef string, cp, manifest map[string]any, snapshot *semanticSession) error {
	current, _, err := verifySpecBaselineSource(source, cpRef)
	if err != nil {
		return err
	}
	captured := semMap(manifest["source"])
	for _, key := range []string{"profile_id", "feature_id", "checkpoint_ref", "spec_ref", "spec_digest", "plan_ref", "domain_strategy_ref", "stage_decision_package_ref", "product_design_required"} {
		if !contractSame(current[key], captured[key]) {
			return source.reject("SPEC_BASELINE_CURRENT", "接收基线与当前批准来源绑定不同："+key)
		}
	}
	stable := map[string]bool{}
	for _, key := range []string{"spec_ref", "plan_ref", "domain_strategy_ref", "stage_decision_package_ref"} {
		if ref := text(captured[key]); ref != "" {
			stable[ref] = true
		}
	}
	frozenCP, err := snapshot.doc(text(captured["checkpoint_ref"]))
	if err != nil {
		return err
	}
	for _, id := range []string{"gate.plan-approved", "gate.spec-baseline-approved"} {
		before, now := semMap(semMap(frozenCP["gates"])[id]), semMap(semMap(cp["gates"])[id])
		for _, key := range []string{"approval_ref", "subject_ref"} {
			if before[key] != now[key] {
				return source.reject("SPEC_BASELINE_CURRENT", "当前来源已替换基线原批准："+id)
			}
			if ref := text(before[key]); ref != "" {
				stable[ref] = true
			}
		}
		// The approval's original Context dependency is part of the stable basis.
		for _, row := range semList(before["basis"]) {
			ref := text(semMap(row)["ref"])
			if ref == "CONTEXT.md" {
				stable[ref] = true
			}
		}
	}
	for _, row := range semList(manifest["files"]) {
		binding := semMap(row)
		ref := text(binding["original_ref"])
		if !stable[ref] {
			continue
		}
		raw, err := source.bytes(ref)
		if err != nil {
			return err
		}
		if "sha256:"+safefs.Digest(raw) != binding["sha256"] {
			return source.reject("SPEC_BASELINE_CURRENT", "当前批准来源资产已变化："+ref)
		}
		delete(stable, ref)
	}
	if len(stable) != 0 {
		return source.reject("SPEC_BASELINE_CURRENT", "冻结基线未完整捕获当前原批准依赖")
	}
	return nil
}
