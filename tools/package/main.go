// Package builds prereleases or assembles verified native archives, without publishing.
package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/iloveZzz/yss-cli/internal/bundle"
	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/tools/release"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func main() {
	if len(os.Args) == 4 && os.Args[1] == "--native-manifest" {
		must(packageNative(os.Args[2], os.Args[3]))
		return
	}
	if len(os.Args) != 2 {
		panic("用法: go run ./tools/package [--native-manifest <固定原生证据manifest.json>] <仓库外新目录>")
	}
	out, e := filepath.Abs(os.Args[1])
	must(e)
	cwd, e := os.Getwd()
	must(e)
	cwd, e = filepath.EvalSymlinks(cwd)
	must(e)
	// Resolve the existing parent before writing, so an external symlink cannot
	// redirect generated archives or build directories into the engineering tree.
	parent, e := filepath.EvalSymlinks(filepath.Dir(out))
	must(e)
	out = filepath.Join(parent, filepath.Base(out))
	rel, e := filepath.Rel(cwd, out)
	must(e)
	if rel == "." || !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		panic("输出必须在工程外")
	}
	if _, e = os.Stat(out); !os.IsNotExist(e) {
		panic("输出必须是新目录")
	}
	must(os.MkdirAll(out, 0755))
	revision, err := exec.Command("git", "rev-parse", "HEAD").Output()
	must(err)
	commit := strings.TrimSpace(string(revision))
	status, err := exec.Command("git", "status", "--porcelain", "--untracked-files=all").Output()
	must(err)
	sourceState := "committed"
	if len(status) > 0 {
		sourceState = "working-tree"
	}
	sources := map[string]any{}
	for _, profile := range []string{"spec", "design", "backend", "frontend"} {
		inspection, e := bundle.Inspect(profile)
		must(e)
		sources[profile] = inspection
	}
	records := []map[string]any{}
	for _, platform := range []string{"darwin/amd64", "darwin/arm64", "linux/amd64", "linux/arm64", "windows/amd64", "windows/arm64"} {
		p := strings.Split(platform, "/")
		name := "yss"
		if p[0] == "windows" {
			name += ".exe"
		}
		bin := filepath.Join(out, "build-"+p[0]+"-"+p[1], name)
		must(os.MkdirAll(filepath.Dir(bin), 0755))
		c := exec.Command("go", "build", "-trimpath", "-ldflags=-s -w -X github.com/iloveZzz/yss-cli/internal/domain.BuildCommit="+commit+" -X github.com/iloveZzz/yss-cli/internal/domain.BuildSourceState="+sourceState, "-o", bin, "./cmd/yss")
		c.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS="+p[0], "GOARCH="+p[1])
		c.Stdout = os.Stdout
		c.Stderr = os.Stderr
		must(c.Run())
		content, e := os.ReadFile(bin)
		must(e)
		files := map[string][]byte{name: content}
		for _, ref := range []string{"docs/source-lock.json", "docs/compatibility.md", "docs/porting-status.md", "docs/native-governance.md", "docs/cli-retirement.md", "compat/README.md", "README.md"} {
			b, e := os.ReadFile(ref)
			must(e)
			files[ref] = b
		}
		descriptors := map[string]map[string]any{}
		for ref, b := range files {
			h := sha256.Sum256(b)
			mode := uint32(0644)
			if ref == name {
				mode = 0755
			}
			descriptors[ref] = map[string]any{"type": "file", "digest": hex.EncodeToString(h[:]), "mode": mode}
		}
		manifest, _ := json.MarshalIndent(map[string]any{"schemaVersion": 1, "cliVersion": domain.Version, "protocolVersion": domain.ProtocolVersion, "platform": platform, "cgo": false, "sourceState": sourceState, "cliCommit": commit, "bundles": sources, "binarySha256": func() string { h := sha256.Sum256(content); return hex.EncodeToString(h[:]) }(), "stableReady": false, "runtimeVerification": "pending except locally recorded host", "files": descriptors}, "", "  ")
		files["release-manifest.json"] = append(manifest, '\n')
		archive := filepath.Join(out, "yss_"+domain.Version+"_"+p[0]+"_"+p[1])
		if p[0] == "windows" {
			archive += ".zip"
			must(makeZip(archive, files, name))
		} else {
			archive += ".tar.gz"
			must(makeTar(archive, files, name))
		}
		b, e := os.ReadFile(archive)
		must(e)
		h := sha256.Sum256(b)
		records = append(records, map[string]any{"platform": platform, "archive": filepath.Base(archive), "sha256": hex.EncodeToString(h[:]), "bytes": len(b), "compiled": true, "nativeRuntimeVerified": false})
		fmt.Println(platform + ": " + archive)
	}
	b, e := json.MarshalIndent(map[string]any{"schemaVersion": 2, "version": domain.Version, "cliCommit": commit, "sourceState": sourceState, "bundles": sources, "stableReady": false, "pending": []string{"fixed-source-full-template-integration", "six-platform-native-runtime-acceptance"}, "artifacts": records}, "", "  ")
	must(e)
	must(os.WriteFile(filepath.Join(out, "checksums.json"), append(b, '\n'), 0644))
}

