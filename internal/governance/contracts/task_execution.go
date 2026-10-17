package contracts

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/schema"
)

// source is the engineering baseline's already verified frozen source, never the task's write allowlist.
func executionCandidate(s *kernel.SemanticSession, root string, source map[string]any, scope []string, inputs any) (map[string]any, []string, error) {
	if !kernel.ApUnique(source["roots"]) || !kernel.ApUnique(scope) {
		return nil, nil, s.Reject("TASK_SOURCE", "缺少工程基线的明确源码范围与冻结清单")
	}
	view := s
	if root != s.Root {
		if s.ExternalViews[root] == nil {
			return nil, nil, s.Reject("TASK_SOURCE", "源码工程未由合同登记")
		}
		view = kernel.NewSemanticSession(s.Ctx, root, s.Args)
		view.V = s.ExternalViews[root]
		s.Children = append(s.Children, view)
	}
	tree, err := s.Git(root, "ls-tree", "-r", "-z", kernel.Text(source["base_commit"]))
	if err != nil {
		return nil, nil, err
	}
	modes := map[string]string{}
	for _, entry := range strings.Split(string(tree), "\x00") {
		parts := strings.SplitN(entry, "\t", 2)
		if len(parts) == 2 {
			meta := strings.Fields(parts[0])
			if len(meta) == 3 {
				modes[parts[1]] = meta[0]
			}
		}
	}
	frozen := map[string]string{}
	for _, file := range kernel.ApRows(source["files"]) {
		frozen[kernel.Text(file["path"])] = strings.TrimPrefix(kernel.Text(file["sha256"]), "sha256:")
	}
	files := map[string]bool{}
	for _, r := range kernel.ApStrings(source["roots"]) {
		if !kernel.ContractPath(r) {
			return nil, nil, s.Reject("TASK_SOURCE", "源码roots必须明确；不能猜测或以证据排除列表缩窄")
		}
		entries, err := view.Scan(r)
		if err != nil {
			return nil, nil, err
		}
		for _, ref := range entries {
			excluded := false
			for _, part := range strings.Split(ref, "/") {
				excluded = excluded || kernel.ApContains([]string{".git", "target", "node_modules"}, part)
			}
			if !excluded {
				files[ref] = true
			}
		}
	}
	for ref := range frozen {
		files[ref] = true
	}
	paths := []string{}
	for ref := range files {
		paths = append(paths, ref)
	}
	sort.Strings(paths)
	inventory := []any{}
	changed := []string{}
	for _, ref := range paths {
		p, err := safefs.Path(root, ref)
		if err != nil {
			return nil, nil, err
		}
		info, err := os.Lstat(p)
		row := map[string]any{"path": ref}
		digest := ""
		if os.IsNotExist(err) {
			row["deleted"] = true
			row["digest"] = nil
		} else {
			if err != nil {
				return nil, nil, err
			}
			if !info.Mode().IsRegular() {
				return nil, nil, s.Reject("TASK_SOURCE", "源码必须为普通文件")
			}
			raw, err := backendCodeBytes(s, root, ref)
			if err != nil {
				return nil, nil, err
			}
			digest = safefs.Digest(raw)
			row["digest"] = digest
			row["mode"] = info.Mode().Perm()
		}
		modeChanged := false
		if info != nil && frozen[ref] != "" && modes[ref] != "" {
			executable := info.Mode().Perm()&0111 != 0
			modeChanged = executable != (modes[ref] == "100755")
		}
		if digest != frozen[ref] || modeChanged {
			allowed := false
			for _, parent := range scope {
				allowed = allowed || ContractWithin(ref, parent)
			}
			if !allowed {
				return nil, nil, s.Reject("TASK_SCOPE", "当前源码变化越出批准范围: "+ref)
			}
			changed = append(changed, ref)
		}
		inventory = append(inventory, row)
	}
	sortedScope := append([]string{}, scope...)
	sort.Strings(sortedScope)
	value := map[string]any{"repository_root": filepath.Clean(root), "source": source, "scope": sortedScope, "input_bindings": inputs, "files": inventory}
	return map[string]any{"ref": root, "digest": strings.TrimPrefix(ContractDigest(value), "sha256:")}, changed, nil
}

