package project

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"github.com/iloveZzz/yss-cli/internal/bundle"
	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/governance"
	"github.com/iloveZzz/yss-cli/internal/identitymeta"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/schema"
	"github.com/iloveZzz/yss-cli/internal/transaction"
	"os"
	"path/filepath"
	"strings"
)

type Change struct {
	Path      string            `json:"path"`
	Before    domain.Descriptor `json:"before"`
	After     domain.Descriptor `json:"after"`
	Data      string            `json:"data"`
	Ownership string            `json:"ownership"`
}
type Plan struct {
	InstallationMigration *InstallationMigration `json:"installationMigration,omitempty"`
	Installation          InstallationKind       `json:"installation,omitempty"`
	GovernanceMigration   *GovernanceMigration   `json:"governanceMigration,omitempty"`
	materialData          map[string][]byte
	AssetScope            []string                     `json:"assetScope,omitempty"`
	ReadyToApply          bool                         `json:"readyToApply,omitempty"`
	Assets                []AssetResult                `json:"assets,omitempty"`
	Blockers              []Blocker                    `json:"blockers,omitempty"`
	Coverage              *Coverage                    `json:"coverage,omitempty"`
	Materials             []Material                   `json:"baselineMaterials,omitempty"`
	BaseBundlePath        string                       `json:"baseBundlePath,omitempty"`
	BaseBundleDigest      string                       `json:"baseBundleDigest,omitempty"`
	ResolvedFrom          string                       `json:"resolvedFrom,omitempty"`
	Resolutions           []Resolution                 `json:"resolutions,omitempty"`
	Binding               *Binding                     `json:"binding,omitempty"`
	SchemaVersion         int                          `json:"schemaVersion"`
	ProtocolVersion       int                          `json:"protocolVersion"`
	Command               string                       `json:"command"`
	Root                  string                       `json:"root"`
	Profile               string                       `json:"profile"`
	TemplateCommit        string                       `json:"templateCommit"`
	SnapshotHash          string                       `json:"snapshotHash"`
	SourceVersion         string                       `json:"sourceVersion,omitempty"`
	Notices               []string                     `json:"notices,omitempty"`
	Changes               []Change                     `json:"changes"`
	Conflicts             []string                     `json:"conflicts"`
	Preserved             []string                     `json:"preserved"`
	Inputs                map[string]domain.Descriptor `json:"inputs"`
	Digest                string                       `json:"digest"`
	Variables             map[string]string            `json:"variables"`
	Selection             []string                     `json:"selection"`
}

func (p *Plan) Public() map[string]any {
	changes := []map[string]any{}
	for _, c := range p.Changes {
		changes = append(changes, map[string]any{"path": c.Path, "before": c.Before, "after": c.After, "ownership": c.Ownership})
	}
	public := map[string]any{"schemaVersion": p.SchemaVersion, "protocolVersion": p.ProtocolVersion, "command": p.Command, "root": p.Root, "profile": p.Profile, "templateCommit": p.TemplateCommit, "snapshotHash": p.SnapshotHash, "changes": changes, "conflicts": p.Conflicts, "preserved": p.Preserved, "digest": p.Digest, "readyToApply": p.ReadyToApply, "assets": p.Assets, "blockers": p.Blockers, "coverage": p.Coverage, "stats": map[string]int{"changed": len(p.Changes), "conflicts": len(p.Conflicts), "preserved": len(p.Preserved)}}
	// 只在目标 Bundle 含版本区间规则时才出现，其余计划的输出保持不变。
	if p.InstallationMigration != nil {
		public["installationMigration"] = p.InstallationMigration
	}
	if p.Installation != "" {
		public["installation"] = p.Installation
	}
	if p.SourceVersion != "" {
		public["sourceVersion"] = p.SourceVersion
	}
	if len(p.Notices) > 0 {
		public["notices"] = p.Notices
	}
	if p.GovernanceMigration != nil {
		public["governanceMigration"] = p.GovernanceMigration
	}
	return public
}

func jsonBytes(v any) ([]byte, error) {
	b, e := json.MarshalIndent(v, "", "  ")
	return append(b, '\n'), e
}
func baseline(id *Identity) map[string]Managed {
	if id.Native != nil {
		return id.Native.Managed
	}
	out := map[string]Managed{}
	return out
}

