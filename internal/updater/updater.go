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
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/schema"
	"github.com/iloveZzz/yss-cli/internal/transaction"
)

const maxArchive = 256 << 20
const receiptRef = "installation.json"

type Manifest struct {
	SchemaVersion int                          `json:"schemaVersion"`
	CLIVersion    string                       `json:"cliVersion"`
	Platform      string                       `json:"platform"`
	SourceState   string                       `json:"sourceState,omitempty"`
	CLICommit     string                       `json:"cliCommit,omitempty"`
	Bundles       json.RawMessage              `json:"bundles,omitempty"`
	Source        json.RawMessage              `json:"source,omitempty"`
	BinarySHA256  string                       `json:"binarySha256"`
	Files         map[string]domain.Descriptor `json:"files"`
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
	SchemaVersion int    `json:"schemaVersion"`
	CLIVersion    string `json:"cliVersion"`
	Platform      string `json:"platform"`
	ArchiveDigest string `json:"archiveDigest"`
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

var digestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

func validPayloadRef(ref string) bool {
	if safefs.ValidateRef(ref) != nil || ref == receiptRef || ref == "yss-project.yaml" {
		return false
	}
	for _, part := range strings.Split(ref, "/") {
		if strings.HasPrefix(part, ".") {
			return false
		}
	}
	return true
}

func archive(path, want string) (map[string][]byte, Manifest, error) {
	files := map[string][]byte{}
	var m Manifest
	f, e := openArchive(path)
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
	if len(b) > maxArchive {
		return nil, m, fail("ARTIFACT", "发行包读取期间超过 256 MiB")
	}
	if len(want) != 64 || safefs.Digest(b) != want {
		return nil, m, fail("DIGEST", "发行包 SHA-256 不匹配")
	}
	seen := map[string]bool{}
	total := int64(0)
	add := func(ref string, size int64, r io.Reader) error {
		if !validPayloadRef(ref) || seen[strings.ToLower(ref)] || size < 0 || size > maxArchive || total+size > maxArchive {
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
	if files[fileName()] == nil || files["release-manifest.json"] == nil {
		return nil, m, fail("ARTIFACT", "发行包缺少程序或 manifest")
	}
	if e = parseJSON(files["release-manifest.json"], &m); e != nil {
		return nil, m, e
	}
	if m.SchemaVersion != 2 || m.Platform != runtime.GOOS+"/"+runtime.GOARCH {
		return nil, m, fail("PLATFORM", "发行包 schema 或平台不匹配")
	}
	if len(m.Files) != len(files)-1 || !digestPattern.MatchString(m.BinarySHA256) || m.BinarySHA256 != m.Files[fileName()].Digest {
		return nil, m, fail("ARTIFACT", "发行包文件清单或程序摘要不完整")
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
	installed, _, e := inspectInstallation(root, true, nil)
	if e != nil {
		return nil, e
	}
	if installed != nil {
		order, e := compareVersions(m.CLIVersion, installed.receipt.CLIVersion)
		if e != nil {
			return nil, e
		}
		if order < 0 {
			return nil, fail("VERSION", "程序升级不允许降级")
		}
		for ref, want := range installed.manifest.Files {
			want.Mode = domain.FileMode(want.Mode)
			p.Inputs[ref] = want
		}
		for ref := range m.Files {
			if _, owned := installed.manifest.Files[ref]; !owned && p.Inputs[ref].Type != "missing" {
				return nil, fail("CONFLICT", "拒绝覆盖非受管文件: "+ref)
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
	files[receiptRef] = encode(receipt{2, p.Manifest.CLIVersion, p.Manifest.Platform, p.ArchiveDigest})
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
	for ref, before := range p.Inputs {
		if ref == receiptRef || ref == "yss-project.yaml" {
			continue
		}
		if _, ok := files[ref]; !ok && before.Type == "file" {
			before := before
			ops = append(ops, transaction.Operation{Path: ref, Delete: true, Before: &before})
		}
	}
	sort.Slice(ops, func(i, j int) bool { return ops[i].Path < ops[j].Path })
	return transaction.ApplyContextWithGuards(ctx, p.ToolRoot, "program-update", ops, p.Inputs)
}
func Status(root string) (any, error) {
	root, e := toolRootPath(root)
	if e != nil {
		return nil, e
	}
	_, report, e := inspectInstallation(root, false, nil)
	return report, e
}
