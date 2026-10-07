package project

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"strings"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/governance"
	"github.com/iloveZzz/yss-cli/internal/identitymeta"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/schema"
	"github.com/iloveZzz/yss-cli/internal/transaction"
)

const ProfileLinksFile = identitymeta.ProfileLinksFile
const ProfileLinksKind = "profile-links"

type ProfileTargets map[string]string

type ProfileLinks = identitymeta.ProfileLinks

type ProfileTargetPlan struct {
	Profile string                       `json:"profile"`
	Root    string                       `json:"root"`
	Action  string                       `json:"action"`
	Init    *Plan                        `json:"init,omitempty"`
	Inputs  map[string]domain.Descriptor `json:"inputs,omitempty"`
}

type ProfileLinkChange struct {
	Profile      string `json:"profile"`
	PreviousRoot string `json:"previous_root"`
	TargetRoot   string `json:"target_root"`
}

type ProfilesPlan struct {
	SchemaVersion    int                          `json:"schemaVersion"`
	ProtocolVersion  int                          `json:"protocolVersion"`
	Kind             string                       `json:"kind"`
	SourceRoot       string                       `json:"sourceRoot"`
	SourceCheckpoint string                       `json:"source_checkpoint,omitempty"`
	SourceProfile    string                       `json:"sourceProfile"`
	SourceRefs       []string                     `json:"sourceRefs"`
	SourceInputs     map[string]domain.Descriptor `json:"sourceInputs"`
	Variables        map[string]string            `json:"variables"`
	Targets          []ProfileTargetPlan          `json:"targets"`
	Changes          []ProfileLinkChange          `json:"changes"`
	LinksBefore      domain.Descriptor            `json:"linksBefore"`
	LinksAfter       domain.Descriptor            `json:"linksAfter"`
	Links            ProfileLinks                 `json:"links"`
	Digest           string                       `json:"digest"`
}

type ProfileTargetResult struct {
	Profile     string              `json:"profile"`
	Root        string              `json:"root"`
	Action      string              `json:"action"`
	Status      string              `json:"status"`
	Code        string              `json:"code,omitempty"`
	Message     string              `json:"message,omitempty"`
	Transaction *transaction.Result `json:"transaction,omitempty"`
}

type ProfilesResult struct {
	Status           string                `json:"status"`
	SourceRoot       string                `json:"sourceRoot"`
	Registration     transaction.Result    `json:"registration"`
	Targets          []ProfileTargetResult `json:"targets"`
	Code             string                `json:"code,omitempty"`
	Message          string                `json:"message,omitempty"`
	RecoveryCommands [][]string            `json:"recovery_commands,omitempty"`
	plan             *ProfilesPlan
	planFile         string
}

// Callbacks keep formal source and handoff policy in its existing owner. A
// successful preflight is never cached as authorization for a later write.
type ProfileApplyOptions struct {
	ValidateSource func(context.Context, string) error
	BeforeTarget   func(context.Context, ProfileTargetPlan) error
	AfterTarget    func(context.Context, ProfileTargetPlan) error
	PlanFile       string
}

func (p *ProfilesPlan) Public() map[string]any {
	targets := make([]map[string]any, 0, len(p.Targets))
	for _, target := range p.Targets {
		row := map[string]any{"profile": target.Profile, "root": target.Root, "action": target.Action}
		if target.Init != nil {
			row["init"] = target.Init.Public()
		}
		targets = append(targets, row)
	}
	result := map[string]any{"schemaVersion": p.SchemaVersion, "protocolVersion": p.ProtocolVersion, "kind": p.Kind, "sourceRoot": p.SourceRoot, "sourceProfile": p.SourceProfile, "targets": targets, "changes": p.Changes, "links": p.Links, "linksBefore": p.LinksBefore, "linksAfter": p.LinksAfter, "digest": p.Digest, "readyToApply": true}
	if p.SourceCheckpoint != "" {
		result["source_checkpoint"] = p.SourceCheckpoint
	}
	return result
}

func profileRoot(root string) (string, error) {
	return identitymeta.NormalizeProfileRoot(root)
}

