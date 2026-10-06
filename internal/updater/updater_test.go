package updater

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/bundle"
	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/transaction"
)

func root(t *testing.T) string {
	t.Helper()
	p, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	return p
}
func fixture(t *testing.T, files map[string][]byte, platform string) (string, string) {
	return fixtureManifest(t, files, platform, nil)
}
func fixtureManifest(t *testing.T, files map[string][]byte, platform string, customize func(*Manifest)) (string, string) {
	t.Helper()
	if files == nil {
		files = map[string][]byte{fileName(): []byte("native-binary"), "README.md": []byte("readme"), "docs/source-lock.json": []byte(`{}`), "docs/compatibility.md": []byte("alpha")}
	}
	m := Manifest{SchemaVersion: 1, CLIVersion: domain.Version, ProtocolVersion: 1, Platform: platform, SourceState: "working-tree", RuntimeVerification: "pending", Files: map[string]domain.Descriptor{}}
	for ref, b := range files {
		mode := uint32(0644)
		if ref == fileName() {
			mode = 0755
		}
		m.Files[ref] = domain.Descriptor{Type: "file", Digest: safefs.Digest(b), Mode: mode}
	}
	if customize != nil {
		customize(&m)
	}
	data, _ := json.Marshal(m)
	files["release-manifest.json"] = data
	var buf bytes.Buffer
	g := gzip.NewWriter(&buf)
	w := tar.NewWriter(g)
	for ref, b := range files {
		if e := w.WriteHeader(&tar.Header{Name: ref, Typeflag: tar.TypeReg, Mode: 0644, Size: int64(len(b))}); e != nil {
			t.Fatal(e)
		}
		if _, e := w.Write(b); e != nil {
			t.Fatal(e)
		}
	}
	if e := w.Close(); e != nil {
		t.Fatal(e)
	}
	if e := g.Close(); e != nil {
		t.Fatal(e)
	}
	file := filepath.Join(root(t), "bundle.tar.gz")
	if e := os.WriteFile(file, buf.Bytes(), 0644); e != nil {
		t.Fatal(e)
	}
	return file, safefs.Digest(buf.Bytes())
}
func TestProgramInstallationIsSeparateDigestBoundIdempotentAndRecoverable(t *testing.T) {
	file, sha := fixture(t, nil, runtime.GOOS+"/"+runtime.GOARCH)
	tool := filepath.Join(root(t), "tools")
	p, e := Build(tool, file, sha)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(tool); !os.IsNotExist(e) {
		t.Fatal("plan created tool root")
	}
	if _, e = Apply(context.Background(), p); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(filepath.Join(tool, "yss-project.yaml")); !os.IsNotExist(e) {
		t.Fatal("program install created project identity")
	}
	next, e := Build(tool, file, sha)
	if e != nil {
		t.Fatal(e)
	}
	out, e := Apply(context.Background(), next)
	if e != nil || out.Status != "unchanged" {
		t.Fatalf("repeat install: %+v %v", out, e)
	}
	if _, e = transaction.Rollback(tool); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(filepath.Join(tool, receiptRef)); !os.IsNotExist(e) {
		t.Fatal("whole program rollback retained installation metadata")
	}
}
func TestProgramInstallRejectsHashPlatformUnsafeArchiveAndExistingCustomization(t *testing.T) {
	file, sha := fixture(t, nil, runtime.GOOS+"/"+runtime.GOARCH)
	tool := filepath.Join(root(t), "tools")
	if _, e := Build(tool, file, "incorrect"); e == nil {
		t.Fatal("unverified archive accepted")
	}
	other, otherSHA := fixture(t, nil, "unsupported/arch")
	if _, e := Build(tool, other, otherSHA); e == nil {
		t.Fatal("wrong platform accepted")
	}
	unsafe, unsafeSHA := fixture(t, map[string][]byte{"../escape": []byte("bad")}, runtime.GOOS+"/"+runtime.GOARCH)
	if _, e := Build(tool, unsafe, unsafeSHA); e == nil {
		t.Fatal("unsafe archive accepted")
	}
	if e := os.MkdirAll(tool, 0755); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(tool, "README.md"), []byte("custom"), 0644); e != nil {
		t.Fatal(e)
	}
	if _, e := Build(tool, file, sha); e == nil {
		t.Fatal("first installation overwrote customization")
	}
	if e := os.Remove(filepath.Join(tool, "README.md")); e != nil {
		t.Fatal(e)
	}
	p, e := Build(tool, file, sha)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = Apply(context.Background(), p); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(tool, "README.md"), []byte("custom"), 0644); e != nil {
		t.Fatal(e)
	}
	if _, e = Build(tool, file, sha); e == nil {
		t.Fatal("program update overwrote customization")
	}
}
func TestProgramPlanCannotBeRehashedToWriteAnotherTarget(t *testing.T) {
	file, sha := fixture(t, nil, runtime.GOOS+"/"+runtime.GOARCH)
	tool := filepath.Join(root(t), "tools")
	p, e := Build(tool, file, sha)
	if e != nil {
		t.Fatal(e)
	}
	p.Inputs["business.go"] = domain.Descriptor{Type: "missing"}
	p.Digest = digest(p)
	if _, e = Apply(context.Background(), p); e == nil {
		t.Fatal("edited and rehashed program plan accepted")
	}
	if _, e = os.Stat(tool); !os.IsNotExist(e) {
		t.Fatal("rejected plan wrote files")
	}
}