func validateTask3Execution(s *kernel.SemanticSession, ref string, task, candidate map[string]any, changed, protectedPaths []string) error {
	actual, err := s.ValidateProvenance(kernel.ApMap(task["provenance"]), ref, kernel.Text(candidate["digest"]))
	if err != nil {
		return err
	}
	if actual["phase"] != "execution" {
		return s.Reject("TASK_ORIGIN", "当前执行必须使用实际execution记录，不能消费派发时间")
	}
	executionScope := task["allowed_write_paths"]
	if task["execution_state"] == "Reviewer" || task["execution_state"] == "Verifier" {
		executionScope = kernel.ApMap(task["review_context"])["approval_scope"]
	}
	for key, want := range map[string]any{"input_bindings": task["input_bindings"], "baseline": task["baseline"], "scope": executionScope, "expected_outputs": task["expected_outputs"]} {
		if !kernel.ApEqual(actual[key], want) {
			return s.Reject("TASK_ORIGIN", "实际执行未绑定当前"+key)
		}
	}
	result := kernel.ApMap(task["result"])
	completed := task["workflow_status"] == "resolved" || result["result"] == "completed"
	if !completed {
		if task["verification_status"] == "executed" {
			return s.ValidateVerificationResultsDigest(task["verification_results"], kernel.ApStrings(task["verification_commands"]), kernel.Text(candidate["digest"]))
		}
		return nil
	}
	if task["workflow_status"] != "resolved" || result["result"] != "completed" || task["verification_status"] != "executed" || actual["result"] != "passed" || kernel.ApNumber(actual["exit_code"]) != 0 || !kernel.ApIsArray(actual["blocking_findings"]) || len(kernel.ApArray(actual["blocking_findings"])) > 0 {
		return s.Reject("TASK_RESULT", "完成状态、实际执行或未解决问题不一致")
	}
	for _, field := range []string{"result_schema", "work_unit", "workflow_reference", "skill", "changed_files", "context_reconciliation", "evidence_refs", "deferred_seams", "drift", "violation", "new_impacts", "stale_candidates", "next_route", "blocking_signals"} {
		if _, ok := result[field]; !ok {
			return s.Reject("TASK_RESULT", "完成结果缺少: "+field)
		}
	}
	if result["result_schema"] != "workflow-execution-result-v1" || result["work_unit"] != task["work_unit_id"] || !kernel.ApContains(task["inputs"], kernel.Text(result["workflow_reference"])) || !kernel.ApContains(kernel.ApMap(task["skill_source"])["skills"], kernel.Text(result["skill"])) {
		return s.Reject("TASK_RESULT", "结果未绑定当前工作单元、流程与Skill")
	}
	for _, field := range []string{"new_impacts", "stale_candidates", "blocking_signals", "drift", "violation"} {
		if !kernel.ApIsArray(result[field]) || len(kernel.ApArray(result[field])) > 0 {
			return s.Reject("TASK_RESULT", "完成仍有阻断或缺少明确状态: "+field)
		}
	}
	if !kernel.ApIsArray(result["changed_files"]) || changed != nil && !kernel.ApSetEqual(result["changed_files"], changed) {
		return s.Reject("TASK_RESULT", "changed_files与实际源码变化不一致")
	}
	for _, path := range kernel.ApStrings(result["changed_files"]) {
		allowed := false
		for _, parent := range kernel.ApStrings(task["allowed_write_paths"]) {
			allowed = allowed || ContractWithin(path, parent)
		}
		if !allowed {
			return s.Reject("TASK_SCOPE", "changed_files越出批准范围")
		}
	}
	if artifacts, present := result["changed_artifacts"]; present {
		if !kernel.ApIsArray(artifacts) || len(kernel.ApArray(artifacts)) > 0 && !kernel.ApUnique(artifacts) {
			return s.Reject("TASK_RESULT", "changed_artifacts必须是明确无重复路径")
		}
		for _, artifact := range kernel.ApStrings(artifacts) {
			if !kernel.ContractPath(artifact) || !kernel.ApContains(result["changed_files"], artifact) {
				return s.Reject("TASK_RESULT", "changed_artifacts未对应实际changed_files")
			}
		}
	}
	reconciliation := kernel.ApMap(result["context_reconciliation"])
	if reconciliation["status"] != "reconciled" || !kernel.ApContains(result["evidence_refs"], kernel.Text(reconciliation["ref"])) {
		return s.Reject("TASK_CONTEXT", "缺少当前context reconciliation")
	}
	if !kernel.ApUnique(result["evidence_refs"]) || !kernel.ApContains(task["downstream_consumers"], kernel.Text(result["next_route"])) {
		return s.Reject("TASK_RESULT", "完成证据或下游汇合不明确")
	}
	for _, evidence := range kernel.ApStrings(task["expected_evidence_files"]) {
		raw, err := backendCodeBytes(s, kernel.Text(kernel.ApMap(task["baseline"])["repository_root"]), evidence)
		if err != nil {
			return err
		}
		if strings.TrimSpace(string(raw)) == "" {
			return s.Reject("TASK_EVIDENCE", "完成证据为空")
		}
	}
	for _, evidence := range kernel.ApStrings(result["evidence_refs"]) {
		raw, err := s.Bytes(evidence)
		if err != nil {
			return err
		}
		if strings.TrimSpace(string(raw)) == "" {
			return s.Reject("TASK_EVIDENCE", "完成证据为空")
		}
	}
	if err = s.ValidateVerificationResultsDigest(task["verification_results"], kernel.ApStrings(task["verification_commands"]), kernel.Text(candidate["digest"])); err != nil {
		return err
	}
	if s.Report.Coverage == nil {
		s.Report.Coverage = map[string]any{}
	}
	s.Report.Coverage["task_execution"] = map[string]any{"task_ref": ref, "candidate": candidate, "provenance": task["provenance"], "scope": task["allowed_write_paths"], "completed": true}
	if protectedPaths != nil {
		kernel.ApMap(s.Report.Coverage["task_execution"])["candidate_scope"] = map[string]any{"repository_root": kernel.ApMap(task["baseline"])["repository_root"], "paths": protectedPaths}
	}
	return nil
}

