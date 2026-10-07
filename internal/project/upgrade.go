package project

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/iloveZzz/yss-cli/internal/bundle"
	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/identitymeta"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/transaction"
)

type Blocker struct {
	Path   string `json:"path"`
	Code   string `json:"code"`
	Reason string `json:"reason"`
}
type Coverage struct {
	InScope    int `json:"inScope"`
	Classified int `json:"classified"`
	Percent    int `json:"percent"`
}
type Candidate struct {
	Data   string `json:"data"`
	Digest string `json:"digest"`
	Clean  bool   `json:"clean"`
}
type AssetResult struct {
	Path              string             `json:"path"`
	Policy            string             `json:"policy"`
	Action            string             `json:"action"`
	Before            domain.Descriptor  `json:"before"`
	Target            domain.Descriptor  `json:"target"`
	Reason            string             `json:"reason,omitempty"`
	Baseline          domain.Descriptor  `json:"baseline"`
	BaselineAvailable bool               `json:"baselineAvailable"`
	BaselineSource    string             `json:"baselineSource"`
	BaselineData      string             `json:"baselineData,omitempty"`
	TargetData        string             `json:"targetData,omitempty"`
	RuleID            string             `json:"ruleId,omitempty"`
	TargetPath        string             `json:"targetPath,omitempty"`
	DestinationBefore *domain.Descriptor `json:"destinationBefore,omitempty"`
	Options           []string           `json:"resolutionOptions,omitempty"`
	Candidate         *Candidate         `json:"candidate,omitempty"`
	Decision          string             `json:"decision,omitempty"`
}
type Material struct {
	Path       string            `json:"path"`
	Kind       string            `json:"kind"`
	Descriptor domain.Descriptor `json:"descriptor"`
}
type PlanningOptions struct {
	BaseBundlePath string
	Original       *Plan
	ResolutionFile string
	// Apply uses exactly the canonical decisions stored by the planner.
	ResolvedFrom string
	Resolutions  []Resolution
	target       *bundle.Bundle // isolated tests, never an external CLI parameter
}

func finalizePlan(p *Plan) {
	sort.Slice(p.Assets, func(i, j int) bool { return p.Assets[i].Path < p.Assets[j].Path })
	sort.Slice(p.Materials, func(i, j int) bool {
		if p.Materials[i].Path == p.Materials[j].Path {
			return p.Materials[i].Kind < p.Materials[j].Kind
		}
		return p.Materials[i].Path < p.Materials[j].Path
	})
	sort.Slice(p.Changes, func(i, j int) bool { return p.Changes[i].Path < p.Changes[j].Path })
	p.Conflicts = union(p.Conflicts)
	p.Preserved = union(p.Preserved)
	p.Blockers = []Blocker{}
	classified := map[string]bool{}
	for _, a := range p.Assets {
		if classified[a.Path] || a.Action == "" {
			p.Blockers = append(p.Blockers, Blocker{a.Path, "COVERAGE", "资产分类重复或缺失"})
		}
		classified[a.Path] = true
		if a.Action == "conflict" {
			p.Blockers = append(p.Blockers, Blocker{a.Path, "CONFLICT", a.Reason})
		}
	}
	p.AssetScope = union(p.AssetScope)
	count := 0
	for _, ref := range p.AssetScope {
		if classified[ref] {
			count++
		} else {
			p.Blockers = append(p.Blockers, Blocker{ref, "COVERAGE", "范围内资产缺少处理结论"})
		}
	}
	percent := 100
	if len(p.AssetScope) > 0 {
		percent = count * 100 / len(p.AssetScope)
	}
	p.Coverage = &Coverage{InScope: len(p.AssetScope), Classified: count, Percent: percent}
	p.ReadyToApply = len(p.Blockers) == 0 && len(p.Conflicts) == 0
	p.Digest = planDigest(p)
}
func addSystemAsset(p *Plan, ref string, before, after domain.Descriptor, kind string) {
	p.AssetScope = union(p.AssetScope, []string{ref})
	action := "unchanged"
	if before != after {
		action = "update"
		if before.Type == "missing" {
			action = "add"
		}
	}
	for i, a := range p.Assets {
		if a.Path == ref {
			p.Assets[i].Target = after
			p.Assets[i].Action = action
			return
		}
	}
	p.Assets = append(p.Assets, AssetResult{Path: ref, Policy: kind, Action: action, Before: before, Target: after, BaselineSource: "generator"})
}