// Only the selected distribution's fixed lock is a managed derived asset.
// Other generated files remain user-preserved after initialization.
func managedDerivedSkillsLock(id *Identity, ref string, f bundle.File, previous Managed, wasManaged, selected bool) bool {
	if !selected || ref != "skills-lock.json" || f.Ownership != "generated" || !wasManaged || previous.Applied.Type != "file" || previous.Applied.Digest == "" {
		return false
	}
	return id.Native != nil && previous.Ownership == "generated"
}

func Variables(id *Identity, provided map[string]string) map[string]string {
	v := map[string]string{"projectName": filepath.Base(id.Root), "businessDomain": "待补充", "teamSize": "待补充"}
	if id.Native != nil {
		for k, x := range id.Native.Variables {
			v[k] = x
		}
	}
	for k, x := range provided {
		if x != "" {
			v[k] = x
		}
	}
	return v
}
func Build(root, profile, command string, vars map[string]string, selection []string) (*Plan, error) {
	return build(root, profile, command, vars, selection, nil)
}
func build(root, profile, command string, vars map[string]string, selection []string, binding *Binding) (*Plan, error) {
	return buildCore(root, profile, command, vars, selection, binding, PlanningOptions{})
}
func buildCore(root, profile, command string, vars map[string]string, selection []string, binding *Binding, options PlanningOptions) (*Plan, error) {
	init := command == "init"
	attach := command == "attach"
	var id *Identity
	var err error
	if command == "migrate" && options.Installation != "" {
		id, err = Detect(root, profile, false)
	} else {
		id, err = migrationIdentity(root, profile, command, init || attach)
	}
	if err != nil {
		return nil, err
	}

	var installationMigration *InstallationMigration
	if command == "migrate" && options.Installation != "" {
		installationMigration, err = planInstallationMigration(context.Background(), id, options.Installation)
		if err != nil {
			return nil, err
		}
	}
	if id.Profile.Name == "" {
		return nil, domain.Fail("IDENTITY", "初始化或接管需要 --profile")
	}
	if command != "diff" && command != "doctor" {
		if status, e := transaction.Status(id.Root); e != nil {
			return nil, e
		} else if len(status.Pending) > 0 || len(status.Preparations) > 0 {
			return nil, domain.Fail("INTERRUPTED", "存在未完成事务；执行 yss recover --root "+id.Root+" --apply")
		}
	}
	if attach && id.Native != nil {
		return nil, domain.Fail("SYNC_REQUIRED", "原生实例使用 yss sync --root "+id.Root+" --plan")
	}
	if init {
		if entries, e := os.ReadDir(id.Root); e == nil && len(entries) > 0 {
			if err := verifyRestoredInitialization(id); err != nil {
				return nil, err
			}
		} else if e != nil && !os.IsNotExist(e) {
			return nil, e
		}
	}
	if !init && !skillsIdentity(id) {
		if ok, e := exists(id.Root, "CONTEXT.md"); e != nil {
			return nil, e
		} else if ok {
			if _, e := governance.Run("context", "verify", id.Root, nil); e != nil {
				return nil, e
			}
		} else if id.Native != nil {
			return nil, domain.Fail("CONTEXT_MISSING", "原生实例缺少 CONTEXT.md；请恢复项目 Context 后重新规划")
		}
	}
	if !init && !attach && id.Native == nil && command != "diff" && command != "doctor" {
		return nil, domain.Fail("IDENTITY", "缺少统一 CLI metadata；本 CLI 仅支持 2.3.1 基线实例")
	}
	b := options.target
	if b == nil {
		b, err = bundle.Load(id.Profile.Name)
		if err != nil {
			return nil, err
		}
	}
	installation := InstallationGoverned
	if b.SchemaVersion >= 5 || options.Installation != "" {
		installation, err = ResolveInstallation(id, command, options.Installation)
		if err != nil {
			return nil, err
		}
		b, err = b.ForInstallation(string(installation))
		if err != nil {
			return nil, err
		}
	}
	if err := requireWorkBuddyRootSync(id, b, command); err != nil {
		return nil, err
	}
	if b.SchemaVersion >= 4 && id.Native != nil && id.Native.SchemaVersion == 3 && command != "migrate" && command != "diff" && command != "doctor" {
		return nil, domain.Fail("GOVERNANCE_MIGRATION_REQUIRED", "旧实例须先显式 migrate；sync 不隐式改变治理身份")
	}
	if command == "migrate" && b.SchemaVersion < 4 {
		return nil, domain.Fail("MIGRATION_UNSUPPORTED", "迁移目标必须为 Bundle4")
	}
	vars = Variables(id, vars)
	for key, value := range vars {
		for _, r := range value {
			if r < 32 || r == 127 {
				return nil, domain.Fail("ARGUMENT", "模板变量不允许控制字符: "+key)
			}
		}
	}
	distribution, err := distributionFor(id, b, selection)
	if installationMigration != nil {
		distribution = cloneInstallationDistribution(b.Distribution)
	}
	if err != nil {
		return nil, err
	}
	if installation == InstallationSkills {
		if err := validateSkillsSelection(b, selection, stringsOf(distribution["runtimes"])); err != nil {
			return nil, err
		}
	}
	p := &Plan{InstallationMigration: installationMigration, SchemaVersion: 2, ProtocolVersion: domain.ProtocolVersion, Command: command, Root: id.Root, Profile: id.Profile.Name, TemplateCommit: b.TemplateCommit, SnapshotHash: b.SnapshotHash, Changes: []Change{}, Conflicts: []string{}, Preserved: []string{}, Inputs: map[string]domain.Descriptor{}, Variables: vars, Selection: selection}
	if b.SchemaVersion >= 4 {
		p.SchemaVersion = 3
	}
	if b.SchemaVersion == 5 {
		p.SchemaVersion = 4
		p.Installation = installation
	}
	// Bind identity and vocabulary inputs even when preserved custom files do not
	// appear in managedFiles. Transaction guards recheck them under the write lock.
	for _, ref := range []string{"yss-project.yaml", ".template-spec/process/harness-profile.yaml", "CONTEXT.md", MetadataFile, id.Profile.Metadata} {
		d, e := safefs.Describe(id.Root, ref)
		if e != nil {
			return nil, e
		}
		p.Inputs[ref] = d
	}
	if id.ProfileRef != "" {
		d, e := safefs.Describe(id.Root, id.ProfileRef)
		if e != nil {
			return nil, e
		}
		p.Inputs[id.ProfileRef] = d
	}
	if err = guardPluginBindings(id, b, p, binding); err != nil {
		return nil, err
	}
	refs := map[string]bundle.File{}
	old := baseline(id)
	if init || attach || id.Native != nil {
		for ref, f := range b.Initial {
			refs[ref] = f
		}
	}
	if !init && !attach {
		for ref := range old {
			if f, ok := b.Initial[ref]; ok {
				refs[ref] = f
			} else if f, ok := b.Files[ref]; ok && installationMigration == nil {
				refs[ref] = f
			}
		}
	}

	if command == "migrate" {
		for ref, f := range b.Initial {
			refs[ref] = f
		}
	}
	for _, ref := range selection {
		f, ok := b.Files[ref]
		if !ok {
			return nil, domain.Fail("ASSET", "快照缺少资产: "+ref)
		}
		refs[ref] = f
	}
	// A complete asset selection still uses the selected-runtime variants of
	// entry files. Synchronization must render the same bytes as initialization.
	if id.Profile.Name == "spec" && text(distribution["mode"]) == "selected" || text(distribution["mode"]) == "skills" {
		for ref, initial := range b.Initial {
			if _, selected := refs[ref]; selected {
				refs[ref] = initial
			}
		}
	}
	if installation != InstallationSkills && (init || attach || id.Native != nil) {
		if _, ok := refs[".template-spec/process/harness-profile.yaml"]; !ok {
			data := "schema_version: 2\nprofile_id: " + id.Profile.ID + "\ninstantiation:\n  cli_package: " + id.Profile.LegacyCommand + "\n  metadata_file: " + id.Profile.Metadata + "\n  template_source: " + id.Profile.TemplateSource + "\n"
			refs[".template-spec/process/harness-profile.yaml"] = bundle.File{Data: base64.StdEncoding.EncodeToString([]byte(data)), Digest: safefs.Digest([]byte(data)), Mode: 0644, Ownership: "managed"}
		}
	}
	if f, ok := refs[".template-spec/process/harness-profile.yaml"]; ok {
		native, e := nativeProfileFile(f, id.Profile)
		if e != nil {
			return nil, e
		}
		refs[".template-spec/process/harness-profile.yaml"] = native
	}
	selectedLockProduced := false
	if id.Profile.Name == "spec" && text(distribution["mode"]) == "selected" || text(distribution["mode"]) == "skills" {
		f, e := selectedLock(b, distribution)
		if e != nil {
			return nil, e
		}
		refs["skills-lock.json"] = f
		selectedLockProduced = true
	}
	if err = completeTargets(id, b, distribution, refs, old, command); err != nil {
		return nil, err
	}
	if f, ok := refs[".template-spec/process/harness-profile.yaml"]; ok {
		native, e := nativeProfileFile(f, id.Profile)
		if e != nil {
			return nil, e
		}
		refs[".template-spec/process/harness-profile.yaml"] = native
	}
	if f, ok := refs["skills-lock.json"]; ok && b.AssetKind("skills-lock.json", f) == "generated" {
		raw, e := bundle.RenderedSkillLock(mustDecode(f.Data), refs, vars)
		if e != nil {
			return nil, domain.Wrap("BUNDLE", e)
		}
		f.Data = base64.StdEncoding.EncodeToString(raw)
		f.Digest = safefs.Digest(raw)
		refs["skills-lock.json"] = f
	}
	planner, err := newUpgradePlanner(id, b, p, old, options)
	if err != nil {
		return nil, err
	}
	managed, err := planner.planAssets(refs, selectedLockProduced)
	if err != nil {
		return nil, err
	}

	if command == "diff" || command == "doctor" {
		if e := finalizePlan(p); e != nil {
			return nil, e
		}
		return p, nil
	}
	var history *identitymeta.GovernanceHistory
	if installationMigration != nil {
		history = id.Native.GovernanceHistory
	} else {
		history, err = planOriginalGovernance(p, id)
	}
	if err != nil {
		return nil, err
	}
	meta := Metadata{GovernanceHistory: history, SchemaVersion: 3, Profile: id.Profile.Name, ProfileID: id.Profile.ID, ProtocolVersion: domain.ProtocolVersion, CLIVersion: domain.Version, TemplateVersion: b.LegacyVersion, LegacyCLIVersion: b.LegacyVersion, TemplateCommit: b.TemplateCommit, SnapshotHash: b.SnapshotHash, ManifestHash: b.ManifestHash, TemplateSourceState: b.SourceState, Managed: managed, Variables: vars, Distribution: distribution}
	if b.SchemaVersion >= 4 {
		meta.SchemaVersion = b.SchemaVersion
	}
	if b.SchemaVersion >= 2 {
		meta.TemplateVersion = b.TemplateVersion
		meta.BundleSchemaVersion = b.SchemaVersion
		meta.BundleHash = b.BundleHash
		source := domain.BuildProvenance()
		meta.CLICommit = source.Commit
		meta.CLISourceState = source.SourceState
	}
	if b.SchemaVersion == 1 {
		meta.BundleSchemaVersion = 2
		meta.BundleHash = bundle.Hash(b)
		meta.TemplateVersion = b.LegacyVersion
		source := domain.BuildProvenance()
		meta.CLICommit = source.Commit
		meta.CLISourceState = source.SourceState
	}
	mb, _ := json.Marshal(managed)
	meta.BaselineDigest = safefs.Digest(mb)
	data, e := jsonBytes(meta)
	if e != nil {
		return nil, e
	}
	before, e := safefs.Describe(id.Root, MetadataFile)
	if e != nil {
		return nil, e
	}
	p.Inputs[MetadataFile] = before
	after := domain.Descriptor{Type: "file", Digest: safefs.Digest(data), Mode: 0644}
	if before != after {
		p.Changes = append(p.Changes, Change{MetadataFile, before, after, base64.StdEncoding.EncodeToString(data), "runtime-metadata"})
	}
	addSystemAsset(p, MetadataFile, before, after, "generated")
	p.AssetScope = union(p.AssetScope, []string{MetadataFile})
	if e := finalizePlan(p); e != nil {
		return nil, e
	}
	return p, nil
}
func planDigest(p *Plan) string {
	copy := *p
	copy.Digest = ""
	b, _ := json.Marshal(copy)
	return safefs.Digest(b)
}
func SavePlan(p *Plan, file string) error {
	return saveNewPlan(p.Root, p, file)
}