func validateTaskOutputRefs(s *kernel.SemanticSession, ref string, task map[string]any) error {
	if task["execution_state"] != "Drafter" && task["execution_state"] != "Explorer" {
		return nil
	}
	for _, output := range kernel.ApStrings(task["expected_outputs"]) {
		if !kernel.ContractPath(output) {
			return s.Reject("TASK_OUTPUT", "task3产出必须为派发前冻结的具体文件引用")
		}
		allowed := false
		for _, scope := range kernel.ApStrings(task["allowed_write_paths"]) {
			allowed = allowed || ContractWithin(output, scope)
		}
		if !allowed {
			return s.Reject("TASK_OUTPUT", "产出未在授权范围内: "+output)
		}
		if output == ref || output == kernel.Text(kernel.ApMap(task["provenance"])["execution_ref"]) || kernel.ApContains(task["expected_evidence_files"], output) {
			return s.Reject("TASK_OUTPUT", "候选产出与task/结果证据自引用")
		}
		if _, err := safefs.Path(kernel.Text(kernel.ApMap(task["baseline"])["repository_root"]), output); err != nil {
			return err
		}
	}
	return nil
}
func draftExecutionCandidate(s *kernel.SemanticSession, task map[string]any) (map[string]any, error) {
	outputs := append([]string{}, kernel.ApStrings(task["expected_outputs"])...)
	sort.Strings(outputs)
	assets := []any{}
	root := kernel.Text(kernel.ApMap(task["baseline"])["repository_root"])
	for _, ref := range outputs {
		raw, err := backendCodeBytes(s, root, ref)
		if err != nil {
			return nil, err
		}
		if len(strings.TrimSpace(string(raw))) == 0 {
			return nil, s.Reject("TASK_OUTPUT", "完成产出为空")
		}
		assets = append(assets, map[string]any{"ref": ref, "digest": safefs.Digest(raw)})
	}
	value := map[string]any{"outputs": assets, "input_bindings": task["input_bindings"], "baseline": task["baseline"], "scope": task["allowed_write_paths"], "expected_outputs": task["expected_outputs"]}
	return map[string]any{"ref": kernel.ApMap(task["contract"])["contract_ref"], "digest": strings.TrimPrefix(ContractDigest(value), "sha256:")}, nil
}