// The complete target is determined by the current base plus the dependency
// closure of recorded selections. It never discovers stages by scanning files.
func completeTargets(id *Identity, b *bundle.Bundle, d map[string]any, refs map[string]bundle.File, old map[string]Managed) error {
	mode := text(d["mode"])
	if id.Profile.Name != "spec" || mode != "selected" {
		if mode == "legacy-all" || mode == "profile-full" {
			for ref, f := range b.Files {
				refs[ref] = f
			}
		}
		return nil
	}
	skills := union(stringsOf(d["installedSkills"]), stringsOf(b.Distribution["installedSkills"]))
	stages := union(stringsOf(d["installedStages"]), stringsOf(b.Distribution["installedStages"]))
	for ref := range old {
		if strings.HasPrefix(ref, ".agents/skills/") {
			parts := strings.Split(ref, "/")
			if len(parts) > 3 && !strings.HasPrefix(parts[2], ".") {
				skills = union(skills, []string{parts[2]})
			}
		}
	}
	for _, stage := range stages {
		r, ok := b.StageRequirements[stage]
		if !ok {
			return domain.Fail("ASSET", "目标 Bundle 缺少已安装阶段: "+stage)
		}
		for _, ref := range r.Paths {
			f, ok := b.Files[ref]
			if !ok {
				return domain.Fail("BUNDLE", "阶段闭包缺少资产: "+ref)
			}
			refs[ref] = f
		}
		skills = union(skills, r.Skills)
	}
	visited := map[string]bool{}
	for {
		changed := false
		for _, name := range skills {
			if visited[name] {
				continue
			}
			visited[name] = true
			changed = true
			if r, ok := b.SkillRequirements[name]; ok {
				if r.UnsupportedReason != "" {
					// A historical/full distribution can contain informational
					// Skills whose executable resource port is unavailable. Keep
					// their fixed trees; explicit ensure still rejects UNPORTED.
					continue
				}
				for _, ref := range r.Paths {
					f, ok := b.Files[ref]
					if !ok {
						return domain.Fail("BUNDLE", "Skill 闭包缺少资产: "+ref)
					}
					refs[ref] = f
				}
				skills = union(skills, r.Skills)
			}
		}
		if !changed {
			break
		}
	}
	runtimes := stringsOf(d["runtimes"])
	roots := map[string]string{"codex": ".codex/skills/", "cursor": ".cursor/skills/", "pi": ".pi/skills/"}
	for _, runtime := range runtimes {
		if roots[runtime] == "" {
			return domain.Fail("BUNDLE", "未知运行时: "+runtime)
		}
	}
	for _, name := range skills {
		found := false
		for ref, f := range b.Files {
			if strings.HasPrefix(ref, ".agents/skills/"+name+"/") {
				refs[ref] = f
				found = true
			}
			for _, runtime := range runtimes {
				if strings.HasPrefix(ref, roots[runtime]+name+"/") {
					refs[ref] = f
				}
			}
		}
		// A platform-only skill has no canonical directory; its exact source is
		// still checked by the selected lock producer.
		if !found {
			platform := false
			for _, runtime := range runtimes {
				for ref := range b.Files {
					platform = platform || strings.HasPrefix(ref, roots[runtime]+name+"/")
				}
			}
			if !platform {
				return domain.Fail("ASSET", "目标 Bundle 缺少已安装 Skill: "+name)
			}
		}
	}
	for ref := range refs {
		for runtime, root := range roots {
			if strings.HasPrefix(ref, root) {
				active := false
				for _, s := range runtimes {
					active = active || s == runtime
				}
				if !active {
					delete(refs, ref)
				}
			}
		}
	}
	for ref, f := range b.Initial {
		if _, ok := refs[ref]; ok {
			refs[ref] = f
		}
	}
	d["installedSkills"] = skills
	d["installedStages"] = stages
	f, e := selectedLock(b, d)
	if e != nil {
		return e
	}
	refs["skills-lock.json"] = f
	return nil
}

