package project

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"github.com/iloveZzz/yss-cli/internal/bundle"
	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/governance"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/schema"
	"github.com/iloveZzz/yss-cli/internal/transaction"
	"os"
	"path/filepath"
	"sort"
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
	SchemaVersion   int                          `json:"schemaVersion"`
	ProtocolVersion int                          `json:"protocolVersion"`
	Command         string                       `json:"command"`
	Root            string                       `json:"root"`
	Profile         string                       `json:"profile"`
	TemplateCommit  string                       `json:"templateCommit"`
	SnapshotHash    string                       `json:"snapshotHash"`
	Changes         []Change                     `json:"changes"`
	Conflicts       []string                     `json:"conflicts"`
	Preserved       []string                     `json:"preserved"`
	Inputs          map[string]domain.Descriptor `json:"inputs"`
	Digest          string                       `json:"digest"`
	Variables       map[string]string            `json:"variables"`
	Selection       []string                     `json:"selection"`
}

func (p *Plan) Public() map[string]any {
	changes := []map[string]any{}
	for _, c := range p.Changes {
		changes = append(changes, map[string]any{"path": c.Path, "before": c.Before, "after": c.After, "ownership": c.Ownership})
	}
	return map[string]any{"schemaVersion": p.SchemaVersion, "protocolVersion": p.ProtocolVersion, "command": p.Command, "root": p.Root, "profile": p.Profile, "templateCommit": p.TemplateCommit, "snapshotHash": p.SnapshotHash, "changes": changes, "conflicts": p.Conflicts, "preserved": p.Preserved, "digest": p.Digest, "stats": map[string]int{"changed": len(p.Changes), "conflicts": len(p.Conflicts), "preserved": len(p.Preserved)}}
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
	if id.Legacy != nil {
		files, _ := object(id.Legacy["managedFiles"])
		for ref, v := range files {
			m, ok := object(v)
			if !ok {
				continue
			}
			if id.Profile.Name == "spec" {
				d := domain.Descriptor{Type: "file", Digest: text(m["contentHash"]), Mode: 0644}
				out[ref] = Managed{Baseline: d, Applied: d, Ownership: "managed"}
			} else {
				b, _ := json.Marshal(m)
				var item Managed
				_ = json.Unmarshal(b, &item)
				item.Baseline.Mode = domain.FileMode(item.Baseline.Mode)
				item.Applied.Mode = domain.FileMode(item.Applied.Mode)
				out[ref] = item
			}
		}
	}
	return out
}
func Variables(id *Identity, provided map[string]string) map[string]string {
	v := map[string]string{"projectName": filepath.Base(id.Root), "businessDomain": "待补充", "teamSize": "待补充"}
	if id.Native != nil {
		for k, x := range id.Native.Variables {
			v[k] = x
		}
	} else if id.Legacy != nil {
		if old, ok := object(id.Legacy["variables"]); ok {
			for k, x := range old {
				if s, ok := x.(string); ok {
					v[k] = s
				}
			}
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
	init := command == "init"
	attach := command == "attach"
	id, err := Detect(root, profile, init || attach)
	if err != nil {
		return nil, err
	}
	if id.Profile.Name == "" {
		return nil, domain.Fail("IDENTITY", "初始化或接管需要 --profile")
	}
	if command != "diff" && command != "doctor" {
		if err = CheckLegacyState(id.Root, id.Profile.Name); err != nil {
			return nil, err
		}
	}
	if init {
		if entries, e := os.ReadDir(id.Root); e == nil && len(entries) > 0 {
			return nil, domain.Fail("CONFLICT", "init 需要空目录")
		} else if e != nil && !os.IsNotExist(e) {
			return nil, e
		}
	}
	if !init {
		if ok, e := exists(id.Root, "CONTEXT.md"); e != nil {
			return nil, e
		} else if ok {
			if _, e := governance.Run("context", "verify", id.Root, nil); e != nil {
				return nil, e
			}
		}
	}
	if !init && !attach && id.Native == nil && command != "migrate" && command != "diff" && command != "doctor" {
		return nil, domain.Fail("MIGRATION_REQUIRED", "旧实例须先只读检查，再执行显式 migrate")
	}
	b, err := bundle.Load(id.Profile.Name)
	if err != nil {
		return nil, err
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
	if err != nil {
		return nil, err
	}
	p := &Plan{SchemaVersion: 1, ProtocolVersion: domain.ProtocolVersion, Command: command, Root: id.Root, Profile: id.Profile.Name, TemplateCommit: b.TemplateCommit, SnapshotHash: b.SnapshotHash, Changes: []Change{}, Conflicts: []string{}, Preserved: []string{}, Inputs: map[string]domain.Descriptor{}, Variables: vars, Selection: selection}
	// Bind identity and vocabulary inputs even when preserved custom files do not
	// appear in managedFiles. Transaction guards recheck them under the write lock.
	for _, ref := range []string{"yss-project.yaml", ".template-spec/process/harness-profile.yaml", "CONTEXT.md", MetadataFile, id.Profile.Metadata} {
		d, e := safefs.Describe(id.Root, ref)
		if e != nil {
			return nil, e
		}
		p.Inputs[ref] = d
	}
	refs := map[string]bundle.File{}
	old := baseline(id)
	if init || attach {
		for ref, f := range b.Initial {
			refs[ref] = f
		}
	} else {
		for ref := range old {
			if f, ok := b.Initial[ref]; ok {
				refs[ref] = f
			} else if f, ok := b.Files[ref]; ok {
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
	if init || attach || command == "migrate" || id.Native != nil {
		if _, ok := refs[".template-spec/process/harness-profile.yaml"]; !ok {
			data := "schema_version: 2\nprofile_id: " + id.Profile.ID + "\ninstantiation:\n  cli_package: " + id.Profile.LegacyCommand + "\n  metadata_file: " + id.Profile.Metadata + "\n  template_source: " + id.Profile.TemplateSource + "\n"
			refs[".template-spec/process/harness-profile.yaml"] = bundle.File{Data: base64.StdEncoding.EncodeToString([]byte(data)), Digest: safefs.Digest([]byte(data)), Mode: 0644, Ownership: "managed"}
		}
	}
	if id.Profile.Name == "spec" && text(distribution["mode"]) == "selected" {
		f, e := selectedLock(b, distribution)
		if e != nil {
			return nil, e
		}
		refs["skills-lock.json"] = f
	}
	managed := map[string]Managed{}
	for ref, m := range old {
		managed[ref] = m
	}
	keys := make([]string, 0, len(refs))
	for ref := range refs {
		keys = append(keys, ref)
	}
	sort.Strings(keys)
	for _, ref := range keys {
		f := refs[ref]
		before, e := safefs.Describe(id.Root, ref)
		if e != nil {
			return nil, e
		}
		p.Inputs[ref] = before
		data, e := f.Render(vars)
		if e != nil {
			return nil, e
		}
		desired := domain.Descriptor{Type: "file", Digest: safefs.Digest(data), Mode: domain.FileMode(f.Mode)}
		previous, wasManaged := old[ref]
		preserve := !init && (ref == "CONTEXT.md" || f.Ownership == "user-owned" || f.Ownership == "generated" || f.Ownership == "managed-customizable")
		if before == desired {
			managed[ref] = Managed{Baseline: desired, Applied: before, Ownership: f.Ownership}
			continue
		}
		if before.Type == "file" && preserve {
			p.Preserved = append(p.Preserved, ref)
			if wasManaged {
				managed[ref] = previous
			}
			continue
		}
		if before.Type != "missing" && (!wasManaged || before.Digest != previous.Applied.Digest || ((id.Native != nil || id.Profile.Name != "spec") && before.Mode != previous.Applied.Mode)) {
			p.Conflicts = append(p.Conflicts, ref)
			continue
		}
		if before.Type == "missing" && wasManaged && command == "sync" {
			p.Conflicts = append(p.Conflicts, ref)
			continue
		}
		if f.Ownership == "user-owned" && command != "init" {
			continue
		}
		p.Changes = append(p.Changes, Change{ref, before, desired, base64.StdEncoding.EncodeToString(data), f.Ownership})
		managed[ref] = Managed{Baseline: desired, Applied: desired, Ownership: f.Ownership}
	}
	if command == "diff" || command == "doctor" {
		p.Digest = planDigest(p)
		return p, nil
	}
	meta := Metadata{SchemaVersion: 1, Profile: id.Profile.Name, ProfileID: id.Profile.ID, ProtocolVersion: domain.ProtocolVersion, CLIVersion: domain.Version, TemplateVersion: b.LegacyVersion, LegacyCLIVersion: b.LegacyVersion, TemplateCommit: b.TemplateCommit, SnapshotHash: b.SnapshotHash, ManifestHash: b.ManifestHash, TemplateSourceState: b.SourceState, Managed: managed, Variables: vars, Distribution: distribution}
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
	if id.LegacyFile != "" {
		d, e := safefs.Describe(id.Root, id.LegacyFile)
		if e != nil {
			return nil, e
		}
		p.Inputs[id.LegacyFile] = d
		if command == "migrate" {
			raw, e := os.ReadFile(filepath.Join(id.Root, id.LegacyFile))
			if e != nil {
				return nil, e
			}
			p.Changes = append(p.Changes, Change{id.LegacyFile, d, d, base64.StdEncoding.EncodeToString(raw), "legacy-metadata-archive"})
		}
	}
	after := domain.Descriptor{Type: "file", Digest: safefs.Digest(data), Mode: 0644}
	if before != after {
		p.Changes = append(p.Changes, Change{MetadataFile, before, after, base64.StdEncoding.EncodeToString(data), "runtime-metadata"})
	}
	p.Digest = planDigest(p)
	return p, nil
}
func planDigest(p *Plan) string {
	copy := *p
	copy.Digest = ""
	b, _ := json.Marshal(copy)
	return safefs.Digest(b)
}
func SavePlan(p *Plan, file string) error {
	file, e := filepath.Abs(file)
	if e != nil {
		return e
	}
	for _, part := range strings.Split(filepath.ToSlash(file), "/") {
		if strings.EqualFold(part, ".git") {
			return domain.Fail("PROTECTED", "计划输出不得位于 Git 内部目录")
		}
	}
	rel, e := filepath.Rel(p.Root, file)
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
	rel, e = filepath.Rel(p.Root, file)
	if e != nil {
		return e
	}
	if rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !strings.HasPrefix(filepath.ToSlash(rel), ".yss/plans/") {
		return domain.Fail("PROTECTED", "计划输出不得通过链接进入项目业务目录")
	}
	b, e := jsonBytes(p)
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
	if p.SchemaVersion != 1 || p.ProtocolVersion != domain.ProtocolVersion || p.Digest != planDigest(&p) {
		return nil, domain.Fail("PLAN", "计划版本或摘要不合法")
	}
	return &p, nil
}
func Apply(p *Plan) (transaction.Result, error) {
	return ApplyContext(context.Background(), p)
}
func ApplyContext(ctx context.Context, p *Plan) (transaction.Result, error) {
	if p.Digest != planDigest(p) {
		return transaction.Result{}, domain.Fail("PLAN", "计划摘要不合法")
	}
	if len(p.Conflicts) > 0 {
		return transaction.Result{}, domain.Fail("CONFLICT", "存在用户修改冲突，须先人工合并")
	}
	b, e := bundle.Load(p.Profile)
	if e != nil {
		return transaction.Result{}, e
	}
	if b.TemplateCommit != p.TemplateCommit || b.SnapshotHash != p.SnapshotHash {
		return transaction.Result{}, domain.Fail("BUNDLE", "计划绑定快照已改变")
	}
	if _, e = Detect(p.Root, p.Profile, p.Command == "init" || p.Command == "attach"); e != nil {
		return transaction.Result{}, e
	}
	for ref, before := range p.Inputs {
		now, e := safefs.Describe(p.Root, ref)
		if e != nil {
			return transaction.Result{}, e
		}
		if now != before {
			return transaction.Result{}, domain.Fail("INPUT_DRIFT", "计划输入发生变化: "+ref)
		}
	}
	rebuilt, e := Build(p.Root, p.Profile, p.Command, p.Variables, p.Selection)
	if e != nil {
		return transaction.Result{}, e
	}
	if rebuilt.Digest != p.Digest {
		return transaction.Result{}, domain.Fail("PLAN", "计划不符合当前快照生成结果；拒绝编辑后重绑定的计划")
	}
	ops := make([]transaction.Operation, 0, len(p.Changes))
	for _, c := range p.Changes {
		data, e := base64.StdEncoding.DecodeString(c.Data)
		if e != nil || safefs.Digest(data) != c.After.Digest {
			return transaction.Result{}, domain.Fail("PLAN", "计划内容摘要不合法: "+c.Path)
		}
		before := c.Before
		ops = append(ops, transaction.Operation{Path: c.Path, Data: data, Mode: c.After.Mode, Before: &before})
	}
	return transaction.ApplyContextWithGuards(ctx, p.Root, p.Command, ops, p.Inputs)
}
