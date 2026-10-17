package contracts

import (
	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
	"github.com/iloveZzz/yss-cli/internal/safefs"
)

func ContractExecutionEvidence(s *kernel.SemanticSession, result map[string]any, c *NativeSlice, work map[string]any) error {
	if kernel.ContractN(result["evidence_binding_version"]) != 1 {
		return s.Reject("EXECUTION_EVIDENCE_LEGACY", "缺少原始证据绑定协议")
	}
	bound, e := s.Bind(c.Ref)
	if e != nil {
		return e
	}
	if kernel.Text(kernel.SemMap(result["consumed_contract"])["contract_digest"]) != bound.Digest {
		return s.Reject("EXECUTION_EVIDENCE_STALE", "执行证据合同原字节摘要不一致")
	}
	unitRoot := kernel.Text(work["project_root"])
	cross := len(c.Repositories) > 0
	read := func(root, ref string) ([]byte, error) {
		if cross || kernel.ContractN(c.Raw["schema_version"]) == 4 && root != s.Root {
			return s.ExternalBytes(root, ref)
		}
		return s.Bytes(ref)
	}
	sourceRead := func(root, ref string) ([]byte, error) {
		if root == s.Root {
			return s.Bytes(ref)
		}
		return s.ExternalBytes(root, ref)
	}
	files := kernel.SemList(result["evidence_files"])
	checks := kernel.SemList(work["verification"])
	allowedRoots := map[string]bool{unitRoot: true}
	for _, v := range checks {
		check := kernel.SemMap(v)
		for _, root := range kernel.SemStrings(check["dependency_roots"]) {
			allowedRoots[root] = true
		}
		rows := []map[string]any{}
		for _, v := range kernel.SemList(result["verification_results"]) {
			r := kernel.SemMap(v)
			if r["verification_id"] == check["id"] {
				rows = append(rows, r)
			}
		}
		if len(rows) != 1 {
			return s.Reject("EXECUTION_EVIDENCE_INVALID", "验证 ID 缺失或重复")
		}
		row := rows[0]
		exit, ok := kernel.Integer(row["exit_code"])
		if !ok || exit != 0 || row["command"] != check["command"] || row["cwd"] != check["cwd"] || !ContractDate(row["executed_at"]) || !kernel.SameStringSet(row["acceptance_refs"], check["acceptance_refs"]) {
			return s.Reject("EXECUTION_EVIDENCE_INVALID", "验证命令/cwd/退出码/验收绑定冲突")
		}
		if check["dependency_roots"] != nil && !ContractSame(row["dependency_roots"], check["dependency_roots"]) {
			return s.Reject("EXECUTION_EVIDENCE_INVALID", "验证依赖工程集合与冻结命令不一致")
		}
		refs := kernel.SemStrings(row["evidence_refs"])
		for _, ref := range kernel.SemStrings(check["expected_evidence"]) {
			if !kernel.SemHas(refs, ref) {
				return s.Reject("EXECUTION_EVIDENCE_REQUIRED", "验证未关联所需证据")
			}
		}
		covered := map[string]bool{}
		for _, ref := range refs {
			matched := false
			for _, v := range files {
				file := kernel.SemMap(v)
				if kernel.Text(file["path"]) != ref || cross && kernel.Text(file["project_root"]) != unitRoot {
					continue
				}
				matched = true
				behavior := kernel.Text(file["behavior_ref"])
				if !kernel.SemHas(check["acceptance_refs"], behavior) {
					return s.Reject("EXECUTION_EVIDENCE_INVALID", "证据关联无关行为")
				}
				bytes, e := read(unitRoot, ref)
				if e != nil {
					return e
				}
				if "sha256:"+safefs.Digest(bytes) != kernel.Text(file["digest"]) {
					return s.Reject("EXECUTION_EVIDENCE_STALE", "证据原字节变化")
				}
				covered[behavior] = true
			}
			if !matched {
				return s.Reject("EXECUTION_EVIDENCE_REQUIRED", "未绑定验证证据")
			}
		}
		for _, behavior := range kernel.SemStrings(check["acceptance_refs"]) {
			if !covered[behavior] {
				return s.Reject("EXECUTION_EVIDENCE_REQUIRED", "验收缺少行为证据")
			}
		}
	}
	sourceBindings := kernel.SemList(result["source_bindings"])
	if len(sourceBindings) == 0 {
		return s.Reject("EXECUTION_SOURCE_REQUIRED", "执行报告缺少固定输入")
	}
	for _, v := range sourceBindings {
		entry := kernel.SemMap(v)
		root := kernel.First(kernel.Text(entry["project_root"]), unitRoot)
		ref := kernel.Text(entry["path"])
		if !allowedRoots[root] || cross && c.Repositories[root] == nil {
			return s.Reject("EXECUTION_SOURCE_REPOSITORY", "执行输入仓库未批准")
		}
		if entry["deleted"] == true {
			if value, present := entry["digest"]; !present || value != nil {
				return s.Reject("EXECUTION_SOURCE_STALE", "删除输入必须显式声明digest:null")
			}
			var exists bool
			if root != s.Root {
				view := s.ExternalViews[root]
				if view == nil {
					return s.Unavailable("CAPABILITY", "执行输入工程没有已验证登记")
				}
				d, e := view.Watch(ref)
				if e != nil {
					return e
				}
				exists = d.Type != "missing"
			} else {
				var e error
				exists, e = s.Exists(ref)
				if e != nil {
					return e
				}
			}
			if exists || entry["digest"] != nil {
				return s.Reject("EXECUTION_SOURCE_STALE", "已删除源仍存在或声明摘要")
			}
		} else {
			bytes, e := sourceRead(root, ref)
			if e != nil {
				return e
			}
			if "sha256:"+safefs.Digest(bytes) != kernel.Text(entry["digest"]) {
				return s.Reject("EXECUTION_SOURCE_STALE", "执行输入已变化")
			}
		}
	}
	for _, v := range kernel.SemList(result["changed_files"]) {
		ref, root := kernel.Text(v), unitRoot
		if m, ok := kernel.Object(v); ok {
			ref = kernel.Text(m["path"])
			root = kernel.First(kernel.Text(m["project_root"]), unitRoot)
		}
		found := false
		for _, v := range sourceBindings {
			b := kernel.SemMap(v)
			found = found || kernel.Text(b["path"]) == ref && kernel.First(kernel.Text(b["project_root"]), unitRoot) == root
		}
		if !found {
			return s.Reject("EXECUTION_SOURCE_REQUIRED", "改动未绑定当前源字节")
		}
	}
	return nil
}