func profileRootsOverlap(a, b string) bool {
	return identitymeta.ProfileRootsOverlap(a, b)
}

func validateProfileLinks(root string, links *ProfileLinks) error {
	return identitymeta.ValidateProfileLinks(root, links)
}

func ReadProfileLinks(root string) (*ProfileLinks, error) {
	return identitymeta.ReadProfileLinks(root)
}

func decodeProfileJSON(raw []byte, out any) error {
	if !json.Valid(raw) {
		return domain.Fail("PLAN", "必须是单个 JSON 文档")
	}
	if _, err := schema.Parse(raw); err != nil {
		return err
	}
	d := json.NewDecoder(strings.NewReader(string(raw)))
	d.DisallowUnknownFields()
	return d.Decode(out)
}

func profileIdentityInputs(root string, refs []string) (map[string]domain.Descriptor, error) {
	inputs := map[string]domain.Descriptor{}
	for _, ref := range refs {
		if ref == ProfileLinksFile {
			return nil, domain.Fail("PLAN", "Profile 关联使用独立的前后置守卫")
		}
		d, err := safefs.Describe(root, ref)
		if err != nil {
			return nil, err
		}
		inputs[ref] = d
	}
	return inputs, nil
}

func profileIdentityRefs(profile string) []string {
	return []string{"yss-project.yaml", MetadataFile, "CONTEXT.md", ".template-spec/process/harness-profile.yaml", domain.Profiles[profile].Metadata}
}

func profileSourceRefs(refs []string) []string {
	kept := []string{}
	for _, ref := range refs {
		if ref == ".yss/transactions" || strings.HasPrefix(ref, ".yss/transactions/") || ref == ".yss/asset-transactions" || strings.HasPrefix(ref, ".yss/asset-transactions/") {
			continue
		}
		kept = append(kept, ref)
	}
	return union(kept)
}

func reusableProfile(ctx context.Context, root, profile string) (*Identity, error) {
	id, err := Detect(root, profile, false)
	if err != nil {
		return nil, err
	}
	if id.Native == nil {
		return nil, domain.Fail("MIGRATION_REQUIRED", "Profile 准备要求原生实例；旧实例先显式迁移")
	}
	if err = CheckLegacyState(root, profile); err != nil {
		return nil, err
	}
	status, err := transaction.Status(root)
	if err != nil {
		return nil, err
	}
	if len(status.Pending) > 0 || len(status.Preparations) > 0 {
		return nil, domain.Fail("INTERRUPTED", "存在未完成事务；先 recover --root "+root+" --apply")
	}
	if _, err = governance.RunContext(ctx, "context", "verify", root, nil); err != nil {
		return nil, err
	}
	return id, nil
}