// The basis is the authority lookup set; review.independence never selects implementation.
func TrustedTaskImplementation(s *kernel.SemanticSession, review map[string]any) (map[string]any, error) {
	var source map[string]any
	matches := 0
	seen := map[string]bool{}
	for _, asset := range kernel.ApRows(review["basis"]) {
		ref := kernel.Text(asset["ref"])
		if seen[ref] {
			return nil, s.Reject("TASK_REVIEW", "审查依据重复")
		}
		seen[ref] = true
		raw, err := s.EvidenceAsset(asset)
		if err != nil {
			return nil, err
		}
		value, parseErr := schema.Parse(raw)
		task, object := kernel.Object(value)
		if parseErr != nil || !object {
			continue
		}
		if kernel.ContractN(task["schema_version"]) != 3 || task["workflow_status"] != "resolved" || kernel.ApMap(task["result"])["result"] != "completed" || !kernel.ApContains([]string{"Drafter", "Explorer", "Worker"}, kernel.Text(task["execution_state"])) {
			continue
		}
		if err = s.Verify("task", ref, nil); err != nil {
			return nil, err
		}
		verified := kernel.ApMap(s.Report.Coverage["task_execution"])
		matchesCandidate := kernel.ApEqual(verified["candidate"], review["candidate"])
		if !matchesCandidate {
			matchesCandidate, err = taskOutputMember(s, task, verified, kernel.ApMap(review["candidate"]))
			if err != nil {
				return nil, err
			}
		}
		if verified["task_ref"] == ref && verified["completed"] == true && matchesCandidate {
			matches++
			source = ContractCopy(verified)
		}
	}
	if matches != 1 {
		return nil, s.Reject("TASK_REVIEW", "必须从当前候选依据找到唯一已验证完成的实施任务")
	}
	return source, nil
}

func trustedTaskImplementation(s *kernel.SemanticSession, review map[string]any) (map[string]any, string, []string, map[string]any, error) {
	source, err := TrustedTaskImplementation(s, review)
	if err != nil {
		return nil, "", nil, nil, err
	}
	return kernel.ApMap(source["provenance"]), kernel.Text(source["task_ref"]), kernel.ApStrings(source["scope"]), kernel.ApMap(source["candidate_scope"]), nil
}

// Raw assets are members only under the completed task's verified physical root.
func taskOutputMember(s *kernel.SemanticSession, task, source, asset map[string]any) (bool, error) {
	protection := kernel.ApMap(source["candidate_scope"])
	root := kernel.Text(protection["repository_root"])
	if !filepath.IsAbs(root) {
		return false, nil
	}
	v, ref, err := s.ReferenceView(kernel.Text(asset["ref"]))
	if err != nil {
		return false, err
	}
	actual, err := safefs.Path(v.Root, ref)
	if err != nil {
		return false, err
	}
	for _, output := range kernel.ApStrings(task["expected_outputs"]) {
		expected, err := safefs.Path(root, output)
		if err != nil {
			return false, err
		}
		if expected != actual {
			continue
		}
		covered := false
		for _, path := range kernel.ApStrings(protection["paths"]) {
			covered = covered || kernel.TaskWithinPath(output, path)
		}
		if !covered {
			return false, s.Reject("TASK_REVIEW", "产物不属于已验证候选范围")
		}
		if _, err = s.EvidenceAsset(asset); err != nil {
			return false, err
		}
		return true, nil
	}
	return false, nil
}

