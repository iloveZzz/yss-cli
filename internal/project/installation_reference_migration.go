package project

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/iloveZzz/yss-cli/internal/bundle"
	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/safefs"
)

func verifyRetainedSourceBefore(ctx context.Context, p *Plan) error {
	if p.RetainedSourceDigest == "" {
		return nil
	}
	digest, _, err := observeInstallationWork(ctx, p.Root, []string{".template-source"})
	if err != nil {
		return err
	}
	if digest != p.RetainedSourceDigest {
		return domain.Fail("INPUT_DRIFT", ".template-source 引用自规划后变化")
	}
	return nil
}

// A governed migration may retain only unchanged source reference assets
// whose prior bundle identity and complete filesystem tree can be verified.
func retainedTemplateSourceReferences(id *Identity) ([]string, error) {
	const sourceRoot = ".template-source"
	root, err := safefs.Path(id.Root, sourceRoot)
	if err != nil {
		return nil, err
	}
	st, err := os.Lstat(root)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return nil, domain.Fail("MIGRATION_INPUT", ".template-source 必须是普通目录")
	}
	var refs, dirs []string
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		rel, e := filepath.Rel(id.Root, path)
		if e != nil {
			return e
		}
		ref := filepath.ToSlash(rel)
		if entry.Type()&os.ModeSymlink != 0 {
			return domain.Fail("MIGRATION_INPUT", ".template-source 含符号链接: "+ref)
		}
		if entry.IsDir() {
			dirs = append(dirs, ref)
			return nil
		}
		if !entry.Type().IsRegular() {
			return domain.Fail("MIGRATION_INPUT", ".template-source 含非常规文件: "+ref)
		}
		m, ok := id.Native.Managed[ref]
		if !ok || m.Ownership != "managed" || !currentWorkBuddyManaged(id, m) || m.Source.Path != ref || m.Disposition != nil || m.Baseline.Type != "file" || m.Applied != m.Baseline {
			return domain.Fail("MIGRATION_INPUT", ".template-source 引用缺少可信受管来源或已修改: "+ref)
		}
		actual, e := safefs.Describe(id.Root, ref)
		if e != nil {
			return e
		}
		if actual != m.Applied {
			return domain.Fail("MIGRATION_INPUT", ".template-source 引用已修改: "+ref)
		}
		refs = append(refs, ref)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(refs) == 0 {
		return nil, domain.Fail("MIGRATION_INPUT", ".template-source 没有可验证的引用文件")
	}
	for _, dir := range dirs {
		if dir == sourceRoot {
			continue
		}
		used := false
		for _, ref := range refs {
			if strings.HasPrefix(ref, dir+"/") {
				used = true
				break
			}
		}
		if !used {
			return nil, domain.Fail("MIGRATION_INPUT", ".template-source 含未登记目录: "+dir)
		}
	}
	sort.Strings(refs)
	return refs, nil
}

func validateRetainedTemplateSourceReferences(id *Identity, b *bundle.Bundle, refs []string) error {
	if len(refs) > 0 && (id.Native.TemplateCommit != b.TemplateCommit || id.Native.SnapshotHash != b.SnapshotHash || id.Native.BundleHash != b.BundleHash) {
		return domain.Fail("MIGRATION_INPUT", ".template-source 引用来源与目标 Bundle 不一致；请先安全同步再迁移")
	}
	if err := validateRetainedTarget(b, refs); err != nil {
		return err
	}
	for _, ref := range refs {
		f := b.Files[ref]
		m := id.Native.Managed[ref]
		if m.Source == nil || m.Source.TemplateDigest != f.Digest || m.Baseline.Digest != f.Digest || m.Baseline.Mode != domain.FileMode(f.Mode) {
			return domain.Fail("MIGRATION_INPUT", ".template-source 引用与固定 Bundle 字节或权限不一致: "+ref)
		}
	}
	return nil
}

func validateRetainedTarget(b *bundle.Bundle, refs []string) error {
	allowed := map[string]bool{}
	installation := b.Installations["skills"]
	for _, owner := range installation.ReferenceOwners() {
		paths, err := installation.ReferencesForSkill(owner)
		if err != nil {
			return domain.Fail("BUNDLE", "独立技能引用分组不合法: "+err.Error())
		}
		for _, ref := range paths {
			if strings.HasPrefix(ref, ".template-source/") {
				allowed[ref] = true
			}
		}
	}
	for _, ref := range refs {
		if !allowed[ref] {
			return domain.Fail("MIGRATION_INPUT", ".template-source 引用不在目标 Bundle 的技能清单: "+ref)
		}
		if _, ok := b.Files[ref]; !ok {
			return domain.Fail("MIGRATION_INPUT", ".template-source 引用缺少固定 Bundle 资产: "+ref)
		}
	}
	return nil
}
