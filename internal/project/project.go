// Package project installs and updates only files recorded in its own baseline.
package project

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/iloveZzz/yss-cli/internal/bundle"
	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/identitymeta"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/transaction"
)

type Options struct {
	Root, Profile, Installation string
	Variables                   map[string]string
	Skills                      []string
	DryRun                      bool
}
type Change struct {
	Path   string            `json:"path"`
	Before domain.Descriptor `json:"before"`
	After  domain.Descriptor `json:"after"`
}
type Result struct {
	Root         string              `json:"root"`
	Profile      string              `json:"profile"`
	Installation string              `json:"installation"`
	Status       string              `json:"status"`
	Changes      []Change            `json:"changes"`
	Conflicts    []string            `json:"conflicts"`
	Preserved    []string            `json:"preserved"`
	Transaction  *transaction.Result `json:"transaction,omitempty"`
}
type installation struct {
	root, profile, kind string
	meta                *identitymeta.Metadata
	before              domain.Descriptor
}

func detect(o Options, allowAbsent bool) (*installation, error) {
	root, err := filepath.Abs(o.Root)
	if err != nil {
		return nil, err
	}
	d, err := safefs.Describe(root, domain.MetadataFile)
	if err != nil {
		return nil, err
	}
	i := &installation{root: root, profile: o.Profile, kind: o.Installation, before: d}
	if d.Type == "file" {
		raw, err := os.ReadFile(filepath.Join(root, domain.MetadataFile))
		if err != nil {
			return nil, err
		}
		if safefs.Digest(raw) != d.Digest {
			return nil, domain.Fail("INPUT_DRIFT", "安装记录读取时发生变化")
		}
		i.meta, err = identitymeta.ValidateNative(root, raw, o.Profile)
		if err != nil {
			return nil, err
		}
		i.profile = i.meta.Profile
		if i.kind == "" {
			i.kind, _ = i.meta.Distribution["installation"].(string)
		}
	} else if !allowAbsent {
		return nil, domain.Fail("IDENTITY", "未接入 YSS；先使用 init 或 attach")
	}
	if i.profile == "" {
		return nil, domain.Fail("ARGUMENT", "新安装须指定 --profile spec|backend")
	}
	if _, err := domain.GetProfile(i.profile); err != nil {
		return nil, err
	}
	if i.kind == "" {
		i.kind = "project"
	}
	if i.kind != "project" && i.kind != "skills" {
		return nil, domain.Fail("ARGUMENT", "--installation 只支持 project|skills")
	}
	if i.meta == nil {
		for _, p := range domain.Profiles {
			d, e := safefs.Describe(root, p.Metadata)
			if e != nil {
				return nil, e
			}
			if d.Type != "missing" {
				return nil, domain.Fail("IDENTITY", "仅旧家族记录的项目须由 Agent 迁移，保留原文件")
			}
		}
	}
	return i, nil
}