type upgradePlanner struct {
	id           *Identity
	bundle       *bundle.Bundle
	plan         *Plan
	old          map[string]Managed
	options      PlanningOptions
	trustedModes map[string]bool
	archive      *transaction.MaterialReader
	base         *bundle.Bundle
	decisions    map[string]Resolution
}

func newUpgradePlanner(id *Identity, b *bundle.Bundle, p *Plan, old map[string]Managed, o PlanningOptions, modes map[string]bool) (*upgradePlanner, error) {
	reader, e := transaction.NewMaterialReader(id.Root)
	if e != nil {
		return nil, e
	}
	u := &upgradePlanner{id: id, bundle: b, plan: p, old: old, options: o, trustedModes: modes, archive: reader, decisions: map[string]Resolution{}}
	p.materialData = map[string][]byte{}
	if o.BaseBundlePath != "" {
		var abs, hash string
		u.base, abs, hash, e = bundle.ReadSnapshot(o.BaseBundlePath, id.Profile.Name)
		if e != nil {
			return nil, e
		}
		p.BaseBundlePath = abs
		p.BaseBundleDigest = hash
		commit, snapshot, hash := u.previousSource()
		if u.base.TemplateCommit != commit || u.base.SnapshotHash != snapshot || hash != "" && u.base.BundleHash != hash {
			return nil, domain.Fail("BASE_BUNDLE", "旧 Bundle 与登记来源不匹配")
		}
	}
	for _, r := range o.Resolutions {
		u.decisions[r.Path] = r
	}
	p.ResolvedFrom = o.ResolvedFrom
	p.Resolutions = o.Resolutions
	return u, nil
}
func (u *upgradePlanner) previousSource() (string, string, string) {
	if u.id.Native != nil {
		return u.id.Native.TemplateCommit, u.id.Native.SnapshotHash, u.id.Native.BundleHash
	}
	if u.id.Legacy != nil {
		return text(u.id.Legacy["templateCommit"]), text(u.id.Legacy["snapshotHash"]), text(u.id.Legacy["bundleHash"])
	}
	return "", "", ""
}
func (u *upgradePlanner) baseMaterial(ref string, m Managed) ([]byte, string, bool, error) {
	if m.Baseline.Type != "file" {
		return nil, "unavailable", false, nil
	}
	data, source, ok, e := u.archive.Read(ref, m.Baseline)
	if e != nil || ok {
		return data, source, ok, e
	}
	if u.base != nil {
		for _, files := range []map[string]bundle.File{u.base.Initial, u.base.Files} {
			if f, exists := files[ref]; exists {
				data, e := f.Render(u.plan.Variables)
				if e != nil {
					return nil, "", false, e
				}
				if safefs.Digest(data) == m.Baseline.Digest && domain.FileMode(f.Mode) == m.Baseline.Mode {
					return data, "base-bundle", true, nil
				}
			}
		}
	}
	return nil, "unavailable", false, nil
}
func (u *upgradePlanner) managed(ref string, f bundle.File, data []byte, applied domain.Descriptor, decision string, ruleID string) Managed {
	baseline := domain.Descriptor{Type: "file", Digest: safefs.Digest(data), Mode: domain.FileMode(f.Mode)}
	variant := "full"
	if initial, ok := u.bundle.Initial[ref]; ok && initial.Data == f.Data {
		variant = "initial"
	}
	if u.bundle.AssetKind(ref, f) == "generated" {
		variant = "generated"
	}
	bundleHash := u.bundle.BundleHash
	if bundleHash == "" {
		bundleHash = bundle.Hash(u.bundle)
	}
	m := Managed{Baseline: baseline, Applied: applied, Ownership: f.Ownership, Source: &identitymeta.BaselineSource{Kind: "bundle", TemplateCommit: u.bundle.TemplateCommit, SnapshotHash: u.bundle.SnapshotHash, BundleHash: bundleHash, Path: ref, Variant: variant, TemplateDigest: f.Digest}}
	if decision == "keep-local" || decision == "use-merged" {
		m.Disposition = &identitymeta.Disposition{Choice: decision, Target: baseline, Applied: applied, RuleID: ruleID}
	}
	raw, _ := base64.StdEncoding.DecodeString(f.Data)
	u.plan.materialData[f.Digest] = raw
	u.plan.materialData[baseline.Digest] = data
	u.plan.Materials = append(u.plan.Materials, Material{ref, "template", domain.Descriptor{Type: "file", Digest: f.Digest, Mode: domain.FileMode(f.Mode)}}, Material{ref, "baseline", baseline})
	for _, transform := range u.bundle.NativeTransforms {
		if transform.Path == ref {
			u.plan.materialData[transform.Source.Digest] = mustDecode(transform.Source.Data)
			u.plan.Materials = append(u.plan.Materials, Material{ref, "source", domain.Descriptor{Type: "file", Digest: transform.Source.Digest, Mode: domain.FileMode(transform.Source.Mode)}})
		}
	}
	return m
}
func (u *upgradePlanner) conflict(a *AssetResult, reason string) {
	a.Action = "conflict"
	a.Reason = reason
	a.Options = []string{"use-template"}
	if a.Policy == "customizable" {
		a.Options = []string{"keep-local", "use-template", "use-merged"}
	}
	if a.Policy == "retired" {
		a.Options = []string{"keep-local", "use-template"}
	}
}
func (u *upgradePlanner) planAssets(refs map[string]bundle.File, selectedLock bool) (map[string]Managed, error) {
	scope := []string{}
	for ref := range refs {
		scope = append(scope, ref)
	}
	for ref := range u.old {
		scope = append(scope, ref)
	}
	u.plan.AssetScope = union(scope)
	managed := map[string]Managed{}
	for ref, m := range u.old {
		if m.Source == nil {
			m.Source = &identitymeta.BaselineSource{Kind: "unavailable"}
		}
		managed[ref] = m
	}
	// Rules own both ends of a rename; a target must not also be independently
	// added, otherwise a conflicting source could leave a duplicate installed.
	retire := map[string]bundle.MigrationRule{}
	targets := map[string]bool{}
	if u.bundle.Upgrade != nil {
		if e := u.bundle.Upgrade.Validate(u.bundle); e != nil {
			return nil, e
		}
		commit, snapshot, hash := u.previousSource()
		for _, rule := range u.bundle.Upgrade.Rules {
			if rule.From.TemplateCommit == commit && rule.From.SnapshotHash == snapshot && (rule.From.BundleHash == "" || rule.From.BundleHash == hash) {
				if _, ok := u.old[rule.SourcePath]; ok {
					retire[rule.SourcePath] = rule
					if rule.Action == "rename" {
						targets[rule.TargetPath] = true
					}
				}
			}
		}
	}
	keys := []string{}
	for ref := range refs {
		if !targets[ref] {
			keys = append(keys, ref)
		}
	}
	sort.Strings(keys)
	for _, ref := range keys {
		f := refs[ref]
		before, e := safefs.Describe(u.id.Root, ref)
		if e != nil {
			return nil, e
		}
		u.plan.Inputs[ref] = before
		data, e := f.Render(u.plan.Variables)
		if e != nil {
			return nil, e
		}
		initial := u.plan.Command == "init" || u.plan.Command == "attach"
		if ref == ".template-spec/agents/issue-tracker.md" && initial {
			data, e = renderTracker(data, u.plan.Variables["issueTracker"])
			if e != nil {
				return nil, e
			}
		}
		if ref == "README.md" && initial && u.plan.Variables["issueTracker"] != "" {
			data = bytes.Replace(data, []byte("默认 Issue Tracker：local-markdown"), []byte("默认 Issue Tracker："+u.plan.Variables["issueTracker"]), 1)
		}
		desired := domain.Descriptor{Type: "file", Digest: safefs.Digest(data), Mode: domain.FileMode(f.Mode)}
		previous, wasManaged := u.old[ref]
		kind := u.bundle.AssetKind(ref, f)
		a := AssetResult{Path: ref, Policy: kind, Before: before, Target: desired, Baseline: previous.Baseline, BaselineSource: "unavailable"}
		modeConflict := before.Mode != previous.Applied.Mode
		if u.id.Native == nil && u.id.Profile.Name == "spec" {
			modeConflict = before.Mode != desired.Mode && !u.trustedModes[ref]
		}
		appliedMatches := before == previous.Applied
		baselineMatches := before == previous.Baseline
		if u.id.Legacy != nil && u.id.Profile.Name == "spec" {
			// Legacy contentHash records have no permission claim. The current
			// template mode, or the exact registered overlay, supplies that guard.
			appliedMatches = before.Type == "file" && before.Digest == previous.Applied.Digest && !modeConflict
			baselineMatches = before.Type == "file" && before.Digest == previous.Baseline.Digest && !modeConflict
		}
		// A fixed historical overlay is itself source-proven, even when the
		// old CLI retained its pre-overlay template baseline in metadata.
		baselineMatches = baselineMatches || u.id.Legacy != nil && u.trustedModes[ref]
		strictLock := !initial && selectedLock && ref == "skills-lock.json"
		if strictLock && (!managedDerivedSkillsLock(u.id, ref, f, previous, wasManaged, selectedLock) || before.Type != "file" || before.Digest != previous.Applied.Digest || modeConflict) {
			u.conflict(&a, "生成锁被修改或缺少可信登记，须由固定生成器重建")
		} else if wasManaged && (kind == "fixed" || kind == "generated") && !appliedMatches {
			u.conflict(&a, "固定来源资产已被修改，须显式恢复登记来源")
		} else if kind == "preserve" && !initial && before.Type == "file" {
			a.Action = "preserve"
			a.Reason = "项目配置或用户资产，保持原字节"
		} else if before == desired {
			a.Action = "unchanged"
			managed[ref] = u.managed(ref, f, data, before, "", "")
		} else if kind == "preserve" && before.Type != "missing" {
			a.Action = "preserve"
			a.Reason = "首次接管保留现有项目资产"
		} else if previous.Disposition != nil && previous.Disposition.Target == desired && previous.Disposition.Applied == before && kind == "customizable" {
			a.Action = "preserve"
			a.Decision = previous.Disposition.Choice
			a.Reason = "继续执行已登记的保留决定"
			managed[ref] = previous
			// Keep the canonical base recoverable even when this transaction only
			// upgrades other assets.
			managed[ref] = u.managed(ref, f, data, before, a.Decision, "")
		} else if kind == "preserve" && !initial && !(u.plan.Command == "migrate" && !wasManaged) {
			a.Action = "preserve"
			a.Reason = "缺失用户资产不由模板补写"
		} else if before.Type == "missing" && !wasManaged {
			a.Action = "add"
		} else if wasManaged && appliedMatches && (baselineMatches || strictLock) && !modeConflict {
			a.Action = "update"
		} else {
			u.conflict(&a, "本地修改或现有文件没有可信受管基线")
			if kind == "customizable" && wasManaged && before.Type == "file" {
				base, source, ok, e := u.baseMaterial(ref, previous)
				if e != nil {
					return nil, e
				}
				a.BaselineAvailable = ok
				a.BaselineSource = source
				if ok {
					a.BaselineData = base64.StdEncoding.EncodeToString(base)
					local, e := os.ReadFile(filepath.Join(u.id.Root, ref))
					if e != nil {
						return nil, e
					}
					if safefs.Digest(local) != before.Digest {
						return nil, domain.Fail("INPUT_DRIFT", "生成候选时输入变化: "+ref)
					}
					a.Candidate, e = mergeCandidate(local, base, data)
					if e != nil {
						return nil, e
					}
				}
			}
		}
		if a.Action == "conflict" {
			a.TargetData = base64.StdEncoding.EncodeToString(data)
		}
		if r, ok := u.decisions[ref]; ok {
			if e := validateResolution(a, r); e != nil {
				return nil, e
			}
			a.Decision = r.Choice
			switch r.Choice {
			case "keep-local":
				if before.Type != "file" {
					return nil, domain.Fail("RESOLUTION", "keep-local 需要普通文件")
				}
				a.Action = "preserve"
				a.Reason = "显式保留本地差异"
				managed[ref] = u.managed(ref, f, data, before, r.Choice, "")
			case "use-template":
				a.Action = "update"
				if before == desired {
					a.Action = "unchanged"
				}
			case "use-merged":
				data, e = resolutionBytes(r)
				if e != nil {
					return nil, e
				}
				a.Action = "update"
				a.Target = domain.Descriptor{Type: "file", Digest: safefs.Digest(data), Mode: desired.Mode}
				managed[ref] = u.managed(ref, f, mustDecode(a.TargetData), a.Target, r.Choice, "")
			}
		}
		switch a.Action {
		case "add", "update":
			u.plan.Changes = append(u.plan.Changes, Change{ref, before, a.Target, base64.StdEncoding.EncodeToString(data), f.Ownership})
			if a.Decision != "use-merged" {
				managed[ref] = u.managed(ref, f, data, a.Target, "", "")
			}
		case "conflict":
			u.plan.Conflicts = append(u.plan.Conflicts, ref)
		case "preserve":
			u.plan.Preserved = append(u.plan.Preserved, ref)
		}
		u.plan.Assets = append(u.plan.Assets, a)
	}
	if e := u.classifyForeignSkillFiles(refs); e != nil {
		return nil, e
	}
	for _, ref := range sortedManaged(u.old) {
		if _, ok := refs[ref]; ok && !targets[ref] {
			continue
		}
		if targets[ref] {
			continue
		}
		before, e := safefs.Describe(u.id.Root, ref)
		if e != nil {
			return nil, e
		}
		u.plan.Inputs[ref] = before
		previous := u.old[ref]
		a := AssetResult{Path: ref, Policy: "retired", Action: "retired-preserved", Before: before, Target: before, Baseline: previous.Baseline, BaselineSource: "unavailable", Reason: "目标 Bundle 不包含此资产；没有匹配迁移规则，保留"}
		if previous.Disposition != nil && previous.Disposition.Choice == "keep-local" && previous.Disposition.Applied == before {
			a.Decision = "keep-local"
			a.RuleID = previous.Disposition.RuleID
			a.Reason = "继续执行已登记的退役保留决定"
		}
		if _, hasRule := retire[ref]; !hasRule && before.Type != "missing" && withinSelectedSkillTree(ref, refs) && !bundle.IgnoredSkillFile(ref) {
			a.Action = "conflict"
			a.Policy = "fixed-retired"
			a.Reason = "退役文件没有删除规则，保持原字节；其内容与目标 Skill 来源锁不兼容，请先移出受管树"
			u.plan.Conflicts = append(u.plan.Conflicts, ref)
		}
		if rule, ok := retire[ref]; ok {
			if previous.Ownership == "user-owned" || previous.Ownership == "protected" {
				return nil, domain.Fail("BUNDLE_RULE", "迁移规则不能处置用户资产")
			}
			a.RuleID = rule.ID
			if previous.Ownership != "managed-customizable" {
				a.Policy = "fixed-retired"
			}
			a.Target = domain.Descriptor{Type: "missing"}
			a.TargetPath = rule.TargetPath
			base, source, trusted, e := u.baseMaterial(ref, previous)
			if e != nil {
				return nil, e
			}
			a.BaselineAvailable = trusted
			a.BaselineSource = source
			a.BaselineData = base64.StdEncoding.EncodeToString(base)
			pristine := trusted && before == previous.Baseline && before == previous.Applied
			var targetFile bundle.File
			var targetData []byte
			if rule.Action == "rename" {
				var selected bool
				targetFile, selected = refs[rule.TargetPath]
				if !selected {
					return nil, domain.Fail("BUNDLE_RULE", "改名目标不在已选择的分发闭包: "+rule.TargetPath)
				}
				targetData, e = targetFile.Render(u.plan.Variables)
				if e != nil {
					return nil, e
				}
				d, e := safefs.Describe(u.id.Root, rule.TargetPath)
				if e != nil {
					return nil, e
				}
				u.plan.Inputs[rule.TargetPath] = d
				a.DestinationBefore = &d
				a.Target = domain.Descriptor{Type: "file", Digest: safefs.Digest(targetData), Mode: domain.FileMode(targetFile.Mode)}
				a.TargetData = base64.StdEncoding.EncodeToString(targetData)
				pristine = pristine && d.Type == "missing"
			}
			if pristine {
				a.Action = rule.Action
				a.Reason = "固定来源规则及旧字节已核验"
			} else {
				u.conflict(&a, "删除/改名需要可信旧基线且源未定制、目标未占用")
				if rule.Action == "rename" || before.Type != "file" {
					// Keeping only the source would omit the required destination
					// and cause a new addition on the next synchronization.
					a.Options = []string{"use-template"}
				}
			}
			if r, ok := u.decisions[ref]; ok {
				if e := validateResolution(a, r); e != nil {
					return nil, e
				}
				a.Decision = r.Choice
				if r.Choice == "keep-local" {
					if before.Type != "file" {
						return nil, domain.Fail("RESOLUTION", "keep-local 需要普通文件")
					}
					a.Action = "retired-preserved"
					a.Reason = "显式保留退役资产"
					m := managed[ref]
					m.Applied = before
					m.Disposition = &identitymeta.Disposition{Choice: "keep-local", Target: m.Baseline, Applied: before, RuleID: rule.ID}
					managed[ref] = m
				} else {
					a.Action = rule.Action
				}
			}
			if a.Action == "delete" || a.Action == "rename" {
				if before.Type != "missing" {
					u.plan.Changes = append(u.plan.Changes, Change{ref, before, domain.Descriptor{Type: "missing"}, "", previous.Ownership})
				}
				delete(managed, ref)
				if a.Action == "rename" {
					d := *a.DestinationBefore
					if d != a.Target {
						u.plan.Changes = append(u.plan.Changes, Change{rule.TargetPath, d, a.Target, a.TargetData, targetFile.Ownership})
					}
					managed[rule.TargetPath] = u.managed(rule.TargetPath, targetFile, targetData, a.Target, "", rule.ID)
					addSystemAsset(u.plan, rule.TargetPath, d, a.Target, u.bundle.AssetKind(rule.TargetPath, targetFile))
				}
			} else if a.Action == "conflict" {
				u.plan.Conflicts = append(u.plan.Conflicts, ref)
			}
			if a.Action != "rename" && rule.Action == "rename" {
				d := *a.DestinationBefore
				u.plan.Assets = append(u.plan.Assets, AssetResult{Path: rule.TargetPath, Policy: u.bundle.AssetKind(rule.TargetPath, targetFile), Action: "preserve", Before: d, Target: d, RuleID: rule.ID, Reason: "改名源尚未处置，目标保持原状", BaselineSource: "unavailable"})
			}
		}
		if a.Action == "retired-preserved" {
			u.plan.Preserved = append(u.plan.Preserved, ref)
		}
		u.plan.Assets = append(u.plan.Assets, a)
	}
	return managed, nil
}