func PrepareProfiles(ctx context.Context, sourceRoot string, targets ProfileTargets, vars map[string]string, sourceCheckpoint string, sourceRefs []string, validateSource func(context.Context, string) error) (*ProfilesPlan, error) {
	if ctx == nil {
		return nil, domain.Fail("ARGUMENT", "取消上下文不可为空")
	}
	if err := ctx.Err(); err != nil {
		return nil, domain.Wrap("CANCELLED", err)
	}
	sourceRoot, err := profileRoot(sourceRoot)
	if err != nil {
		return nil, err
	}
	id, err := reusableProfile(ctx, sourceRoot, "")
	if err != nil {
		return nil, err
	}
	if id.Profile.Name != "spec" && id.Profile.Name != "design" {
		return nil, domain.Fail("IDENTITY", "Profile 准备来源只支持 Spec 或 Design 原生实例")
	}
	if len(targets) == 0 || len(targets) > 2 {
		return nil, domain.Fail("ARGUMENT", "选择独立 Design，或 Backend、Frontend 中的一项或两项")
	}
	if _, design := targets["design"]; design && (len(targets) != 1 || id.Profile.Name != "spec") {
		return nil, domain.Fail("ARGUMENT", "独立 Design 只能单独承接 Spec 来源")
	}
	if validateSource != nil {
		if err = validateSource(ctx, sourceRoot); err != nil {
			return nil, err
		}
	}
	links, err := ReadProfileLinks(sourceRoot)
	if err != nil {
		return nil, err
	}
	before, err := safefs.Describe(sourceRoot, ProfileLinksFile)
	if err != nil {
		return nil, err
	}
	p := &ProfilesPlan{SchemaVersion: 1, ProtocolVersion: domain.ProtocolVersion, Kind: "profile-prepare", SourceRoot: sourceRoot, SourceProfile: id.Profile.Name, SourceCheckpoint: sourceCheckpoint, SourceRefs: union(profileSourceRefs(sourceRefs), profileIdentityRefs(id.Profile.Name)), Variables: Variables(id, vars), Links: *links, LinksBefore: before, Targets: []ProfileTargetPlan{}}
	if sourceCheckpoint != "" {
		p.SourceRefs = union(p.SourceRefs, []string{sourceCheckpoint})
	}
	p.SourceInputs, err = profileIdentityInputs(sourceRoot, p.SourceRefs)
	if err != nil {
		return nil, err
	}
	previous := map[string]string{}
	for name, root := range links.Links {
		previous[name] = root
	}
	for name := range targets {
		if name != "design" && name != "backend" && name != "frontend" {
			return nil, domain.Fail("ARGUMENT", "未知目标 Profile: "+name)
		}
	}
	for _, name := range []string{"design", "backend", "frontend"} {
		requested, ok := targets[name]
		if !ok {
			continue
		}
		root, err := profileRoot(requested)
		if err != nil {
			return nil, err
		}
		p.Links.Links[name] = root
		if err = validateProfileLinks(sourceRoot, &p.Links); err != nil {
			return nil, err
		}
		target := ProfileTargetPlan{Profile: name, Root: root, Action: "init"}
		observed, err := Detect(root, name, true)
		if err != nil {
			return nil, err
		}
		if observed.Native != nil || observed.Legacy != nil {
			if _, err = reusableProfile(ctx, root, name); err != nil {
				return nil, err
			}
			target.Action = "reuse"
			target.Inputs, err = profileIdentityInputs(root, profileIdentityRefs(name))
		} else {
			target.Init, err = Build(root, name, "init", p.Variables, nil)
			if err == nil {
				err = PreflightApplyContext(ctx, target.Init)
			}
		}
		if err != nil {
			return nil, err
		}
		p.Targets = append(p.Targets, target)
		p.Changes = append(p.Changes, ProfileLinkChange{Profile: name, PreviousRoot: previous[name], TargetRoot: root})
	}
	data, err := jsonBytes(p.Links)
	if err != nil {
		return nil, err
	}
	mode := uint32(0644)
	if before.Type == "file" {
		mode = before.Mode
	}
	p.LinksAfter = domain.Descriptor{Type: "file", Digest: safefs.Digest(data), Mode: mode}
	p.Digest = profilesPlanDigest(p)
	if err = checkProfileSource(ctx, p, before, validateSource); err != nil {
		return nil, err
	}
	return p, nil
}

func profilesPlanDigest(p *ProfilesPlan) string {
	copy := *p
	copy.Digest = ""
	raw, _ := json.Marshal(copy)
	return safefs.Digest(raw)
}

func SaveProfilesPlan(p *ProfilesPlan, file string) error {
	if err := validateProfilesPlan(p); err != nil {
		return err
	}
	abs, err := filepath.Abs(file)
	if err != nil {
		return err
	}
	for _, target := range p.Targets {
		if profileRootsOverlap(target.Root, abs) {
			return domain.Fail("PROTECTED", "联合计划必须保存到所有目标目录之外")
		}
	}
	return saveNewPlan(p.SourceRoot, p, file)
}

