package project

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/iloveZzz/yss-cli/internal/bundle"
	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/governance"
	"github.com/iloveZzz/yss-cli/internal/safefs"
)

func verifyAppliedPlan(ctx context.Context, p *Plan, b *bundle.Bundle) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	for _, c := range p.Changes {
		actual, e := workDescribe(p.Root, c.Path)
		if e != nil {
			return e
		}
		if actual != c.After {
			return domain.Fail("VERIFY", "应用后描述不匹配: "+c.Path)
		}
	}
	id, e := Detect(p.Root, p.Profile, false)
	if e != nil {
		return e
	}
	if id.Native == nil || id.Native.SchemaVersion != 3 || id.Native.TemplateCommit != p.TemplateCommit || id.Native.Profile != p.Profile {
		return domain.Fail("VERIFY", "应用后身份或来源不匹配")
	}
	if _, e = governance.RunContext(ctx, "context", "verify", p.Root, nil); e != nil {
		return domain.Wrap("VERIFY", e)
	}
	for _, a := range p.Assets {
		want := a.Target
		if a.Action == "preserve" || a.Action == "retired-preserved" {
			want = a.Before
		}
		if a.Action == "delete" || a.Action == "rename" {
			want = domain.Descriptor{Type: "missing"}
		}
		actual, e := workDescribe(p.Root, a.Path)
		if e != nil {
			return e
		}
		if actual != want {
			return domain.Fail("VERIFY", "资产后置条件不匹配: "+a.Path)
		}
	}
	if e = verifySkillTrees(p.Root, id.Native); e != nil {
		return e
	}
	if p.Binding != nil {
		raw, e := os.ReadFile(filepath.Join(p.Root, p.Binding.Path))
		if e != nil {
			return e
		}
		if !bytes.Equal(raw, mustDecode(p.Binding.Data)) {
			return domain.Fail("VERIFY", "插件 binding 字节不匹配")
		}
		var payload map[string]any
		if e = json.Unmarshal(raw, &payload); e != nil {
			return e
		}
		if payload["profile"] != p.Profile || payload["template_commit"] != p.TemplateCommit || payload["bundle_hash"] != b.BundleHash {
			return domain.Fail("VERIFY", "插件 binding 来源不匹配")
		}
	}
	return ctx.Err()
}

func verifySkillTrees(root string, m *Metadata) error {
	if _, ok := m.Managed["skills-lock.json"]; !ok {
		return nil
	}
	raw, e := os.ReadFile(filepath.Join(root, "skills-lock.json"))
	if e != nil {
		return e
	}
	var lock struct {
		Skills struct {
			Shared   map[string]json.RawMessage            `json:"shared"`
			Platform map[string]map[string]json.RawMessage `json:"platform"`
		} `json:"skills"`
	}
	if e = json.Unmarshal(raw, &lock); e != nil {
		return domain.Wrap("VERIFY", e)
	}
	files := map[string]bundle.File{}
	roots := map[string]bool{}
	for name := range lock.Skills.Shared {
		roots[".agents/skills/"+name] = true
	}
	for runtime, entries := range lock.Skills.Platform {
		for name := range entries {
			roots[runtime+"/"+name] = true
		}
	}
	for ref := range m.Managed {
		if strings.HasPrefix(ref, ".codex/skills/") || strings.HasPrefix(ref, ".cursor/skills/") || strings.HasPrefix(ref, ".pi/skills/") {
			parts := strings.Split(ref, "/")
			if len(parts) > 3 && !strings.HasPrefix(parts[2], ".") {
				roots[strings.Join(parts[:3], "/")] = true
			}
		}
	}
	for prefix := range roots {
		if e := safefs.ValidateRef(prefix); e != nil {
			return e
		}
		path, e := safefs.Path(root, prefix)
		if e != nil {
			return e
		}
		e = filepath.WalkDir(path, func(path string, entry os.DirEntry, e error) error {
			if e != nil {
				return e
			}
			if entry.IsDir() {
				return nil
			}
			ref, e := filepath.Rel(root, path)
			if e != nil {
				return e
			}
			ref = filepath.ToSlash(ref)
			if e := safefs.ValidateRef(ref); e != nil {
				return e
			}
			d, e := safefs.Describe(root, ref)
			if e != nil {
				return e
			}
			if d.Type != "file" {
				return domain.Fail("VERIFY", "Skill 树包含非普通文件")
			}
			raw, e := os.ReadFile(path)
			if e != nil {
				return e
			}
			files[ref] = bundle.File{Data: base64.StdEncoding.EncodeToString(raw), Digest: safefs.Digest(raw), Mode: d.Mode}
			return nil
		})
		if e != nil {
			return domain.Wrap("VERIFY", e)
		}
	}
	rendered, e := bundle.RenderedSkillLock(raw, files, m.Variables)
	if e != nil {
		return domain.Wrap("VERIFY", e)
	}
	if !bytes.Equal(rendered, raw) {
		return domain.Fail("VERIFY", "Skill 来源锁与实际树或投影不匹配")
	}
	if text(m.Distribution["mode"]) == "selected" {
		names := []string{}
		for name := range lock.Skills.Shared {
			names = append(names, name)
		}
		for _, entries := range lock.Skills.Platform {
			for name := range entries {
				names = append(names, name)
			}
		}
		if strings.Join(union(names), ",") != strings.Join(union(stringsOf(m.Distribution["installedSkills"])), ",") {
			return domain.Fail("VERIFY", "资源闭包与锁中 Skill 不匹配")
		}
	}
	return nil
}