// Local additions in a fixed Skill tree participate in its source digest. They
// cannot be silently excluded from the plan or deleted without retirement
// authority. The user must relocate them before the native lock can be valid.
func (u *upgradePlanner) classifyForeignSkillFiles(refs map[string]bundle.File) error {
	roots := map[string]bool{}
	for ref := range refs {
		parts := strings.Split(ref, "/")
		if len(parts) > 3 && parts[1] == "skills" && (parts[0] == ".agents" || parts[0] == ".codex" || parts[0] == ".cursor" || parts[0] == ".pi") && !strings.HasPrefix(parts[2], ".") {
			roots[strings.Join(parts[:3], "/")] = true
		}
	}
	for prefix := range roots {
		path, e := safefs.Path(u.id.Root, prefix)
		if e != nil {
			return e
		}
		if _, e = os.Lstat(path); os.IsNotExist(e) {
			continue
		} else if e != nil {
			return e
		}
		e = filepath.WalkDir(path, func(path string, entry os.DirEntry, e error) error {
			if e != nil {
				return e
			}
			if entry.IsDir() {
				return nil
			}
			ref, e := filepath.Rel(u.id.Root, path)
			if e != nil {
				return e
			}
			ref = filepath.ToSlash(ref)
			if _, ok := refs[ref]; ok {
				return nil
			}
			if _, ok := u.old[ref]; ok {
				return nil // classified by the retirement ledger below
			}
			d, e := safefs.Describe(u.id.Root, ref)
			if e != nil {
				return e
			}
			u.plan.Inputs[ref] = d
			u.plan.AssetScope = union(u.plan.AssetScope, []string{ref})
			asset := AssetResult{Path: ref, Policy: "fixed", Action: "conflict", Before: d, Target: domain.Descriptor{Type: "missing"}, BaselineSource: "unavailable", Reason: "固定 Skill 树包含未登记文件；保留原文件，请移出受管树后重新规划"}
			if bundle.IgnoredSkillFile(ref) {
				asset.Policy = "preserve"
				asset.Action = "preserve"
				asset.Target = d
				asset.Reason = "来源锁排除的运行缓存，保持原文件"
				u.plan.Preserved = append(u.plan.Preserved, ref)
			} else {
				u.plan.Conflicts = append(u.plan.Conflicts, ref)
			}
			u.plan.Assets = append(u.plan.Assets, asset)
			return nil
		})
		if e != nil {
			return e
		}
	}
	return nil
}