func packageNative(input, out string) error {
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	revision, err := exec.Command("git", "rev-parse", "HEAD").Output()
	if err != nil {
		return err
	}
	status, err := exec.Command("git", "status", "--porcelain", "--untracked-files=all").Output()
	if err != nil {
		return err
	}
	if len(status) != 0 {
		return &release.Error{Code: "PROVENANCE", Detail: "stable assembly requires a clean committed source checkout"}
	}
	expected := release.Expected{Identity: release.Identity{CLIVersion: domain.Version, ProtocolVersion: domain.ProtocolVersion, CLICommit: strings.TrimSpace(string(revision)), SourceState: "committed", Bundles: map[string]release.BundleIdentity{}}, Documents: map[string][]byte{}, RepositoryRoot: root}
	for _, profile := range []string{"spec", "design", "backend", "frontend"} {
		b, err := bundle.Inspect(profile)
		if err != nil {
			return err
		}
		expected.Bundles[profile] = release.BundleIdentity{TemplateVersion: b.TemplateVersion, TemplateCommit: b.TemplateCommit, SourceState: b.SourceState, SourceSnapshotHash: b.SnapshotHash, ManifestHash: b.ManifestHash, BundleHash: b.BundleHash}
	}
	for _, ref := range release.Documents {
		b, err := os.ReadFile(ref)
		if err != nil {
			return err
		}
		expected.Documents[ref] = b
	}
	h := sha256.Sum256(expected.Documents["docs/source-lock.json"])
	expected.SourceLockSHA256 = hex.EncodeToString(h[:])
	if err = release.Assemble(input, out, expected); err != nil {
		return err
	}
	current, err := exec.Command("git", "rev-parse", "HEAD").Output()
	if err != nil {
		return err
	}
	status, err = exec.Command("git", "status", "--porcelain", "--untracked-files=all").Output()
	if err != nil {
		return err
	}
	if !bytes.Equal(current, revision) || len(status) != 0 {
		return &release.Error{Code: "INPUT_DRIFT", Detail: "source changed during assembly; do not publish output"}
	}
	fmt.Println(filepath.Join(out, "checksums.json"))
	return nil
}
func makeZip(file string, files map[string][]byte, bin string) error {
	f, e := os.Create(file)
	if e != nil {
		return e
	}
	defer f.Close()
	w := zip.NewWriter(f)
	for _, ref := range sortedFiles(files) {
		b := files[ref]
		h := &zip.FileHeader{Name: ref, Method: zip.Deflate}
		h.SetModTime(time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC))
		h.SetMode(0644)
		if ref == bin {
			h.SetMode(0755)
		}
		d, e := w.CreateHeader(h)
		if e != nil {
			return e
		}
		if _, e = d.Write(b); e != nil {
			return e
		}
	}
	return w.Close()
}
func makeTar(file string, files map[string][]byte, bin string) error {
	f, e := os.Create(file)
	if e != nil {
		return e
	}
	defer f.Close()
	g := gzip.NewWriter(f)
	w := tar.NewWriter(g)
	for _, ref := range sortedFiles(files) {
		b := files[ref]
		mode := int64(0644)
		if ref == bin {
			mode = 0755
		}
		if e = w.WriteHeader(&tar.Header{Name: ref, Mode: mode, Size: int64(len(b)), ModTime: time.Unix(0, 0)}); e != nil {
			return e
		}
		if _, e = w.Write(b); e != nil {
			return e
		}
	}
	if e = w.Close(); e != nil {
		return e
	}
	return g.Close()
}
func must(e error) {
	if e != nil {
		panic(e)
	}
}

func sortedFiles(files map[string][]byte) []string {
	refs := make([]string, 0, len(files))
	for ref := range files {
		refs = append(refs, ref)
	}
	sort.Strings(refs)
	return refs
}