func protect(root, ref string) error {
	if _, err := safefs.Path(root, ref); err != nil {
		return err
	}
	for parent := path.Dir(ref); parent != "."; parent = path.Dir(parent) {
		// The target path was checked above; inspect the Git marker without following it.
		p := filepath.Join(root, filepath.FromSlash(parent), ".git")
		if _, err := os.Lstat(p); err == nil {
			return domain.Fail("PROTECTED", "目标位于独立仓库或子模块: "+ref)
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}
func userOwned(ref, ownership string) bool {
	if ownership == "user-owned" || ownership == "protected" {
		return true
	}
	switch ref {
	case "CONTEXT.md", "DESIGN.md", "README.md", "yss-project.yaml", ".template-spec/agents/issue-tracker.md":
		return true
	}
	return strings.HasPrefix(ref, ".work/") || strings.HasPrefix(ref, "docs/") || strings.Contains(ref, "/governance-history/")
}
func encode(v any) []byte { b, _ := json.MarshalIndent(v, "", "  "); return append(b, '\n') }
func describe(data []byte, mode uint32) domain.Descriptor {
	return domain.Descriptor{Type: "file", Digest: safefs.Digest(data), Mode: domain.FileMode(mode)}
}

func Run(ctx context.Context, command string, o Options) (Result, error) {
	out := Result{Changes: []Change{}, Conflicts: []string{}, Preserved: []string{}}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	i, err := detect(o, command == "init" || command == "attach")
	if err != nil {
		return out, err
	}
	out.Root, out.Profile, out.Installation = i.root, i.profile, i.kind
	if (command == "init" || command == "attach") && i.meta != nil {
		return out, domain.Fail("IDENTITY", "项目已接入；使用 sync 更新")
	}
	if command == "init" {
		entries, e := os.ReadDir(i.root)
		if e != nil && !os.IsNotExist(e) {
			return out, e
		}
		if len(entries) > 0 {
			return out, domain.Fail("CONFLICT", "init 目标必须为空；已有项目使用 attach")
		}
	}
	b, err := bundle.Load(i.profile)
	if err != nil {
		return out, err
	}
	view, err := b.ForInstallation(i.kind)
	if err != nil {
		return out, err
	}
	vars := map[string]string{"projectName": filepath.Base(i.root), "businessDomain": "待补充", "teamSize": "待补充"}
	if i.meta != nil {
		for k, v := range i.meta.Variables {
			vars[k] = v
		}
	}
	for k, v := range o.Variables {
		if v != "" {
			vars[k] = v
		}
	}
	desired := map[string]bundle.File{}
	if command == "skills" {
		desired, err = view.SkillFiles(o.Skills)
		if err != nil {
			return out, err
		}
	} else {
		for ref, f := range view.Initial {
			desired[ref] = f
		}
		if i.meta != nil {
			names := []string{}
			for ref := range i.meta.Managed {
				parts := strings.Split(ref, "/")
				if len(parts) == 4 && parts[0] == ".agents" && parts[1] == "skills" && parts[3] == "SKILL.md" {
					if _, available := view.Files[ref]; available {
						names = append(names, parts[2])
					}
				}
			}
			selected, e := view.SkillFiles(names)
			if e != nil {
				return out, e
			}
			for ref, f := range selected {
				if _, exists := desired[ref]; !exists {
					desired[ref] = f
				}
			}
		}
	}
	managed := map[string]identitymeta.Managed{}
	if i.meta != nil {
		for ref, m := range i.meta.Managed {
			managed[ref] = m
		}
	}
	guards := map[string]domain.Descriptor{domain.MetadataFile: i.before}
	ops := []transaction.Operation{}
	artifacts := []transaction.Artifact{}
	refs := map[string]bool{}
	for ref := range desired {
		refs[ref] = true
	}
	if command != "skills" {
		for ref := range managed {
			refs[ref] = true
		}
	}
	ordered := make([]string, 0, len(refs))
	for ref := range refs {
		ordered = append(ordered, ref)
	}
	sort.Strings(ordered)
	for _, ref := range ordered {
		if i.meta != nil && command == "sync" && i.meta.SchemaVersion < 6 && (ref == ".yss-backend-plugin.json" || ref == ".yss-plugin.json" || ref == ".yss-execution-scope.yaml") {
			continue
		}
		if err := ctx.Err(); err != nil {
			return out, err
		}
		if err := protect(i.root, ref); err != nil {
			return out, err
		}
		current, err := safefs.Describe(i.root, ref)
		if err != nil {
			return out, err
		}
		guards[ref] = current
		previous, known := managed[ref]
		f, wanted := desired[ref]
		if !wanted {
			if userOwned(ref, previous.Ownership) {
				out.Preserved = append(out.Preserved, ref)
				delete(managed, ref)
				continue
			}
			if current.Type == "missing" {
				delete(managed, ref)
				continue
			}
			// Local resolutions are user content even when they equal lastApplied.
			if !domain.SameDescriptor(current, previous.Applied) || !domain.SameDescriptor(previous.Applied, previous.Baseline) {
				out.Conflicts = append(out.Conflicts, ref)
				continue
			}
			ops = append(ops, transaction.Operation{Path: ref, Delete: true, Before: &current})
			out.Changes = append(out.Changes, Change{ref, current, domain.Descriptor{Type: "missing"}})
			delete(managed, ref)
			continue
		}
		data, err := f.Render(vars)
		if err != nil {
			return out, err
		}
		target := describe(data, f.Mode)
		if userOwned(ref, f.Ownership) && current.Type != "missing" {
			out.Preserved = append(out.Preserved, ref)
			if !known {
				continue
			}
			continue
		}
		if !domain.SameDescriptor(current, target) {
			if known && domain.SameDescriptor(target, previous.Baseline) {
				out.Preserved = append(out.Preserved, ref)
				continue
			}
			if current.Type != "missing" && (!known || !domain.SameDescriptor(current, previous.Applied) || !domain.SameDescriptor(previous.Applied, previous.Baseline)) {
				out.Conflicts = append(out.Conflicts, ref)
				continue
			}
			// A deleted managed file is a local modification.
			if known && current.Type == "missing" {
				out.Conflicts = append(out.Conflicts, ref)
				continue
			}
			ops = append(ops, transaction.Operation{Path: ref, Data: data, Mode: f.Mode, Before: &current})
			out.Changes = append(out.Changes, Change{ref, current, target})
		}
		managed[ref] = identitymeta.Managed{Baseline: target, Applied: target, Ownership: f.Ownership}
	}
	if i.meta != nil && i.meta.SchemaVersion < 6 {
		for _, ref := range []string{".yss-backend-plugin.json", ".yss-plugin.json", ".yss-execution-scope.yaml"} {
			d, e := safefs.Describe(i.root, ref)
			if e != nil {
				return out, e
			}
			guards[ref] = d
			if d.Type == "file" {
				raw, e := os.ReadFile(filepath.Join(i.root, ref))
				if e != nil {
					return out, e
				}
				if safefs.Digest(raw) != d.Digest {
					return out, domain.Fail("INPUT_DRIFT", "旧绑定记录发生变化")
				}
				artifacts = append(artifacts, transaction.Artifact{ArtifactRecord: transaction.ArtifactRecord{Path: ref, Kind: "baseline", Descriptor: d}, Data: raw})
				old, known := managed[ref]
				if command == "sync" && known && !userOwned(ref, old.Ownership) {
					if !domain.SameDescriptor(d, old.Applied) || !domain.SameDescriptor(old.Applied, old.Baseline) {
						out.Conflicts = append(out.Conflicts, ref)
						continue
					}
					ops = append(ops, transaction.Operation{Path: ref, Delete: true, Before: &d})
					out.Changes = append(out.Changes, Change{ref, d, domain.Descriptor{Type: "missing"}})
				} else {
					out.Preserved = append(out.Preserved, ref)
				}
			}
			if command == "sync" {
				delete(managed, ref)
			}
		}
	}
	if len(out.Conflicts) > 0 {
		out.Status = "conflict"
		return out, domain.Fail("CONFLICT", "目标含用户修改，未写入任何文件: "+strings.Join(out.Conflicts, ", "))
	}
	profile, _ := domain.GetProfile(i.profile)
	meta := identitymeta.Metadata{SchemaVersion: 6, BundleSchemaVersion: 6, Profile: i.profile, ProfileID: profile.ID, ProtocolVersion: domain.ProtocolVersion, CLIVersion: domain.Version, TemplateVersion: b.TemplateVersion, TemplateCommit: b.TemplateCommit, TemplateSourceState: b.SourceState, SnapshotHash: b.SnapshotHash, ManifestHash: b.ManifestHash, BundleHash: b.BundleHash, Managed: managed, Variables: vars, Distribution: map[string]any{"installation": i.kind}}
	provenance := domain.BuildProvenance()
	meta.CLICommit = provenance.Commit
	meta.CLISourceState = provenance.SourceState
	mb, _ := json.Marshal(managed)
	meta.BaselineDigest = safefs.Digest(mb)
	raw := encode(meta)
	after := describe(raw, 0644)
	if !domain.SameDescriptor(after, i.before) {
		ops = append(ops, transaction.Operation{Path: domain.MetadataFile, Data: raw, Mode: 0644, Before: &i.before})
		out.Changes = append(out.Changes, Change{domain.MetadataFile, i.before, after})
	}
	if err := transaction.CheckPending(i.root, ops); err != nil {
		return out, err
	}
	if o.DryRun {
		out.Status = "preview"
		return out, nil
	}
	if len(ops) == 0 {
		out.Status = "unchanged"
		return out, nil
	}
	receipt, err := transaction.ApplyContextWithValidation(ctx, i.root, command, ops, guards, artifacts, nil)
	out.Transaction = &receipt
	out.Status = receipt.Status
	return out, err
}

func Skills(o Options) (any, error) {
	i, err := detect(o, false)
	if err != nil {
		return nil, err
	}
	b, err := bundle.Load(i.profile)
	if err != nil {
		return nil, err
	}
	names := []string{}
	for ref := range b.Files {
		if strings.HasPrefix(ref, ".agents/skills/") && strings.HasSuffix(ref, "/SKILL.md") {
			parts := strings.Split(ref, "/")
			if len(parts) == 4 {
				names = append(names, parts[2])
			}
		}
	}
	sort.Strings(names)
	installed := map[string]bool{}
	for _, name := range names {
		d, e := safefs.Describe(i.root, ".agents/skills/"+name+"/SKILL.md")
		if e != nil {
			return nil, e
		}
		installed[name] = d.Type == "file"
	}
	return map[string]any{"profile": i.profile, "skills": names, "installed": installed}, nil
}

func Doctor(ctx context.Context, o Options) (any, error) {
	i, err := detect(o, false)
	if err != nil {
		return nil, err
	}
	state, err := transaction.Status(i.root)
	if err != nil {
		return nil, err
	}
	differences := []string{}
	for ref, m := range i.meta.Managed {
		d, e := safefs.Describe(i.root, ref)
		if e != nil {
			return nil, e
		}
		if !domain.SameDescriptor(d, m.Applied) {
			differences = append(differences, ref)
		}
	}
	sort.Strings(differences)
	return map[string]any{"root": i.root, "profile": i.profile, "installation": i.kind, "metadataVersion": i.meta.SchemaVersion, "templateVersion": i.meta.TemplateVersion, "source": i.meta.TemplateCommit, "modifiedFiles": differences, "transactions": state}, ctx.Err()
}

func Recover(ctx context.Context, root string, dry bool) (transaction.Result, error) {
	if dry {
		return transaction.Status(root)
	}
	validate := func(s transaction.Summary, paths []string) error {
		switch s.Kind {
		case "init", "attach", "sync", "skills", "program-update":
		default:
			return domain.Fail("RETIRED_TRANSACTION", fmt.Sprintf("旧 %s 事务仅保留现场，不重放", s.Kind))
		}
		for _, ref := range paths {
			if err := protect(root, ref); err != nil {
				return err
			}
		}
		return nil
	}
	state, err := transaction.Status(root)
	if err != nil {
		return state, err
	}
	if len(state.Preparations) > 0 {
		// Identify the preparation family without mutating it before dispatch.
		_, handled, e := transaction.RecoverProgramPreparations(ctx, root, false, validate)
		if e == nil && handled {
			returnValue, _, err := transaction.RecoverProgramPreparations(ctx, root, true, validate)
			return returnValue, err
		}
		// Preparations contain no target writes; the existing recovery seals them.
		for _, profile := range []string{"spec", "backend"} {
			r, handled, e := transaction.RecoverPreparations(ctx, root, profile, true, validate)
			if e == nil && handled {
				return r, nil
			}
		}
		return state, domain.Fail("INTERRUPTED", "准备记录无法安全识别，保留现场")
	}
	return transaction.RecoverContextWithValidator(ctx, root, validate)
}
