// Package updater installs a digest-pinned native archive in an explicit tool
// root. Program installation never reads or migrates a project Profile.
package updater

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"

	"github.com/iloveZzz/yss-cli/internal/bundle"
	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/schema"
	"github.com/iloveZzz/yss-cli/internal/transaction"
)

const maxArchive = 256 << 20
const receiptRef = "installation.json"

type Manifest struct {
	SchemaVersion       int                           `json:"schemaVersion"`
	CLIVersion          string                        `json:"cliVersion"`
	ProtocolVersion     int                           `json:"protocolVersion"`
	Platform            string                        `json:"platform"`
	CGO                 bool                          `json:"cgo"`
	SourceState         string                        `json:"sourceState"`
	CLICommit           string                        `json:"cliCommit,omitempty"`
	Bundles             map[string]*bundle.Inspection `json:"bundles,omitempty"`
	BinarySHA256        string                        `json:"binarySha256,omitempty"`
	StableReady         bool                          `json:"stableReady"`
	RuntimeVerification string                        `json:"runtimeVerification"`
	Files               map[string]domain.Descriptor  `json:"files"`
}
type Plan struct {
	SchemaVersion int                          `json:"schemaVersion"`
	Command       string                       `json:"command"`
	ToolRoot      string                       `json:"toolRoot"`
	Archive       string                       `json:"archive"`
	ArchiveDigest string                       `json:"archiveDigest"`
	Manifest      Manifest                     `json:"manifest"`
	Inputs        map[string]domain.Descriptor `json:"inputs"`
	Digest        string                       `json:"digest"`
}
type receipt struct {
	SchemaVersion   int    `json:"schemaVersion"`
	CLIVersion      string `json:"cliVersion"`
	ProtocolVersion int    `json:"protocolVersion"`
	Platform        string `json:"platform"`
	ArchiveDigest   string `json:"archiveDigest"`
	SourceState     string `json:"sourceState"`
	StableReady     bool   `json:"stableReady"`
}

