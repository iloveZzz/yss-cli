package scaffold

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/iloveZzz/yss-cli/internal/bundle"
	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/schema"
)

const planKind = "scaffold-plan"

// RenderContext captures the environment once; saved plans reuse these bytes.
type RenderContext struct {
	Author      string `json:"author"`
	Date        string `json:"date"`
	GeneratedAt string `json:"generatedAt"`
}

func captureRenderContext() *RenderContext {
	author := os.Getenv("USER")
	if author == "" {
		author = "yss-team"
	}
	now := time.Now().UTC()
	return &RenderContext{Author: author, Date: now.Format("2006-01-02"), GeneratedAt: now.Format("2006-01-02T15:04:05.000Z")}
}

// PermissionRule distinguishes chmod from the platform's umask-filtered default.
type PermissionRule struct {
	Rule string `json:"rule"`
	Mode uint32 `json:"mode,omitempty"`
}

type PlannedFile struct {
	Path       string         `json:"path"`
	Type       string         `json:"type"`
	SHA256     string         `json:"sha256"`
	Permission PermissionRule `json:"permission"`
}

type PlannedDirectory struct {
	Path       string         `json:"path"`
	Type       string         `json:"type"`
	Permission PermissionRule `json:"permission"`
}

type CLISource struct {
	domain.Provenance
	ExecutableSHA256 string `json:"executableSha256"`
}

type BundleBinding struct {
	Profile         string `json:"profile"`
	SchemaVersion   int    `json:"schemaVersion"`
	BundleHash      string `json:"bundleHash"`
	TemplateVersion string `json:"templateVersion"`
	TemplateCommit  string `json:"templateCommit"`
	SourceState     string `json:"sourceState"`
	SnapshotHash    string `json:"snapshotHash"`
	ManifestHash    string `json:"manifestHash"`
}

// Plan contains descriptors only; application regenerates all bytes from the
// same executable, fixed Bundle, inputs and rendering context.
type Plan struct {
	Kind            string             `json:"kind"`
	SchemaVersion   int                `json:"schemaVersion"`
	ProtocolVersion int                `json:"protocolVersion"`
	CLI             CLISource          `json:"cli"`
	Bundle          BundleBinding      `json:"bundle"`
	Options         Options            `json:"options"`
	ProjectRoot     string             `json:"projectRoot"`
	RenderContext   RenderContext      `json:"renderContext"`
	Files           []PlannedFile      `json:"files"`
	Dirs            []PlannedDirectory `json:"dirs"`
	Digest          string             `json:"digest"`
	Blockers        []string           `json:"blockers"`
	ReadyToApply    bool               `json:"readyToApply"`
}

// Public is also the saved representation: no template or generated file text.
func (p *Plan) Public() any { return p }

func currentCLISource() (CLISource, error) {
	path, err := os.Executable()
	if err != nil {
		return CLISource{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return CLISource{}, err
	}
	return CLISource{Provenance: domain.BuildProvenance(), ExecutableSHA256: safefs.Digest(data)}, nil
}

func planSource() (Source, BundleBinding, error) {
	b, err := bundle.Load("spec")
	if err != nil {
		return nil, BundleBinding{}, err
	}
	return newBundleSource(b.Files), BundleBinding{Profile: b.Profile, SchemaVersion: b.SchemaVersion,
		BundleHash: bundle.Hash(b), TemplateVersion: b.TemplateVersion, TemplateCommit: b.TemplateCommit,
		SourceState: b.SourceState, SnapshotHash: b.SnapshotHash, ManifestHash: b.ManifestHash}, nil
}

// BuildPlan renders only in memory; even a missing output parent stays absent.
func BuildPlan(ctx context.Context, o Options) (*Plan, error) {
	return buildPlan(ctx, o, captureRenderContext())
}

func buildPlan(ctx context.Context, o Options, render *RenderContext) (*Plan, error) {
	if ctx == nil {
		return nil, domain.Fail("ARGUMENT", "取消上下文不可为空")
	}
	if err := ctx.Err(); err != nil {
		return nil, domain.Wrap("CANCELLED", err)
	}
	if o.Source != nil || o.PlatformCatalog != nil || o.AllowCandidate {
		return nil, domain.Fail("CAPABILITY", "保存计划只支持当前 CLI 的固定 Bundle，不接受维护期来源覆盖")
	}
	if err := normalizeOptions(&o); err != nil {
		return nil, err
	}
	if !o.hasContract() {
		o.Standalone = true
		example := o.example()
		o.WithExample = &example
	}
	o.renderContext = render
	src, binding, err := planSource()
	if err != nil {
		return nil, err
	}
	rendered, err := renderProject(ctx, src, &o)
	if err != nil {
		return nil, err
	}
	cli, err := currentCLISource()
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, domain.Wrap("CANCELLED", err)
	}
	p := describePlan(o, *render, cli, binding, rendered.tree)
	if err := validatePlan(p); err != nil {
		return nil, err
	}
	return p, nil
}

