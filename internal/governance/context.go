package governance

import (
	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"os"
	"path/filepath"
	"strings"
)

func scanContext(root string) error {
	skip := map[string]bool{".git": true, ".codegraph": true, ".template-source": true, "node_modules": true, "dist": true, "build": true}
	return filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		if d.IsDir() && rel != "." {
			if skip[d.Name()] {
				return filepath.SkipDir
			}
			if _, e := os.Lstat(filepath.Join(p, ".git")); e == nil {
				return filepath.SkipDir
			} else if !os.IsNotExist(e) {
				return e
			}
		}
		if !d.IsDir() {
			if d.Name() == "CONTEXT-MAP.md" {
				return domain.Fail("CONTEXT", "禁止 CONTEXT-MAP.md: "+rel)
			}
			if d.Name() == "context.md" {
				return domain.Fail("CONTEXT", "文件名大小写错误: "+rel)
			}
			if d.Name() == "CONTEXT.md" && rel != "CONTEXT.md" {
				return domain.Fail("CONTEXT", "禁止嵌套 CONTEXT.md: "+rel)
			}
		}
		return nil
	})
}

func contextContract(root string) (map[string]any, error) {
	if err := scanContext(root); err != nil {
		return nil, err
	}
	file, err := safefs.Path(root, "CONTEXT.md")
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	return kernel.ContextContractBytes(b)
}

func contextRun(action, root string, args map[string]string) (any, error) {
	if action != "check" && action != "verify" && action != "query" {
		return nil, domain.Fail("UNPORTED", "Context 动作尚未迁移: "+action)
	}
	result, err := contextContract(root)
	if err != nil {
		return nil, err
	}
	if ids := args["allowed-context-ids"]; ids != "" {
		allowed := map[string]bool{"Global": true}
		for _, id := range strings.Split(ids, ",") {
			allowed[strings.TrimSpace(id)] = true
		}
		for _, term := range result["business_terms"].([]kernel.Term) {
			if !allowed[term.ContextID] {
				return nil, domain.Fail("CONTEXT", "业务责任区未在调用者的边界合同中登记: "+term.ContextID)
			}
		}
	}
	ref := args["id"]
	if ref == "" {
		ref = args["arg0"]
	}
	if action == "query" && ref != "" {
		if args["term-refs"] != "" {
			return nil, domain.Fail("INPUT", "query 的单个 ID 与 --term-refs 不能同时使用")
		}
		for _, term := range result["business_terms"].([]kernel.Term) {
			if term.TermRef == ref {
				return map[string]any{"term": term, "document_digest": result["document_digest"], "read_only": true, "execution_authorization": "not-evaluated"}, nil
			}
		}
		return nil, domain.Fail("CONTEXT_REFERENCE", "术语引用无法解析: "+ref)
	}
	if refs := args["term-refs"]; refs != "" {
		selected := []kernel.Term{}
		byID := map[string]kernel.Term{}
		for _, term := range result["business_terms"].([]kernel.Term) {
			byID[term.TermRef] = term
		}
		for _, id := range strings.Split(refs, ",") {
			id = strings.TrimSpace(id)
			term, ok := byID[id]
			if !ok {
				return nil, domain.Fail("CONTEXT_REFERENCE", "术语引用无法解析: "+id)
			}
			selected = append(selected, term)
		}
		digest, e := kernel.TermsDigest(selected)
		if e != nil {
			return nil, e
		}
		result["terms"] = selected
		result["referenced_terms_digest"] = digest
	}
	snapshotRef := kernel.First(args["snapshot"], args["file"])
	if action != "query" {
		snapshotRef = kernel.First(snapshotRef, args["arg0"])
	}
	if snapshotRef != "" {
		snapshot, err := load(root, snapshotRef)
		if err != nil {
			return nil, err
		}
		version, ok := kernel.Integer(snapshot["context_schema_version"])
		if !ok || version != 1 || kernel.Text(snapshot["context_ref"]) != "CONTEXT.md" {
			return nil, domain.Fail("CONTEXT_SNAPSHOT", "context_snapshot 身份或版本非法")
		}
		refs, ok := snapshot["term_refs"].([]any)
		if !ok {
			return nil, domain.Fail("CONTEXT_SNAPSHOT", "context_snapshot.term_refs 必须是数组")
		}
		byID := map[string]kernel.Term{}
		for _, term := range result["business_terms"].([]kernel.Term) {
			byID[term.TermRef] = term
		}
		selected := []kernel.Term{}
		for _, ref := range refs {
			id, ok := ref.(string)
			if !ok {
				return nil, domain.Fail("CONTEXT_REFERENCE", "术语引用必须是字符串")
			}
			term, ok := byID[id]
			if !ok {
				return nil, domain.Fail("CONTEXT_REFERENCE", "术语引用无法解析: "+id)
			}
			selected = append(selected, term)
		}
		digest, err := kernel.TermsDigest(selected)
		if err != nil {
			return nil, err
		}
		if kernel.Text(snapshot["document_digest"]) != result["document_digest"] || kernel.Text(snapshot["referenced_terms_digest"]) != digest {
			return nil, domain.Fail("CONTEXT_SNAPSHOT_STALE", "Context snapshot 与当前词汇文档或引用术语摘要不一致")
		}
		result["terms"] = selected
		result["referenced_terms_digest"] = digest
		result["snapshot_validation"] = "passed"
	}
	selected, _ := result["terms"].([]kernel.Term)
	refs := make([]string, 0, len(selected))
	for _, term := range selected {
		refs = append(refs, term.TermRef)
	}
	digest, err := kernel.TermsDigest(selected)
	if err != nil {
		return nil, err
	}
	result["referenced_terms_digest"] = digest
	result["context_snapshot"] = map[string]any{
		"context_ref": result["context_ref"], "context_schema_version": result["context_schema_version"],
		"document_digest": result["document_digest"], "referenced_terms_digest": digest, "term_refs": refs,
	}
	return result, nil
}