func fail(code, message string) error { return domain.Fail(code, message) }
func encode(v any) []byte             { b, _ := json.MarshalIndent(v, "", "  "); return append(b, '\n') }
func digest(p *Plan) string {
	copy := *p
	copy.Digest = ""
	b, _ := json.Marshal(copy)
	return safefs.Digest(b)
}
func parseJSON(b []byte, v any) error {
	if !json.Valid(b) {
		return fail("ARTIFACT", "必须是单个 JSON 文档")
	}
	if _, e := schema.Parse(b); e != nil {
		return e
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	return d.Decode(v)
}
func fileName() string {
	if runtime.GOOS == "windows" {
		return "yss.exe"
	}
	return "yss"
}
func expectedFiles() map[string]bool {
	return map[string]bool{fileName(): true, "README.md": true, "docs/source-lock.json": true, "docs/compatibility.md": true, "docs/porting-status.md": true, "docs/native-governance.md": true, "docs/cli-retirement.md": true, "compat/README.md": true, "release-manifest.json": true}
}

var commitPattern = regexp.MustCompile(`^[a-f0-9]{40}$`)
var digestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

// Early pinned alpha archives omit provenance. New archives carry the complete
// group, bound to their binary descriptor and their included source lock.
func validateProvenance(m Manifest, sourceLock []byte) error {
	if m.CLICommit == "" && m.Bundles == nil && m.BinarySHA256 == "" {
		var marker struct {
			SchemaVersion int `json:"schemaVersion"`
		}
		if e := json.Unmarshal(sourceLock, &marker); e == nil && marker.SchemaVersion == 2 {
			return fail("ARTIFACT", "v2 来源锁缺少发行包来源字段")
		}
		return nil
	}
	if !commitPattern.MatchString(m.CLICommit) || !digestPattern.MatchString(m.BinarySHA256) || m.BinarySHA256 != m.Files[fileName()].Digest || len(m.Bundles) != 4 {
		return fail("ARTIFACT", "发行包程序来源、二进制摘要或 Profile 集不完整")
	}
	var lock bundle.SourceLock
	if e := parseJSON(sourceLock, &lock); e != nil {
		return e
	}
	if lock.SchemaVersion != 2 || len(lock.Profiles) != 4 || lock.Producer.Version == "" || lock.Producer.SourceState != "committed" || !commitPattern.MatchString(lock.Producer.Commit) {
		return fail("ARTIFACT", "发行包缺少固定来源锁")
	}
	for _, profile := range []string{"spec", "design", "backend", "frontend"} {
		i := m.Bundles[profile]
		s, ok := lock.Profiles[profile]
		version := s.TemplateVersion
		if version == "" {
			version = "git:" + s.Commit
		}
		policyKind := "committed"
		if strings.HasPrefix(s.PolicyPath, "builtin:") {
			policyKind = "bootstrap"
		}
		if !ok || i == nil || i.SchemaVersion != 2 || i.Profile != profile || s.Profile != profile || i.SourceState != "committed" || !commitPattern.MatchString(i.TemplateCommit) || i.TemplateCommit != s.Commit || i.TemplateVersion != version || i.Producer != lock.Producer || i.Legacy != s.Legacy || i.LegacyVersion != s.Legacy.Version || i.LegacyCLICommit != s.Legacy.CLICommit || i.SourcePolicy.Kind != policyKind || i.SourcePolicy.Path == "" || i.SourcePolicy.Path != s.PolicyPath || i.SourcePolicy.Digest != s.PolicyHash {
			return fail("ARTIFACT", "发行包 Profile 来源与锁不一致: "+profile)
		}
		for _, hash := range []string{i.SnapshotHash, i.ManifestHash, i.BundleHash, i.SourcePolicy.Digest} {
			if !digestPattern.MatchString(hash) {
				return fail("ARTIFACT", "发行包 Profile 来源摘要不完整: "+profile)
			}
		}
	}
	return nil
}
func archive(path, want string) (map[string][]byte, Manifest, error) {
	files := map[string][]byte{}
	var m Manifest
	f, e := os.Open(path)
	if e != nil {
		return nil, m, e
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil {
		return nil, m, e
	}
	if !info.Mode().IsRegular() || info.Size() > maxArchive {
		return nil, m, fail("ARTIFACT", "发行包不是普通文件或超过 256 MiB")
	}
	b, e := io.ReadAll(io.LimitReader(f, maxArchive+1))
	if e != nil {
		return nil, m, e
	}
	if len(want) != 64 || safefs.Digest(b) != want {
		return nil, m, fail("DIGEST", "发行包 SHA-256 不匹配")
	}
	allowed := expectedFiles()
	seen := map[string]bool{}
	total := int64(0)
	add := func(ref string, size int64, r io.Reader) error {
		if !allowed[ref] || seen[strings.ToLower(ref)] || size < 0 || size > maxArchive || total+size > maxArchive {
			return fail("ARTIFACT", "发行包含未知、重复或超限路径: "+ref)
		}
		seen[strings.ToLower(ref)] = true
		total += size
		data, e := io.ReadAll(io.LimitReader(r, size+1))
		if e != nil {
			return e
		}
		if int64(len(data)) != size {
			return fail("ARTIFACT", "发行包文件长度不匹配: "+ref)
		}
		files[ref] = data
		return nil
	}
	if strings.HasSuffix(strings.ToLower(path), ".zip") {
		z, e := zip.NewReader(bytes.NewReader(b), int64(len(b)))
		if e != nil {
			return nil, m, e
		}
		for _, f := range z.File {
			if f.Flags&1 != 0 || !f.Mode().IsRegular() || f.UncompressedSize64 > maxArchive {
				return nil, m, fail("ARTIFACT", "ZIP含加密、链接、目录或超限文件")
			}
			r, e := f.Open()
			if e != nil {
				return nil, m, e
			}
			e = add(f.Name, int64(f.UncompressedSize64), r)
			ce := r.Close()
			if e != nil {
				return nil, m, e
			}
			if ce != nil {
				return nil, m, ce
			}
		}
	} else if strings.HasSuffix(strings.ToLower(path), ".tar.gz") {
		compressed := bytes.NewReader(b)
		g, e := gzip.NewReader(compressed)
		if e != nil {
			return nil, m, e
		}
		defer g.Close()
		g.Multistream(false)
		t := tar.NewReader(g)
		for {
			h, e := t.Next()
			if e == io.EOF {
				break
			}
			if e != nil {
				return nil, m, e
			}
			if h.Typeflag != tar.TypeReg || h.Linkname != "" {
				return nil, m, fail("ARTIFACT", "TAR含非普通文件")
			}
			if e = add(h.Name, h.Size, t); e != nil {
				return nil, m, e
			}
		}
		// Validate checksum without silently accepting another tar/gzip stream.
		remaining, e := io.ReadAll(io.LimitReader(g, maxArchive+1))
		if e != nil {
			return nil, m, e
		}
		if len(remaining) > maxArchive || len(bytes.Trim(remaining, "\x00")) != 0 || compressed.Len() != 0 {
			return nil, m, fail("ARTIFACT", "发行包含未登记的尾随数据或多个 gzip 流")
		}
	} else {
		return nil, m, fail("ARTIFACT", "仅接受 .tar.gz 或 .zip 原生发行包")
	}
	// Schema v1 core files remain required; registered newer documentation files
	// are registered optional additions so pinned early alpha archives remain readable.
	for _, ref := range []string{fileName(), "README.md", "docs/source-lock.json", "docs/compatibility.md", "release-manifest.json"} {
		if _, ok := files[ref]; !ok {
			return nil, m, fail("ARTIFACT", "发行包缺少: "+ref)
		}
	}
	if e = parseJSON(files["release-manifest.json"], &m); e != nil {
		return nil, m, e
	}
	if m.SchemaVersion != 1 || m.ProtocolVersion != domain.ProtocolVersion || m.CGO || m.Platform != runtime.GOOS+"/"+runtime.GOARCH {
		return nil, m, fail("PLATFORM", "发行包 schema、协议、cgo 或目标平台不匹配")
	}
	if m.CLIVersion == "" || m.SourceState != "committed" && m.SourceState != "working-tree" || len(m.Files) != len(files)-1 {
		return nil, m, fail("ARTIFACT", "发行包来源或文件清单不完整")
	}
	if m.StableReady && (m.SourceState != "committed" || m.RuntimeVerification != "passed") {
		return nil, m, fail("ARTIFACT", "稳定发行标记缺少固定提交或实际运行验证")
	}
	if _, e = versionParts(m.CLIVersion); e != nil {
		return nil, m, e
	}
	for ref, d := range m.Files {
		data, ok := files[ref]
		if !ok || ref == "release-manifest.json" || d.Type != "file" || d.Digest != safefs.Digest(data) || d.Mode != 0644 && d.Mode != 0755 {
			return nil, m, fail("DIGEST", "发行包文件摘要或权限不合法: "+ref)
		}
	}
	if m.Files[fileName()].Mode != 0755 {
		return nil, m, fail("ARTIFACT", "原生程序必须标记为可执行文件")
	}
	if e = validateProvenance(m, files["docs/source-lock.json"]); e != nil {
		return nil, m, e
	}
	return files, m, nil
}

func Build(toolRoot, archivePath, sha string) (*Plan, error) {
	root, e := toolRootPath(toolRoot)
	if e != nil {
		return nil, e
	}
	path, e := filepath.Abs(archivePath)
	if e != nil {
		return nil, e
	}
	files, m, e := archive(path, sha)
	if e != nil {
		return nil, e
	}
	p := &Plan{SchemaVersion: 1, Command: "update", ToolRoot: root, Archive: path, ArchiveDigest: sha, Manifest: m, Inputs: map[string]domain.Descriptor{}}
	for ref := range files {
		d, e := safefs.Describe(root, ref)
		if e != nil {
			return nil, e
		}
		p.Inputs[ref] = d
	}
	for _, ref := range []string{receiptRef, "yss-project.yaml"} {
		d, e := safefs.Describe(root, ref)
		if e != nil {
			return nil, e
		}
		p.Inputs[ref] = d
	}
	if p.Inputs[receiptRef].Type == "file" {
		data, e := os.ReadFile(filepath.Join(root, receiptRef))
		if e != nil {
			return nil, e
		}
		var current receipt
		if e = parseJSON(data, &current); e != nil {
			return nil, e
		}
		if current.SchemaVersion != 1 || current.Platform != m.Platform || current.ProtocolVersion != domain.ProtocolVersion {
			return nil, fail("ARTIFACT", "未知程序安装合同")
		}
		order, e := compareVersions(m.CLIVersion, current.CLIVersion)
		if e != nil {
			return nil, e
		}
		if order < 0 {
			return nil, fail("VERSION", "程序升级不允许降级；恢复须使用归档的整体回退")
		}
		oldData, e := os.ReadFile(filepath.Join(root, "release-manifest.json"))
		if e != nil {
			return nil, e
		}
		var old Manifest
		if e = parseJSON(oldData, &old); e != nil {
			return nil, e
		}
		if old.SchemaVersion != 1 || old.ProtocolVersion != domain.ProtocolVersion || old.CGO || old.SourceState != current.SourceState || old.StableReady != current.StableReady || old.Platform != current.Platform || old.CLIVersion != current.CLIVersion || len(old.Files) > len(m.Files) {
			return nil, fail("ARTIFACT", "现有程序来源清单与安装记录不匹配")
		}
		for _, ref := range []string{fileName(), "README.md", "docs/source-lock.json", "docs/compatibility.md"} {
			if _, ok := old.Files[ref]; !ok {
				return nil, fail("ARTIFACT", "现有来源清单缺少核心文件: "+ref)
			}
		}
		for ref, want := range old.Files {
			if !expectedFiles()[ref] || ref == "release-manifest.json" || want.Type != "file" || want.Mode != 0644 && want.Mode != 0755 || len(want.Digest) != 64 || strings.Trim(want.Digest, "0123456789abcdef") != "" || ref == fileName() && want.Mode != 0755 {
				return nil, fail("ARTIFACT", "现有来源清单包含未知路径或非法描述: "+ref)
			}
			if _, ok := m.Files[ref]; !ok {
				return nil, fail("ARTIFACT", "程序升级未授权移除旧受管文件: "+ref)
			}
			want.Mode = domain.FileMode(want.Mode)
			got, ok := p.Inputs[ref]
			if !ok || got != want {
				return nil, fail("CONFLICT", "现有程序文件发生用户修改: "+ref)
			}
		}
		for ref := range m.Files {
			if _, owned := old.Files[ref]; !owned && p.Inputs[ref].Type != "missing" {
				return nil, fail("CONFLICT", "新增程序文件拒绝覆盖用户内容: "+ref)
			}
		}
	} else {
		for ref, d := range p.Inputs {
			if d.Type != "missing" {
				return nil, fail("CONFLICT", "首次安装拒绝覆盖已有文件: "+ref)
			}
		}
	}
	p.Digest = digest(p)
	return p, nil
}
func ReadPlan(file string) (*Plan, error) {
	b, e := os.ReadFile(file)
	if e != nil {
		return nil, e
	}
	var p Plan
	if e = parseJSON(b, &p); e != nil {
		return nil, e
	}
	if p.SchemaVersion != 1 || p.Command != "update" || p.Digest != digest(&p) {
		return nil, fail("PLAN", "程序安装计划摘要或版本不合法")
	}
	return &p, nil
}
func SavePlan(p *Plan, file string) error {
	path, e := filepath.Abs(file)
	if e != nil {
		return e
	}
	parent, e := filepath.EvalSymlinks(filepath.Dir(path))
	if e != nil {
		return e
	}
	path = filepath.Join(parent, filepath.Base(path))
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if strings.EqualFold(part, ".git") {
			return fail("PROTECTED", "计划输出禁止 Git 内部目录")
		}
	}
	rel, e := filepath.Rel(p.ToolRoot, path)
	if e != nil {
		return e
	}
	if rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fail("PROTECTED", "程序升级计划须保存到工具目录外")
	}
	f, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	defer f.Close()
	if _, e = f.Write(encode(p)); e != nil {
		return e
	}
	return f.Sync()
}
func Apply(ctx context.Context, p *Plan) (transaction.Result, error) {
	if p.Digest != digest(p) {
		return transaction.Result{}, fail("PLAN", "程序安装计划摘要不合法")
	}
	rebuilt, e := Build(p.ToolRoot, p.Archive, p.ArchiveDigest)
	if e != nil {
		return transaction.Result{}, e
	}
	if rebuilt.Digest != p.Digest {
		return transaction.Result{}, fail("INPUT_DRIFT", "程序安装输入或现有工具发生变化")
	}
	files, _, e := archive(p.Archive, p.ArchiveDigest)
	if e != nil {
		return transaction.Result{}, e
	}
	files[receiptRef] = encode(receipt{1, p.Manifest.CLIVersion, p.Manifest.ProtocolVersion, p.Manifest.Platform, p.ArchiveDigest, p.Manifest.SourceState, p.Manifest.StableReady})
	refs := []string{}
	for ref := range files {
		refs = append(refs, ref)
	}
	sort.Strings(refs)
	ops := []transaction.Operation{}
	for _, ref := range refs {
		before := p.Inputs[ref]
		mode := uint32(0644)
		if ref != receiptRef {
			mode = domain.FileMode(p.Manifest.Files[ref].Mode)
			if ref == "release-manifest.json" {
				mode = 0644
			}
		}
		after := domain.Descriptor{Type: "file", Digest: safefs.Digest(files[ref]), Mode: mode}
		if before == after {
			continue
		}
		ops = append(ops, transaction.Operation{Path: ref, Data: files[ref], Mode: mode, Before: &before})
	}
	return transaction.ApplyContextWithGuards(ctx, p.ToolRoot, "program-update", ops, p.Inputs)
}
func Status(root string) (any, error) {
	root, e := toolRootPath(root)
	if e != nil {
		return nil, e
	}
	txn, e := transaction.Status(root)
	if e != nil {
		return nil, e
	}
	pending := map[string]bool{}
	for _, id := range txn.Pending {
		pending[id] = true
	}
	foreignKinds := map[string]bool{}
	matching := 0
	for _, summary := range txn.Transactions {
		if !pending[summary.TransactionID] {
			continue
		}
		if summary.Kind == "program-update" {
			matching++
		} else {
			foreignKinds[summary.Kind] = true
		}
	}
	blockedKinds := []string{}
	for kind := range foreignKinds {
		blockedKinds = append(blockedKinds, kind)
	}
	sort.Strings(blockedKinds)
	// These are read-only candidate facts, not a promise that current bytes and
	// archive scope will pass the locked recovery preflight.
	out := map[string]any{"installed": false, "currentProgramVersion": domain.Version, "projectMigrationPerformed": false, "transaction": txn, "recoverable": len(txn.Pending) == 1 && matching == 1, "recoveryRequired": len(txn.Pending) > 0, "recoveryKindMatches": len(txn.Pending) == 1 && matching == 1, "recoveryPreflightPerformed": false, "recoveryBlockedKinds": blockedKinds}
	path, e := safefs.Path(root, receiptRef)
	if e != nil {
		return nil, e
	}
	b, e := os.ReadFile(path)
	if os.IsNotExist(e) {
		return out, nil
	}
	if e != nil {
		return nil, e
	}
	var r receipt
	if e = parseJSON(b, &r); e != nil {
		return nil, e
	}
	if r.SchemaVersion != 1 || r.ProtocolVersion != domain.ProtocolVersion || r.Platform != runtime.GOOS+"/"+runtime.GOARCH || r.SourceState != "committed" && r.SourceState != "working-tree" || r.StableReady && r.SourceState != "committed" {
		return nil, fail("ARTIFACT", "未知程序安装合同")
	}
	if _, e = versionParts(r.CLIVersion); e != nil {
		return nil, e
	}
	out["installed"] = true
	out["installation"] = r
	return out, nil
}
