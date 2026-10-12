package kernel

import (
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func (s *SemanticSession) ContextFiles() ([]string, error) {
	if s.V.Virtual != nil {
		rows := []string{}
		for ref := range s.V.Virtual {
			if err := s.Guard(); err != nil {
				return nil, err
			}
			rows = append(rows, ref)
			if filepath.Base(ref) == "CONTEXT-MAP.md" || filepath.Base(ref) == "context.md" || filepath.Base(ref) == "CONTEXT.md" && ref != "CONTEXT.md" {
				return nil, s.Reject("CONTEXT", "归档 Context 文件身份非法: "+ref)
			}
		}
		sort.Strings(rows)
		return rows, nil
	}
	// 模板源仓根的 profiles/<name> 是各 Profile 的覆盖层，各有自己的 CONTEXT.md，不属于本仓根的业务语言。
	skipProfiles := false
	if identity, e := s.Doc("yss-project.yaml"); e == nil {
		skipProfiles = Text(identity["repository_mode"]) == "template-source"
	}
	skip := map[string]bool{".git": true, ".codegraph": true, ".template-source": true, "node_modules": true, "dist": true, "build": true}
	rows := []string{}
	err := filepath.WalkDir(s.Root, func(path string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if e = s.Guard(); e != nil {
			return e
		}
		ref, e := filepath.Rel(s.Root, path)
		if e != nil {
			return e
		}
		ref = filepath.ToSlash(ref)
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if d.IsDir() && ref != "." {
			if skip[d.Name()] || skipProfiles && ref == "profiles" {
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
			return s.Unavailable("CAPABILITY", "Context 扫描超过50000项")
		}
		if !d.IsDir() && (d.Name() == "CONTEXT-MAP.md" || d.Name() == "context.md" || d.Name() == "CONTEXT.md" && ref != "CONTEXT.md") {
			return s.Reject("CONTEXT", "Context 文件身份非法: "+ref)
		}
		return nil
	})
	sort.Strings(rows)
	return rows, err
}

func (s *SemanticSession) ContextContract() (map[string]any, error) {
	rows, err := s.ContextFiles()
	if err != nil {
		return nil, err
	}
	if s.ContextInventory != nil && !EqualStrings(rows, s.ContextInventory) {
		return nil, s.Unavailable("INPUT_DRIFT", "Context 扫描集合变化")
	}
	s.ContextInventory = rows
	b, err := s.Bytes("CONTEXT.md")
	if err != nil {
		return nil, err
	}
	result, err := ContextContractBytes(b)
	if err != nil {
		return nil, s.Reject("CONTEXT", err.Error())
	}
	return result, nil
}

func (s *SemanticSession) ContextSnapshot(snapshot map[string]any) (map[string]any, error) {
	contract, err := s.ContextContract()
	if err != nil {
		return nil, err
	}
	n, ok := Integer(snapshot["context_schema_version"])
	if !ok || n != 1 || Text(snapshot["context_ref"]) != "CONTEXT.md" {
		return nil, s.Reject("CONTEXT", "Context snapshot 版本或文件身份非法")
	}
	terms := contract["business_terms"].([]Term)
	byID := map[string]Term{}
	for _, t := range terms {
		byID[t.TermRef] = t
	}
	selected := []Term{}
	seen := map[string]bool{}
	for _, id := range SemStrings(snapshot["term_refs"]) {
		t, ok := byID[id]
		if !ok || seen[id] {
			return nil, s.Reject("CONTEXT_REFERENCE", "术语缺失或重复: "+id)
		}
		seen[id] = true
		selected = append(selected, t)
	}
	digest, err := TermsDigest(selected)
	if err != nil {
		return nil, err
	}
	if snapshot["document_digest"] != contract["document_digest"] || snapshot["referenced_terms_digest"] != digest {
		return nil, s.Reject("CONTEXT_DRIFT", "Context snapshot 摘要变化")
	}
	return contract, nil
}

// Shared file-level currentness assertion, without running any transaction.
func (s *SemanticSession) CurrentAsset(ref string) error {
	if _, err := safefs.Path(s.Root, s.LocalRef(ref)); err != nil {
		return s.Unavailable("PATH", err.Error())
	}
	exists, err := s.Exists(".yss/asset-transactions/active.json")
	if err != nil {
		return err
	}
	if exists {
		return s.Reject("ASSET_TRANSACTION_PENDING", "先恢复未完成的资产事务")
	}
	files, err := s.Scan(".yss/asset-transactions")
	if err != nil {
		return err
	}
	for _, file := range files {
		name := filepath.Base(file)
		if !strings.HasSuffix(name, ".json") || !IsSHA256(strings.TrimSuffix(name, ".json")) {
			continue
		}
		receipt, e := s.Doc(file)
		if e != nil {
			return e
		}
		if receipt["status"] == "applied" && Text(SemMap(SemMap(receipt["migration"])["source_to_target"])[ref]) != "" {
			return s.Reject("ASSET_HISTORICAL_ONLY", "资产已迁移，仅能检查历史结构: "+ref)
		}
	}
	_, err = s.Bytes(ref)
	return err
}
