package governance

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/iloveZzz/yss-cli/internal/safefs"
)

func init() { registerSemanticValidator("context-reconciliation", verifySemanticReconciliation) }

func (s *semanticSession) contextFiles() ([]string, error) {
	if s.v.virtual != nil {
		rows := []string{}
		for ref := range s.v.virtual {
			if err := s.guard(); err != nil {
				return nil, err
			}
			rows = append(rows, ref)
			if filepath.Base(ref) == "CONTEXT-MAP.md" || filepath.Base(ref) == "context.md" || filepath.Base(ref) == "CONTEXT.md" && ref != "CONTEXT.md" {
				return nil, s.reject("CONTEXT", "归档 Context 文件身份非法: "+ref)
			}
		}
		sort.Strings(rows)
		return rows, nil
	}
	skip := map[string]bool{".git": true, ".codegraph": true, ".template-source": true, "node_modules": true, "dist": true, "build": true}
	rows := []string{}
	err := filepath.WalkDir(s.root, func(path string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if e = s.guard(); e != nil {
			return e
		}
		ref, e := filepath.Rel(s.root, path)
		if e != nil {
			return e
		}
		ref = filepath.ToSlash(ref)
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if d.IsDir() && ref != "." {
			if skip[d.Name()] {
				return filepath.SkipDir
			}
			if _, e = os.Lstat(filepath.Join(path, ".git")); e == nil {
				rows = append(rows, "repository:"+ref)
				return filepath.SkipDir
			} else if !os.IsNotExist(e) {
				return e
			}
		}
		rows = append(rows, ref)
		if len(rows) > 50000 {
			return s.unavailable("CAPABILITY", "Context 扫描超过50000项")
		}
		if !d.IsDir() && (d.Name() == "CONTEXT-MAP.md" || d.Name() == "context.md" || d.Name() == "CONTEXT.md" && ref != "CONTEXT.md") {
			return s.reject("CONTEXT", "Context 文件身份非法: "+ref)
		}
		return nil
	})
	sort.Strings(rows)
	return rows, err
}
func (s *semanticSession) contextContract() (map[string]any, error) {
	rows, err := s.contextFiles()
	if err != nil {
		return nil, err
	}
	if s.contextInventory != nil && !equalStrings(rows, s.contextInventory) {
		return nil, s.unavailable("INPUT_DRIFT", "Context 扫描集合变化")
	}
	s.contextInventory = rows
	b, err := s.bytes("CONTEXT.md")
	if err != nil {
		return nil, err
	}
	result, err := contextContractBytes(b)
	if err != nil {
		return nil, s.reject("CONTEXT", err.Error())
	}
	return result, nil
}
func (s *semanticSession) contextSnapshot(snapshot map[string]any) (map[string]any, error) {
	contract, err := s.contextContract()
	if err != nil {
		return nil, err
	}
	n, ok := integer(snapshot["context_schema_version"])
	if !ok || n != 1 || text(snapshot["context_ref"]) != "CONTEXT.md" {
		return nil, s.reject("CONTEXT", "Context snapshot 版本或文件身份非法")
	}
	terms := contract["business_terms"].([]Term)
	byID := map[string]Term{}
	for _, t := range terms {
		byID[t.TermRef] = t
	}
	selected := []Term{}
	seen := map[string]bool{}
	for _, id := range semStrings(snapshot["term_refs"]) {
		t, ok := byID[id]
		if !ok || seen[id] {
			return nil, s.reject("CONTEXT_REFERENCE", "术语缺失或重复: "+id)
		}
		seen[id] = true
		selected = append(selected, t)
	}
	digest, err := termsDigest(selected)
	if err != nil {
		return nil, err
	}
	if snapshot["document_digest"] != contract["document_digest"] || snapshot["referenced_terms_digest"] != digest {
		return nil, s.reject("CONTEXT_DRIFT", "Context snapshot 摘要变化")
	}
	return contract, nil
}
func verifySemanticReconciliation(s *semanticSession, ref string, opts map[string]string) error {
	v, err := s.doc(ref)
	if err != nil {
		return err
	}
	if err = s.validateSchema(".template-spec/process/schemas/context-reconciliation.schema.json", v); err != nil {
		return err
	}
	identity, err := s.doc("yss-project.yaml")
	if err != nil {
		return err
	}
	if v["repository_mode"] != identity["repository_mode"] {
		return s.reject("CONTEXT", "对账仓库身份不一致")
	}
	contract, err := s.contextSnapshot(semMap(v["context_snapshot"]))
	if err != nil {
		return err
	}
	known := map[string]bool{}
	for _, t := range contract["business_terms"].([]Term) {
		known[t.TermRef] = true
	}
	changed := map[string]bool{}
	for _, k := range []string{"added", "updated", "deprecated"} {
		for _, id := range semStrings(semMap(v["changes"])[k]) {
			if !known[id] || changed[id] {
				return s.reject("CONTEXT_REFERENCE", "变更术语缺失或跨分类重复")
			}
			changed[id] = true
		}
	}
	status := text(v["status"])
	unresolved := semList(v["unresolved_terms"])
	if text(v["repository_mode"]) == "project-instance" && status != "reconciled" || status == "reconciled" && len(unresolved) > 0 || status == "blocked" && len(unresolved) == 0 || status == "not-applicable" && (text(v["repository_mode"]) != "template-source" || strings.TrimSpace(text(v["reason"])) == "") {
		return s.reject("CONTEXT", "Context 对账状态不允许批准或流转")
	}
	for _, r := range semStrings(v["evidence_refs"]) {
		if strings.HasPrefix(r, "https://") || strings.HasPrefix(r, "http://") {
			continue
		}
		if _, err = s.bytes(r); err != nil {
			return err
		}
	}
	s.report.Applicability = append(s.report.Applicability, map[string]any{"id": "context-reconciliation", "status": status, "reason": v["reason"], "source_ref": ref})
	return nil
}

// Shared file-level currentness assertion, without running any transaction.
func (s *semanticSession) currentAsset(ref string) error {
	if _, err := safefs.Path(s.root, s.localRef(ref)); err != nil {
		return s.unavailable("PATH", err.Error())
	}
	exists, err := s.exists(".yss/asset-transactions/active.json")
	if err != nil {
		return err
	}
	if exists {
		return s.reject("ASSET_TRANSACTION_PENDING", "先恢复未完成的资产事务")
	}
	files, err := s.scan(".yss/asset-transactions")
	if err != nil {
		return err
	}
	for _, file := range files {
		name := filepath.Base(file)
		if !strings.HasSuffix(name, ".json") || !isSHA256(strings.TrimSuffix(name, ".json")) {
			continue
		}
		receipt, e := s.doc(file)
		if e != nil {
			return e
		}
		if receipt["status"] == "applied" && text(semMap(semMap(receipt["migration"])["source_to_target"])[ref]) != "" {
			return s.reject("ASSET_HISTORICAL_ONLY", "资产已迁移，仅能检查历史结构: "+ref)
		}
	}
	_, err = s.bytes(ref)
	return err
}