func withinSelectedSkillTree(ref string, refs map[string]bundle.File) bool {
	parts := strings.Split(ref, "/")
	if len(parts) < 4 || parts[1] != "skills" || strings.HasPrefix(parts[2], ".") || (parts[0] != ".agents" && parts[0] != ".codex" && parts[0] != ".cursor" && parts[0] != ".pi") {
		return false
	}
	prefix := strings.Join(parts[:3], "/") + "/"
	for target := range refs {
		if strings.HasPrefix(target, prefix) {
			return true
		}
	}
	return false
}
func sortedManaged(m map[string]Managed) []string {
	out := []string{}
	for ref := range m {
		out = append(out, ref)
	}
	sort.Strings(out)
	return out
}
func mustDecode(data string) []byte { b, _ := base64.StdEncoding.DecodeString(data); return b }

func BuildWithOptions(root, profile, command string, vars map[string]string, selection []string, binding *Binding, o PlanningOptions) (*Plan, error) {
	baseOptions := o
	baseOptions.Resolutions = nil
	baseOptions.ResolvedFrom = ""
	baseOptions.Original = nil
	baseOptions.ResolutionFile = ""
	p, e := buildCore(root, profile, command, vars, selection, binding, baseOptions)
	if e != nil {
		return nil, e
	}
	if e = addBinding(p, binding); e != nil {
		return nil, e
	}
	if o.ResolutionFile == "" && len(o.Resolutions) == 0 {
		return p, nil
	}
	origin := o.ResolvedFrom
	resolutions := o.Resolutions
	if o.ResolutionFile != "" {
		if o.Original == nil {
			return nil, domain.Fail("PLAN_REQUIRED", "决议需要 --plan-file 原保存计划")
		}
		if o.Original.SchemaVersion != 2 {
			return nil, domain.Fail("PLAN_VERSION", "旧保存计划必须重新生成")
		}
		if o.Original.Digest != planDigest(o.Original) || o.Original.ResolvedFrom != "" {
			return nil, domain.Fail("RESOLUTION", "决议须绑定原始未决计划")
		}
		origin = o.Original.Digest
		if o.Original.Root != p.Root || o.Original.Profile != p.Profile || o.Original.Command != p.Command {
			return nil, domain.Fail("RESOLUTION", "原计划根目录、Profile 或命令不匹配")
		}
		var file ResolutionFile
		file, e = ReadResolutionFile(o.ResolutionFile)
		if e != nil {
			return nil, e
		}
		if file.PlanDigest != origin {
			return nil, domain.Fail("RESOLUTION_STALE", "决议没有绑定原计划摘要")
		}
		resolutions = file.Decisions
	}
	if origin != p.Digest {
		return nil, domain.Fail("RESOLUTION_STALE", "原计划输入或目标已改变，须重新规划决议")
	}
	assets := map[string]AssetResult{}
	for _, a := range p.Assets {
		assets[a.Path] = a
	}
	seen := map[string]bool{}
	for i, r := range resolutions {
		a, ok := assets[r.Path]
		if !ok || seen[r.Path] {
			return nil, domain.Fail("RESOLUTION", "决议路径重复或不在原计划中")
		}
		seen[r.Path] = true
		if e = validateResolution(a, r); e != nil {
			return nil, e
		}
		if r.Choice == "use-merged" {
			data, e := resolutionBytes(r)
			if e != nil {
				return nil, e
			}
			r.CandidateData = base64.StdEncoding.EncodeToString(data)
			resolutions[i] = r
		}
	}
	sort.Slice(resolutions, func(i, j int) bool { return resolutions[i].Path < resolutions[j].Path })
	o.Resolutions = resolutions
	o.ResolvedFrom = origin
	p, e = buildCore(root, profile, command, vars, selection, binding, o)
	if e != nil {
		return nil, e
	}
	if e = addBinding(p, binding); e != nil {
		return nil, e
	}
	return p, nil
}