func describePlan(o Options, render RenderContext, cli CLISource, binding BundleBinding, tree *Tree) *Plan {
	o.renderContext = nil
	p := &Plan{Kind: planKind, SchemaVersion: 1, ProtocolVersion: domain.ProtocolVersion, CLI: cli, Bundle: binding,
		Options: o, ProjectRoot: filepath.Join(o.OutputDir, o.ProjectName), RenderContext: render,
		Files: []PlannedFile{}, Dirs: []PlannedDirectory{}, Blockers: []string{}, ReadyToApply: true}
	for _, path := range tree.Paths() {
		file := tree.files[path]
		permission := PermissionRule{Rule: "platform-default"}
		if file.fixed {
			permission = PermissionRule{Rule: "fixed", Mode: uint32(file.mode.Perm())}
		}
		p.Files = append(p.Files, PlannedFile{Path: path, Type: "file", SHA256: safefs.Digest(file.data), Permission: permission})
	}
	dirs := tree.directoryModes()
	for _, path := range sortedKeys(dirs) {
		permission := PermissionRule{Rule: "platform-default"}
		if mode := dirs[path]; mode != 0 {
			permission = PermissionRule{Rule: "fixed", Mode: uint32(mode.Perm())}
		}
		p.Dirs = append(p.Dirs, PlannedDirectory{Path: path, Type: "directory", Permission: permission})
	}
	p.Digest = scaffoldPlanDigest(p)
	return p
}

func scaffoldPlanDigest(p *Plan) string {
	copy := *p
	copy.Digest = ""
	data, _ := json.Marshal(copy)
	return safefs.Digest(data)
}

func validPlanPermission(p PermissionRule) bool {
	return p.Rule == "platform-default" && p.Mode == 0 || p.Rule == "fixed" && p.Mode <= 0o777
}

func validSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, c := range value {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func validatePlan(p *Plan) error {
	if p == nil {
		return domain.Fail("ARGUMENT", "缺少脚手架保存计划")
	}
	if p.Kind != planKind || p.SchemaVersion != 1 || p.ProtocolVersion != domain.ProtocolVersion {
		return domain.Fail("PLAN_VERSION", "脚手架计划类型或版本不支持；请用当前 CLI 重新规划")
	}
	if !validSHA256(p.Digest) || p.Digest != scaffoldPlanDigest(p) {
		return domain.Fail("INPUT_DRIFT", "脚手架保存计划摘要不合法")
	}
	if p.Options.Source != nil || p.Options.PlatformCatalog != nil || p.Options.AllowCandidate || p.Options.renderContext != nil {
		return domain.Fail("ARGUMENT", "脚手架保存计划不得覆盖固定来源或渲染上下文")
	}
	if p.Options.WithExample == nil && !p.Options.hasContract() || p.Options.Standalone != !p.Options.hasContract() {
		return domain.Fail("ARGUMENT", "脚手架计划输入尚未规范化")
	}
	var optionErr error
	switch p.Options.Architecture {
	case "ddd":
		optionErr = validateDDDOptions(&p.Options)
	case "mvc":
		optionErr = validateMVCOptions(&p.Options)
	default:
		return domain.Fail("ARGUMENT", "脚手架计划架构不支持")
	}
	if optionErr != nil {
		return domain.Wrap("ARGUMENT", optionErr)
	}
	if !filepath.IsAbs(p.Options.OutputDir) || filepath.Clean(p.Options.OutputDir) != p.Options.OutputDir ||
		!standaloneName.MatchString(p.Options.ProjectName) || p.ProjectRoot != filepath.Join(p.Options.OutputDir, p.Options.ProjectName) ||
		p.Options.ContractFile != "" && (!filepath.IsAbs(p.Options.ContractFile) || filepath.Clean(p.Options.ContractFile) != p.Options.ContractFile) {
		return domain.Fail("ARGUMENT", "脚手架计划目标或输入路径不合法")
	}
	generatedAt, err := time.Parse("2006-01-02T15:04:05.000Z", p.RenderContext.GeneratedAt)
	if err != nil || generatedAt.Format("2006-01-02") != p.RenderContext.Date || p.RenderContext.Author == "" {
		return domain.Fail("ARGUMENT", "脚手架计划渲染上下文不合法")
	}
	if p.CLI.Version == "" || !validSHA256(p.CLI.ExecutableSHA256) || p.Bundle.Profile != "spec" || p.Bundle.BundleHash == "" ||
		len(p.Files) == 0 || p.Files == nil || p.Dirs == nil || p.Blockers == nil {
		return domain.Fail("ARGUMENT", "脚手架计划缺少来源或完整文件描述")
	}
	seen := map[string]string{}
	var paths safefs.PathSet
	last, hasManifest := "", false
	for _, file := range p.Files {
		if safefs.ValidateRef(file.Path) != nil || file.Path <= last || file.Type != "file" || !validSHA256(file.SHA256) || !validPlanPermission(file.Permission) {
			return domain.Fail("ARGUMENT", "脚手架计划文件描述不合法: "+file.Path)
		}
		if err := paths.Add(file.Path); err != nil {
			return err
		}
		seen[file.Path] = "file"
		last = file.Path
		hasManifest = hasManifest || file.Path == manifestRef
	}
	last = ""
	for _, dir := range p.Dirs {
		if safefs.ValidateRef(dir.Path) != nil || dir.Path <= last || dir.Type != "directory" || seen[dir.Path] != "" || !validPlanPermission(dir.Permission) {
			return domain.Fail("ARGUMENT", "脚手架计划目录描述不合法: "+dir.Path)
		}
		if err := paths.Add(dir.Path); err != nil {
			return err
		}
		seen[dir.Path] = "directory"
		last = dir.Path
	}
	for path := range seen {
		for dir := parent(path); dir != ""; dir = parent(dir) {
			if seen[dir] != "directory" {
				return domain.Fail("ARGUMENT", "脚手架计划缺少父目录描述: "+dir)
			}
		}
	}
	if !hasManifest || p.ReadyToApply != (len(p.Blockers) == 0) {
		return domain.Fail("ARGUMENT", "脚手架计划缺少生成清单或阻断状态不一致")
	}
	return nil
}

// SavePlan only creates a new file outside the future project and Git internals.
func SavePlan(p *Plan, file string) error {
	if err := validatePlan(p); err != nil {
		return err
	}
	file, err := filepath.Abs(file)
	if err != nil {
		return err
	}
	if strings.EqualFold(filepath.VolumeName(p.ProjectRoot), filepath.VolumeName(file)) {
		rel, err := filepath.Rel(p.ProjectRoot, file)
		if err != nil {
			return err
		}
		if rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return domain.Fail("ARGUMENT", "脚手架计划须保存到待生成项目外的新文件")
		}
	}
	for _, part := range strings.Split(filepath.ToSlash(file), "/") {
		if strings.EqualFold(part, ".git") {
			return domain.Fail("ARGUMENT", "计划输出不得位于 Git 内部目录")
		}
	}
	if _, err = safefs.Path(filepath.Dir(file), filepath.Base(file)); err != nil {
		return err
	}
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.OpenFile(file, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err = f.Write(append(data, '\n')); err != nil {
		return err
	}
	return f.Sync()
}

func ReadPlan(file string) (*Plan, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	if !json.Valid(data) {
		return nil, domain.Fail("ARGUMENT", "脚手架计划必须是单个 JSON 文档")
	}
	if _, err = schema.Parse(data); err != nil {
		return nil, err
	}
	var p Plan
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err = d.Decode(&p); err != nil {
		return nil, err
	}
	if err = validatePlan(&p); err != nil {
		return nil, err
	}
	return &p, nil
}