func TestProgramDocumentsCanExtendValidatedAlphaBaselineWithoutOverwriting(t *testing.T) {
	for _, custom := range []bool{false, true} {
		t.Run(map[bool]string{false: "managed-additions", true: "human-addition-conflict"}[custom], func(t *testing.T) {
			tool := filepath.Join(root(t), "tools")
			legacy, sha := fixture(t, nil, runtime.GOOS+"/"+runtime.GOARCH)
			p, e := Build(tool, legacy, sha)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = Apply(context.Background(), p); e != nil {
				t.Fatal(e)
			}
			if custom {
				if e = os.MkdirAll(filepath.Join(tool, "compat"), 0755); e != nil {
					t.Fatal(e)
				}
				if e = os.WriteFile(filepath.Join(tool, "compat/README.md"), []byte("human"), 0644); e != nil {
					t.Fatal(e)
				}
			}
			files := map[string][]byte{fileName(): []byte("native-binary"), "README.md": []byte("readme"), "docs/source-lock.json": []byte(`{}`), "docs/compatibility.md": []byte("alpha"), "docs/porting-status.md": []byte("explicit alpha limits"), "docs/cli-retirement.md": []byte("retirement recovery"), "compat/README.md": []byte("compat alpha limits")}
			next, nextSHA := fixture(t, files, runtime.GOOS+"/"+runtime.GOARCH)
			p, e = Build(tool, next, nextSHA)
			if custom {
				if e == nil {
					t.Fatal("manual new-doc destination accepted")
				}
				b, _ := os.ReadFile(filepath.Join(tool, "compat/README.md"))
				if string(b) != "human" {
					t.Fatal("manual doc changed")
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			if _, e = Apply(context.Background(), p); e != nil {
				t.Fatal(e)
			}
			b, e := os.ReadFile(filepath.Join(tool, "docs/porting-status.md"))
			if e != nil || string(b) != "explicit alpha limits" {
				t.Fatal("new docs not installed")
			}
			p, e = Build(tool, next, nextSHA)
			if e != nil {
				t.Fatal(e)
			}
			out, e := Apply(context.Background(), p)
			if e != nil || out.Status != "unchanged" {
				t.Fatalf("repeat extended package %+v %v", out, e)
			}
			if _, e = Build(tool, legacy, sha); e == nil {
				t.Fatal("old package silently removed managed docs")
			}
			if _, e = Rollback(context.Background(), tool); e != nil {
				t.Fatal(e)
			}
			for _, ref := range []string{"docs/porting-status.md", "docs/cli-retirement.md", "compat/README.md"} {
				if _, e = os.Stat(filepath.Join(tool, ref)); !os.IsNotExist(e) {
					t.Fatalf("rollback retained newly installed document: %s", ref)
				}
			}
		})
	}
}

func TestProgramPackageProvenanceMustBeCompleteAndMatchSourceLock(t *testing.T) {
	for _, name := range []string{"valid", "partial", "stripped-provenance", "binary-digest", "cli-commit", "profile-set", "template-commit", "policy-digest", "producer", "source-lock", "unknown-doc"} {
		t.Run(name, func(t *testing.T) {
			lockBytes, e := os.ReadFile("../../docs/source-lock.json")
			if e != nil {
				t.Fatal(e)
			}
			files := map[string][]byte{fileName(): []byte("native-binary"), "README.md": []byte("readme"), "docs/source-lock.json": lockBytes, "docs/compatibility.md": []byte("alpha"), "docs/cli-retirement.md": []byte("retirement recovery")}
			if name == "source-lock" {
				files["docs/source-lock.json"] = []byte(`{}`)
			}
			if name == "unknown-doc" {
				files["docs/unregistered.md"] = []byte("unknown")
			}
			file, sha := fixtureManifest(t, files, runtime.GOOS+"/"+runtime.GOARCH, func(m *Manifest) {
				m.SourceState = "committed"
				m.CLICommit = strings.Repeat("a", 40)
				m.BinarySHA256 = m.Files[fileName()].Digest
				m.Bundles = map[string]*bundle.Inspection{}
				for _, p := range []string{"spec", "design", "backend", "frontend"} {
					m.Bundles[p], e = bundle.Inspect(p)
					if e != nil {
						t.Fatal(e)
					}
				}
				switch name {
				case "stripped-provenance":
					m.CLICommit = ""
					m.BinarySHA256 = ""
					m.Bundles = nil
				case "partial":
					m.Bundles = nil
				case "binary-digest":
					m.BinarySHA256 = strings.Repeat("b", 64)
				case "cli-commit":
					m.CLICommit = "short"
				case "profile-set":
					delete(m.Bundles, "design")
				case "template-commit":
					m.Bundles["spec"].TemplateCommit = strings.Repeat("b", 40)
				case "policy-digest":
					m.Bundles["backend"].SourcePolicy.Digest = strings.Repeat("c", 64)
				case "producer":
					m.Bundles["frontend"].Producer.Commit = strings.Repeat("d", 40)
				}
			})
			tool := filepath.Join(root(t), "tools")
			p, e := Build(tool, file, sha)
			if name != "valid" {
				if e == nil {
					t.Fatal("inconsistent package accepted")
				}
			} else {
				if e != nil {
					t.Fatal(e)
				}
				if _, e = Apply(context.Background(), p); e != nil {
					t.Fatal(e)
				}
				if _, e = Rollback(context.Background(), tool); e != nil {
					t.Fatal(e)
				}
			}
		})
	}
}