func ReadProfilesPlan(file string) (*ProfilesPlan, error) {
	info, err := os.Lstat(file)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > 256<<20 {
		return nil, domain.Fail("PLAN", "联合计划必须是有限大小的普通文件")
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	var p ProfilesPlan
	if err = decodeProfileJSON(raw, &p); err != nil {
		return nil, err
	}
	if err = validateProfilesPlan(&p); err != nil {
		return nil, err
	}
	return &p, nil
}

func validateProfilesPlan(p *ProfilesPlan) error {
	if p == nil || p.SchemaVersion != 1 || p.ProtocolVersion != domain.ProtocolVersion || p.Kind != "profile-prepare" || p.Digest != profilesPlanDigest(p) {
		return domain.Fail("PLAN", "联合计划 schema、协议或摘要不合法")
	}
	root, err := profileRoot(p.SourceRoot)
	if err != nil {
		return err
	}
	if root != p.SourceRoot || (p.SourceProfile != "spec" && p.SourceProfile != "design") || len(p.Targets) == 0 || len(p.Targets) > 2 || len(p.Changes) != len(p.Targets) {
		return domain.Fail("PLAN", "联合计划来源或目标不合法")
	}
	if err = validateProfileLinks(root, &p.Links); err != nil {
		return err
	}
	for _, ref := range profileIdentityRefs(p.SourceProfile) {
		if _, ok := p.SourceInputs[ref]; !ok {
			return domain.Fail("PLAN", "联合计划缺少来源身份守卫: "+ref)
		}
	}
	if p.SourceCheckpoint != "" {
		if d, ok := p.SourceInputs[p.SourceCheckpoint]; !ok || d.Type != "file" {
			return domain.Fail("PLAN", "来源检查点缺少普通文件守卫")
		}
	}
	if len(p.SourceRefs) != len(p.SourceInputs) {
		return domain.Fail("PLAN", "来源引用与守卫不匹配")
	}
	if len(profileSourceRefs(p.SourceRefs)) != len(p.SourceRefs) {
		return domain.Fail("PLAN", "运行事务控制目录不能作为来源批准事实")
	}
	for _, ref := range p.SourceRefs {
		if ref == ProfileLinksFile || safefs.ValidateRef(ref) != nil {
			return domain.Fail("PLAN", "来源引用越界")
		}
		if _, ok := p.SourceInputs[ref]; !ok {
			return domain.Fail("PLAN", "来源引用缺少守卫")
		}
	}
	order := map[string]int{"design": 1, "backend": 2, "frontend": 3}
	last := 0
	previous := ProfileLinks{SchemaVersion: 1, Links: map[string]string{}}
	for name, root := range p.Links.Links {
		previous.Links[name] = root
	}
	for i, target := range p.Targets {
		if order[target.Profile] <= last || target.Root != p.Links.Links[target.Profile] {
			return domain.Fail("PLAN", "联合计划目标重复、未知或与关联不匹配")
		}
		last = order[target.Profile]
		change := p.Changes[i]
		if change.Profile != target.Profile || change.TargetRoot != target.Root {
			return domain.Fail("PLAN", "关联变更记录与目标不匹配")
		}
		if change.PreviousRoot == "" {
			delete(previous.Links, change.Profile)
		} else {
			previous.Links[change.Profile] = change.PreviousRoot
		}
		if target.Profile == "design" && (p.SourceProfile != "spec" || len(p.Targets) != 1) {
			return domain.Fail("PLAN", "独立 Design 只能单独承接 Spec")
		}
		switch target.Action {
		case "init":
			if target.Init == nil || target.Init.SchemaVersion != 2 || target.Init.ProtocolVersion != domain.ProtocolVersion || target.Init.MigrationKind != "" || target.Init.Digest != planDigest(target.Init) || target.Init.Root != target.Root || target.Init.Profile != target.Profile || target.Init.Command != "init" || len(target.Inputs) != 0 {
				return domain.Fail("PLAN", "初始化子计划不匹配")
			}
		case "reuse":
			if target.Init != nil {
				return domain.Fail("PLAN", "复用目标不得包含初始化计划")
			}
			for _, ref := range profileIdentityRefs(target.Profile) {
				if _, ok := target.Inputs[ref]; !ok {
					return domain.Fail("PLAN", "复用目标缺少身份守卫")
				}
			}
		default:
			return domain.Fail("PLAN", "未知目标动作")
		}
	}
	if err = validateProfileLinks(root, &previous); err != nil {
		return err
	}
	if p.LinksBefore.Type == "missing" && len(previous.Links) != 0 {
		return domain.Fail("PLAN", "不存在的关联文件不得声明旧路径")
	}
	data, err := jsonBytes(p.Links)
	if err != nil {
		return err
	}
	mode := uint32(0644)
	if p.LinksBefore.Type == "file" {
		mode = p.LinksBefore.Mode
	}
	if p.LinksAfter.Type != "file" || p.LinksAfter.Digest != safefs.Digest(data) || (p.LinksBefore.Type != "missing" && p.LinksBefore.Type != "file") || p.LinksAfter.Mode != mode || mode == 0 || mode > 0777 {
		return domain.Fail("PLAN", "关联登记候选不匹配")
	}
	return nil
}

func checkProfileDescriptors(root string, inputs map[string]domain.Descriptor) error {
	for ref, before := range inputs {
		now, err := safefs.Describe(root, ref)
		if err != nil {
			return err
		}
		if now != before {
			return domain.Fail("INPUT_DRIFT", "Profile 准备输入已变化: "+filepath.Join(root, ref))
		}
	}
	return nil
}

func checkProfileSource(ctx context.Context, p *ProfilesPlan, expectedLinks domain.Descriptor, validate func(context.Context, string) error) error {
	if err := ctx.Err(); err != nil {
		return domain.Wrap("CANCELLED", err)
	}
	if _, err := reusableProfile(ctx, p.SourceRoot, p.SourceProfile); err != nil {
		return err
	}
	if err := checkProfileDescriptors(p.SourceRoot, p.SourceInputs); err != nil {
		return err
	}
	if err := checkProfileDescriptors(p.SourceRoot, map[string]domain.Descriptor{ProfileLinksFile: expectedLinks}); err != nil {
		return err
	}
	if validate != nil {
		if err := validate(ctx, p.SourceRoot); err != nil {
			return err
		}
	}
	if err := checkProfileDescriptors(p.SourceRoot, p.SourceInputs); err != nil {
		return err
	}
	return checkProfileDescriptors(p.SourceRoot, map[string]domain.Descriptor{ProfileLinksFile: expectedLinks})
}

func preflightProfileTarget(ctx context.Context, target ProfileTargetPlan, registered bool) (bool, error) {
	if target.Action == "reuse" {
		if _, err := reusableProfile(ctx, target.Root, target.Profile); err != nil {
			return false, err
		}
		return true, checkProfileDescriptors(target.Root, target.Inputs)
	}
	observed, err := Detect(target.Root, target.Profile, true)
	if err != nil {
		return false, err
	}
	if observed.Native == nil && observed.Legacy == nil {
		return false, PreflightApplyContext(ctx, target.Init)
	}
	if !registered || observed.Native == nil {
		return false, domain.Fail("INPUT_DRIFT", "待初始化目标已有身份，须重新生成复用计划")
	}
	if _, err = reusableProfile(ctx, target.Root, target.Profile); err != nil {
		return false, err
	}
	ops, err := nativePlanOperations(target.Init)
	if err != nil {
		return false, err
	}
	matched, err := transaction.CommittedApplicationMatches(target.Root, "init", ops)
	if err != nil {
		return false, err
	}
	if !matched {
		return false, domain.Fail("PLAN", "已初始化目标缺少与保存子计划匹配的已提交事务")
	}
	// Only the exact committed initialization can resume a saved partial plan.
	// Added handoff files are independent; any changed init asset blocks reuse.
	for _, change := range target.Init.Changes {
		d, err := workDescribe(target.Root, change.Path)
		if err != nil {
			return false, err
		}
		if d != change.After {
			return false, domain.Fail("INPUT_DRIFT", "已初始化目标与保存计划不同: "+change.Path)
		}
	}
	return true, nil
}

func ValidateProfileLinksTransaction(root, expectedProfile string, paths []string) error {
	if len(paths) != 1 || paths[0] != ProfileLinksFile {
		return domain.Fail("KIND", "Profile 关联事务只能恢复或回退唯一关联文件")
	}
	id, err := Detect(root, expectedProfile, false)
	if err != nil {
		return err
	}
	if id.Native == nil || (id.Profile.Name != "spec" && id.Profile.Name != "design") {
		return domain.Fail("IDENTITY", "Profile 关联恢复要求匹配的原生来源身份")
	}
	return nil
}

func ApplyProfiles(ctx context.Context, p *ProfilesPlan, validateSource func(context.Context, string) error) (*ProfilesResult, error) {
	return ApplyProfilesWithOptions(ctx, p, ProfileApplyOptions{ValidateSource: validateSource})
}

func ApplyProfilesWithOptions(ctx context.Context, p *ProfilesPlan, options ProfileApplyOptions) (*ProfilesResult, error) {
	r := &ProfilesResult{Status: "failed", Targets: []ProfileTargetResult{}}
	if ctx == nil {
		return failProfiles(r, -1, domain.Fail("ARGUMENT", "取消上下文不可为空"))
	}
	if err := validateProfilesPlan(p); err != nil {
		return failProfiles(r, -1, err)
	}
	r.SourceRoot = p.SourceRoot
	r.plan, r.planFile = p, options.PlanFile
	for _, target := range p.Targets {
		r.Targets = append(r.Targets, ProfileTargetResult{Profile: target.Profile, Root: target.Root, Action: target.Action, Status: "not-executed"})
	}
	observedLinks, err := safefs.Describe(p.SourceRoot, ProfileLinksFile)
	if err != nil {
		return failProfiles(r, -1, err)
	}
	registered := observedLinks == p.LinksAfter
	if !registered && observedLinks != p.LinksBefore {
		return failProfiles(r, -1, domain.Fail("INPUT_DRIFT", "来源 Profile 关联已变化，须重新生成计划"))
	}
	if !registered {
		previous, err := ReadProfileLinks(p.SourceRoot)
		if err != nil {
			return failProfiles(r, -1, err)
		}
		expected := maps.Clone(p.Links.Links)
		for _, change := range p.Changes {
			if change.PreviousRoot == "" {
				delete(expected, change.Profile)
			} else {
				expected[change.Profile] = change.PreviousRoot
			}
		}
		if !maps.Equal(previous.Links, expected) {
			return failProfiles(r, -1, domain.Fail("PLAN", "保存计划的旧关联路径与来源不匹配"))
		}
	}
	if err = checkProfileSource(ctx, p, observedLinks, options.ValidateSource); err != nil {
		return failProfiles(r, -1, err)
	}
	for i, target := range p.Targets {
		if _, err = preflightProfileTarget(ctx, target, registered); err != nil {
			return failProfiles(r, i, err)
		}
		if options.BeforeTarget != nil {
			if err = options.BeforeTarget(ctx, target); err != nil {
				return failProfiles(r, i, err)
			}
		}
	}
	if err = checkProfileSource(ctx, p, observedLinks, options.ValidateSource); err != nil {
		return failProfiles(r, -1, err)
	}
	data, _ := jsonBytes(p.Links)
	guards := map[string]domain.Descriptor{}
	for ref, guard := range p.SourceInputs {
		guards[ref] = guard
	}
	guards[ProfileLinksFile] = observedLinks
	ops := []transaction.Operation{}
	if !registered {
		ops = append(ops, transaction.Operation{Path: ProfileLinksFile, Data: data, Mode: p.LinksAfter.Mode, Before: &observedLinks})
	}
	r.Registration, err = transaction.ApplyContextWithValidation(ctx, p.SourceRoot, ProfileLinksKind, ops, guards, nil, func() error {
		if err := checkProfileDescriptors(p.SourceRoot, p.SourceInputs); err != nil {
			return err
		}
		return checkProfileDescriptors(p.SourceRoot, map[string]domain.Descriptor{ProfileLinksFile: p.LinksAfter})
	})
	if err != nil {
		return failProfiles(r, -1, err)
	}
	for i, target := range p.Targets {
		if err = checkProfileSource(ctx, p, p.LinksAfter, options.ValidateSource); err != nil {
			return failProfiles(r, i, err)
		}
		if options.BeforeTarget != nil {
			if err = options.BeforeTarget(ctx, target); err != nil {
				return failProfiles(r, i, err)
			}
		}
		if err = checkProfileSource(ctx, p, p.LinksAfter, options.ValidateSource); err != nil {
			return failProfiles(r, i, err)
		}
		reused, err := preflightProfileTarget(ctx, target, true)
		if err != nil {
			return failProfiles(r, i, err)
		}
		if reused {
			r.Targets[i].Action = "reuse"
		} else {
			applied, err := ApplyContext(ctx, target.Init)
			r.Targets[i].Transaction = &applied
			if err != nil {
				return failProfiles(r, i, err)
			}
		}
		if options.AfterTarget != nil {
			if err = options.AfterTarget(ctx, target); err != nil {
				return failProfiles(r, i, err)
			}
		}
		if _, err = preflightProfileTarget(ctx, target, true); err != nil {
			return failProfiles(r, i, err)
		}
		r.Targets[i].Status = "success"
		if err = checkProfileSource(ctx, p, p.LinksAfter, options.ValidateSource); err != nil {
			return failProfiles(r, -1, err)
		}
	}
	r.Status = "completed"
	return r, nil
}

type profilesFailure struct {
	cause  error
	result *ProfilesResult
}

func (e *profilesFailure) Error() string    { return e.cause.Error() }
func (e *profilesFailure) Unwrap() error    { return e.cause }
func (e *profilesFailure) ErrorResult() any { return e.result }

func failProfiles(r *ProfilesResult, target int, err error) (*ProfilesResult, error) {
	code := "EXECUTION"
	var domainError *domain.Error
	if errors.As(err, &domainError) {
		code = domainError.Code
	}
	r.Code, r.Message = code, err.Error()
	if target >= 0 {
		r.Targets[target].Status, r.Targets[target].Code, r.Targets[target].Message = "failed", code, err.Error()
	}
	r.Status = "failed"
	if r.Registration.Status == "applied" || r.Registration.Status == "unchanged" || r.Registration.Status == "pending" || r.Registration.Status == "blocked" {
		r.Status = "partial"
	}
	for _, row := range r.Targets {
		if row.Status == "success" {
			r.Status = "partial"
		}
	}
	if r.plan != nil {
		if r.Registration.Status == "pending" || r.Registration.Status == "blocked" {
			r.RecoveryCommands = append(r.RecoveryCommands, []string{"yss", "recover", "--root", r.SourceRoot, "--json"}, []string{"yss", "recover", "--root", r.SourceRoot, "--apply", "--json"})
		}
		for _, row := range r.Targets {
			if row.Status == "failed" {
				if info, e := os.Lstat(row.Root); e == nil && info.IsDir() {
					r.RecoveryCommands = append(r.RecoveryCommands, []string{"yss", "recover", "--root", row.Root, "--json"}, []string{"yss", "recover", "--root", row.Root, "--apply", "--json"})
				}
			}
		}
		file := r.planFile
		if file == "" {
			file = "<saved-plan.json>"
		}
		if code == "INPUT_DRIFT" || code == "PLAN" || code == "CONCURRENT" {
			command := []string{"yss", "profile", "prepare", "--root", r.SourceRoot}
			for _, target := range r.plan.Targets {
				command = append(command, "--"+target.Profile+"-root", target.Root)
			}
			if r.plan.SourceCheckpoint != "" {
				command = append(command, "--checkpoint", r.plan.SourceCheckpoint)
			}
			r.RecoveryCommands = append(r.RecoveryCommands, append(command, "--plan", "--out", "<new-plan.json>", "--json"))
		} else {
			r.RecoveryCommands = append(r.RecoveryCommands, []string{"yss", "profile", "prepare", "--root", r.SourceRoot, "--apply", "--plan-file", file, "--json"})
		}
	}
	return r, &profilesFailure{cause: err, result: r}
}