// protection is emitted by the uniquely matched completed task's candidate computation.
// Compare physical roots too: equal relative names in different repositories are not overlaps.
func validateReviewCandidateWrites(s *kernel.SemanticSession, task, protection map[string]any) error {
	root := kernel.Text(protection["repository_root"])
	if !filepath.IsAbs(root) || !kernel.ApUnique(protection["paths"]) {
		return s.Reject("TASK_REVIEW", "已验证实施候选缺少真实路径范围")
	}
	reviewerRoot := kernel.Text(kernel.ApMap(task["baseline"])["repository_root"])
	writes := append([]string{}, kernel.ApStrings(task["allowed_write_paths"])...)
	writes = append(writes, kernel.ApStrings(kernel.ApMap(task["result"])["changed_files"])...)
	for _, ref := range kernel.ApStrings(protection["paths"]) {
		protected, err := safefs.Path(root, strings.TrimSuffix(ref, "/"))
		if err != nil {
			return err
		}
		for _, write := range writes {
			actual, err := safefs.Path(reviewerRoot, strings.TrimSuffix(write, "/"))
			if err != nil {
				return err
			}
			if actual == protected || strings.HasPrefix(actual, protected+string(filepath.Separator)) || strings.HasPrefix(protected, actual+string(filepath.Separator)) {
				return s.Reject("TASK_REVIEW", "审查写范围或实际变化覆盖受审候选: "+ref)
			}
		}
	}
	return nil
}