func saveNewPlan(root string, value any, file string) error {
	file, e := filepath.Abs(file)
	if e != nil {
		return e
	}
	for _, part := range strings.Split(filepath.ToSlash(file), "/") {
		if strings.EqualFold(part, ".git") {
			return domain.Fail("PROTECTED", "计划输出不得位于 Git 内部目录")
		}
	}
	rel, e := filepath.Rel(root, file)
	if e != nil {
		return e
	}
	if rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !strings.HasPrefix(filepath.ToSlash(rel), ".yss/plans/") {
		return domain.Fail("PROTECTED", "项目内计划输出仅允许 .yss/plans；其他计划保存到项目外新文件")
	}
	parent := filepath.Dir(file)
	canonicalParent, e := filepath.EvalSymlinks(parent)
	if e != nil {
		return e
	}
	file, e = safefs.Path(canonicalParent, filepath.Base(file))
	if e != nil {
		return e
	}
	// An outside alias may resolve into this project's Git or business assets.
	for _, part := range strings.Split(filepath.ToSlash(file), "/") {
		if strings.EqualFold(part, ".git") {
			return domain.Fail("PROTECTED", "计划输出不得通过链接进入 Git 内部目录")
		}
	}
	rel, e = filepath.Rel(root, file)
	if e != nil {
		return e
	}
	if rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !strings.HasPrefix(filepath.ToSlash(rel), ".yss/plans/") {
		return domain.Fail("PROTECTED", "计划输出不得通过链接进入项目业务目录")
	}
	b, e := jsonBytes(value)
	if e != nil {
		return e
	}
	f, e := os.OpenFile(file, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	defer f.Close()
	_, e = f.Write(b)
	if e != nil {
		return e
	}
	return f.Sync()
}
func ReadPlan(file string) (*Plan, error) {
	b, e := os.ReadFile(file)
	if e != nil {
		return nil, e
	}
	// Plans use the same strict JSON boundary as identity assets.
	if !json.Valid(b) {
		return nil, domain.Fail("PLAN", "计划必须是单个 JSON 文档")
	}
	if _, e = schema.Parse(b); e != nil {
		return nil, e
	}
	var p Plan
	d := json.NewDecoder(strings.NewReader(string(b)))
	d.DisallowUnknownFields()
	if e = d.Decode(&p); e != nil {
		return nil, e
	}
	if (p.SchemaVersion != 1 && p.SchemaVersion != 2 && p.SchemaVersion != 3 && p.SchemaVersion != 4) || p.ProtocolVersion != domain.ProtocolVersion || p.Digest != planDigest(&p) {
		return nil, domain.Fail("PLAN", "计划版本或摘要不合法")
	}
	return &p, nil
}
func Apply(p *Plan) (transaction.Result, error) {
	return ApplyContext(context.Background(), p)
}
func ApplyContext(ctx context.Context, p *Plan) (transaction.Result, error) {
	a, err := prepareApplication(ctx, p)
	if err != nil {
		return transaction.Result{}, err
	}
	return transaction.ApplyContextWithValidation(ctx, p.Root, p.Command, a.ops, p.Inputs, a.materials, func() error { return verifyAppliedPlan(ctx, p, a.bundle) })
}

type applicationInputs struct {
	bundle    *bundle.Bundle
	ops       []transaction.Operation
	materials []transaction.Artifact
}

// PreflightApplyContext shares every native apply input check without creating
// target directories, acquiring a write lock, or trusting its result at apply.
func PreflightApplyContext(ctx context.Context, p *Plan) error {
	_, err := prepareApplication(ctx, p)
	return err
}

func prepareApplication(ctx context.Context, p *Plan) (*applicationInputs, error) {
	if ctx == nil {
		return nil, domain.Fail("ARGUMENT", "取消上下文不可为空")
	}
	if err := ctx.Err(); err != nil {
		return nil, domain.Wrap("CANCELLED", err)
	}
	if p == nil {
		return nil, domain.Fail("PLAN", "缺少保存计划")
	}
	if p.Command == "doctor" || p.Command == "diff" {
		return nil, domain.Fail("PLAN", "只读检查计划不能应用；请生成显式同步或迁移计划")
	}
	if p.SchemaVersion != 2 && p.SchemaVersion != 3 && p.SchemaVersion != 4 {
		return nil, domain.Fail("PLAN_VERSION", "旧保存计划不能应用；须用新 CLI 重新生成")
	}
	if p.Digest != planDigest(p) {
		return nil, domain.Fail("PLAN", "计划摘要不合法")
	}
	if len(p.Conflicts) > 0 || len(p.Blockers) > 0 || !p.ReadyToApply {
		return nil, domain.Explain(domain.Fail("CONFLICT", "存在用户修改冲突，须先人工合并"), "MANAGED_FILE_CONFLICT", "保存计划中存在尚未关闭的文件冲突或阻断项。", map[string]any{"root": p.Root, "conflicts": p.Conflicts, "blockers": p.Blockers})
	}
	b, e := bundle.Load(p.Profile)
	if e != nil {
		return nil, e
	}
	if b.TemplateCommit != p.TemplateCommit || b.SnapshotHash != p.SnapshotHash {
		return nil, domain.Fail("BUNDLE", "计划绑定快照已改变")
	}
	if p.InstallationMigration != nil {
		_, e = Detect(p.Root, p.Profile, false)
	} else {
		_, e = migrationIdentity(p.Root, p.Profile, p.Command, p.Command == "init" || p.Command == "attach")
	}
	if e != nil {
		return nil, e
	}
	if e = verifyInstallationWork(ctx, p); e != nil {
		return nil, e
	}
	for ref, before := range p.Inputs {
		now, e := workDescribe(p.Root, ref)
		if e != nil {
			return nil, e
		}
		if now != before {
			return nil, domain.Explain(domain.Fail("INPUT_DRIFT", "计划输入发生变化: "+ref), "SAVED_PLAN_INPUT_CHANGED", "当前输入与保存计划绑定的字节或权限不同，必须重新生成计划。", map[string]any{"root": p.Root, "path": ref, "expected": before, "observed": now})
		}
	}
	rebuilt, e := BuildWithOptions(p.Root, p.Profile, p.Command, p.Variables, p.Selection, p.Binding, planningOptionsForReplay(p))
	if e != nil {
		return nil, e
	}
	if rebuilt.Digest != p.Digest {
		return nil, domain.Fail("PLAN", "计划不符合当前快照生成结果；拒绝编辑后重绑定的计划")
	}
	ops, e := nativePlanOperations(p)
	if e != nil {
		return nil, e
	}
	materials := make([]transaction.Artifact, 0, len(p.Materials))
	for _, m := range p.Materials {
		data := rebuilt.materialData[m.Descriptor.Digest]
		if safefs.Digest(data) != m.Descriptor.Digest {
			return nil, domain.Fail("PLAN", "模板材料摘要不合法")
		}
		materials = append(materials, transaction.Artifact{ArtifactRecord: transaction.ArtifactRecord{Path: m.Path, Kind: m.Kind, Descriptor: m.Descriptor}, Data: data})
	}
	return &applicationInputs{bundle: b, ops: ops, materials: materials}, nil
}

func nativePlanOperations(p *Plan) ([]transaction.Operation, error) {
	ops := make([]transaction.Operation, 0, len(p.Changes))
	for _, c := range p.Changes {
		before := c.Before
		if c.After.Type == "directory" {
			if c.Data != "" {
				return nil, domain.Fail("PLAN", "目录操作不得含候选字节")
			}
			ops = append(ops, transaction.Operation{Path: c.Path, Directory: true, Mode: c.After.Mode, Before: &before})
			continue
		}
		if c.After.Type == "missing" {
			if c.Data != "" {
				return nil, domain.Fail("PLAN", "删除操作不得含候选字节")
			}
			ops = append(ops, transaction.Operation{Path: c.Path, Delete: true, Before: &before})
			continue
		}
		data, e := base64.StdEncoding.DecodeString(c.Data)
		if e != nil || safefs.Digest(data) != c.After.Digest {
			return nil, domain.Fail("PLAN", "计划内容摘要不合法: "+c.Path)
		}
		ops = append(ops, transaction.Operation{Path: c.Path, Data: data, Mode: c.After.Mode, Before: &before})
	}
	return ops, nil
}