func sliceTaskCandidate(s *kernel.SemanticSession, task map[string]any) (map[string]any, []string, []string, error) {
	contract := kernel.ApMap(task["contract"])
	ref := kernel.Text(contract["slice_contract_ref"])
	if ref == "" || contract["contract_ref"] != ref || contract["lifecycle_ref"] != nil || contract["maintenance_ref"] != nil || kernel.ApMap(task["convergence"])["parent_work_unit"] != "work-unit.slice-implementation" {
		return nil, nil, nil, s.Reject("TASK_CONTRACT", "Slice引用与汇合不一致")
	}
	c, err := LoadNativeSlice(s, ref)
	if err != nil {
		return nil, nil, nil, err
	}
	if err = validateSlice4Selection(s, c); err != nil {
		return nil, nil, nil, err
	}
	if c.Raw["contract_id"] != contract["contract_id"] || c.Raw["contract_version"] != contract["contract_version"] {
		return nil, nil, nil, s.Reject("TASK_CONTRACT", "Slice身份/版本不一致")
	}
	unit := kernel.ApFind(c.Normalized["work_units"], "id", kernel.Text(task["work_unit_id"]))
	if unit == nil {
		return nil, nil, nil, s.Reject("TASK_CONTRACT", "Slice工作单元不存在")
	}
	stages := []string{}
	for _, stage := range kernel.ApRows(s.Registry["stages"]) {
		id := kernel.Text(stage["id"])
		if id == "stage.vertical-slice-implementation" || id == "stage.slice-implementation" {
			stages = append(stages, id)
		}
	}
	if len(stages) != 1 || task["stage_id"] != stages[0] {
		return nil, nil, nil, s.Reject("TASK_STAGE", "任务未绑定当前Slice实现阶段")
	}
	if !kernel.ApSetEqual(task["allowed_write_paths"], unit["allowed_write_paths"]) {
		return nil, nil, nil, s.Reject("TASK_SCOPE", "任务范围不等于批准的工作单元范围")
	}
	for field, want := range map[string]any{"verification_commands": kernel.ApMap(unit["work_unit"])["verification_commands"], "expected_evidence_files": kernel.ApMap(unit["work_unit"])["expected_evidence"]} {
		if !kernel.ApSetEqual(task[field], want) {
			return nil, nil, nil, s.Reject("TASK_CONTRACT", "任务替换或遗漏Slice "+field)
		}
	}
	for _, forbidden := range kernel.ApStrings(kernel.ApMap(unit["work_unit"])["forbidden_patterns"]) {
		if !kernel.ApContains(task["forbidden_actions"], forbidden) {
			return nil, nil, nil, s.Reject("TASK_CONTRACT", "任务遗漏Slice禁止动作")
		}
	}
	gates := kernel.ApStrings(contract["gate_refs"])
	if len(gates) != 1 {
		return nil, nil, nil, s.Reject("TASK_CONTRACT", "Slice需要唯一批准checkpoint")
	}
	opts := map[string]string{"unit": kernel.Text(task["work_unit_id"]), "approval-ref": gates[0]}
	selected, _, _, err := contractSliceSelect(s, c, opts)
	if err != nil {
		return nil, nil, nil, err
	}
	base, err := ContractBoundDoc(s, selected.Basis["engineering_baseline"])
	if err != nil {
		return nil, nil, nil, err
	}
	source := kernel.ApMap(base["source"])
	identity := kernel.ApMap(kernel.ApMap(selected.Normalized["resolution"])["architecture_identity"])
	if kernel.ContractN(identity["schema_version"]) != 2 || identity["source_kind"] != "existing-registration" {
		return nil, nil, nil, s.Reject("TASK_SOURCE", "缺少可由现有工程来源算法验证的冻结source；不能猜测源码身份")
	}
	if !kernel.ApUnique(source["roots"]) || len(kernel.ApRows(source["files"])) == 0 {
		return nil, nil, nil, s.Reject("TASK_SOURCE", "实际工程基线缺少source.roots/files；不能使用总写范围猜测源码")
	}
	baseline := kernel.ApMap(task["baseline"])
	if baseline["repository_root"] != unit["project_root"] || baseline["commit"] != source["base_commit"] || baseline["digest"] != strings.TrimPrefix(ContractDigest(source), "sha256:") {
		return nil, nil, nil, s.Reject("TASK_BASELINE", "任务基线未绑定实际工程冻结source")
	}
	// Existing native freshness validates the engineering source's Git tree, patches and registration.
	if err = s.Verify("slice", ref, opts); err != nil {
		return nil, nil, nil, err
	}
	if err = AssertImplementationDecisionSemantic(s, map[string]any{"slice_contract_ref": ref, "vertical_slice_ticket_ref": kernel.ApMap(c.Normalized["lifecycle_refs"])["ticket"], "user_decisions": task["user_decisions"]}); err != nil {
		return nil, nil, nil, err
	}
	root := kernel.Text(baseline["repository_root"])
	excludedEvidence := append([]string{}, kernel.ApStrings(task["expected_evidence_files"])...)
	if root == s.Root {
		excludedEvidence = append(excludedEvidence, kernel.Text(kernel.ApMap(task["provenance"])["execution_ref"]), kernel.Text(kernel.ApMap(task["provenance"])["task_ref"]))
	}
	for _, evidence := range excludedEvidence {
		for _, sourceRoot := range kernel.ApStrings(source["roots"]) {
			if ContractWithin(evidence, sourceRoot) {
				return nil, nil, nil, s.Reject("TASK_SOURCE", "源码范围包含任务或执行证据，会形成自引用；需要真实工程来源范围")
			}
		}
	}
	candidate, changed, err := executionCandidate(s, root, source, kernel.ApStrings(task["allowed_write_paths"]), task["input_bindings"])
	if err == nil {
		candidate["ref"] = ref
	}
	return candidate, changed, kernel.ApStrings(source["roots"]), err
}
