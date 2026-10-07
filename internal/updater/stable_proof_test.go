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

	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/safefs"
)

// Model the public assembler archive, including complete inspect results. The
// receipt hashes bind external evidence; their raw documents are not in a pack.
func stableProofFixture(t *testing.T, change func(map[string]any)) (string, string) {
	t.Helper()
	lock, bundles := committedPackageInputs(t)
	var err error
	files := map[string][]byte{fileName(): []byte("native-binary"), "README.md": []byte("readme"), "docs/source-lock.json": lock, "docs/compatibility.md": []byte("stable")}
	descriptors := map[string]domain.Descriptor{}
	for ref, data := range files {
		mode := uint32(0644)
		if ref == fileName() {
			mode = 0755
		}
		descriptors[ref] = domain.Descriptor{Type: "file", Digest: safefs.Digest(data), Mode: mode}
	}
	m := map[string]any{
		"schemaVersion": 1, "cliVersion": "1.0.0", "protocolVersion": 1,
		"cliCommit": strings.Repeat("a", 40), "sourceState": "committed",
		"platform": runtime.GOOS + "/" + runtime.GOARCH, "cgo": false,
		"bundles": bundles, "binarySha256": descriptors[fileName()].Digest,
		"sourceLockSha256": safefs.Digest(lock), "stableReady": true,
		"runtimeVerification": "passed", "requiredPlatforms": []string{runtime.GOOS + "/" + runtime.GOARCH},
		"supportedPlatforms":  []string{"darwin/amd64", "darwin/arm64", "linux/amd64", "linux/arm64", "windows/amd64", "windows/arm64"},
		"nativeReceiptSha256": strings.Repeat("b", 64), "releaseGateSha256": strings.Repeat("c", 64), "files": descriptors,
	}
	if change != nil {
		change(m)
	}
	files["release-manifest.json"], err = json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	var buffer bytes.Buffer
	gz := gzip.NewWriter(&buffer)
	w := tar.NewWriter(gz)
	for ref, data := range files {
		if err = w.WriteHeader(&tar.Header{Name: ref, Typeflag: tar.TypeReg, Mode: 0644, Size: int64(len(data))}); err != nil {
			t.Fatal(err)
		}
		if _, err = w.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	if err = gz.Close(); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(root(t), "stable.tar.gz")
	if err = os.WriteFile(archive, buffer.Bytes(), 0644); err != nil {
		t.Fatal(err)
	}
	return archive, safefs.Digest(buffer.Bytes())
}

func TestStableProgramPackageProofInstallsAndRollsBack(t *testing.T) {
	archive, sha := stableProofFixture(t, nil)
	tool := filepath.Join(root(t), "tools")
	plan, err := Build(tool, archive, sha)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(tool); !os.IsNotExist(err) {
		t.Fatal("program preview created tool root")
	}
	if _, err = Apply(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	installed, err := os.ReadFile(filepath.Join(tool, "release-manifest.json"))
	if err != nil || !bytes.Contains(installed, []byte(`"sourceLockSha256"`)) {
		t.Fatalf("installed proof missing: %v", err)
	}
	if _, err = os.Stat(filepath.Join(tool, "yss-project.yaml")); !os.IsNotExist(err) {
		t.Fatal("program installation created project identity")
	}
	plan, err = Build(tool, archive, sha)
	if err != nil {
		t.Fatal(err)
	}
	result, err := Apply(context.Background(), plan)
	if err != nil || result.Status != "unchanged" {
		t.Fatalf("same stable package is not idempotent: %+v %v", result, err)
	}
	if _, err = Rollback(context.Background(), tool); err != nil {
		t.Fatal(err)
	}
	for _, ref := range []string{fileName(), "release-manifest.json", receiptRef} {
		if _, err = os.Stat(filepath.Join(tool, ref)); !os.IsNotExist(err) {
			t.Fatalf("whole program rollback retained %s", ref)
		}
	}
}

func TestStableProgramPackageRejectsIncompleteOrInconsistentProof(t *testing.T) {
	mutations := map[string]func(map[string]any){
		"source-lock-sha":      func(m map[string]any) { m["sourceLockSha256"] = strings.Repeat("d", 64) },
		"native-receipt-sha":   func(m map[string]any) { m["nativeReceiptSha256"] = "not-a-digest" },
		"release-gate-missing": func(m map[string]any) { delete(m, "releaseGateSha256") },
		"required-empty":       func(m map[string]any) { m["requiredPlatforms"] = []string{} },
		"required-null":        func(m map[string]any) { m["requiredPlatforms"] = nil },
		"required-duplicate": func(m map[string]any) {
			p := runtime.GOOS + "/" + runtime.GOARCH
			m["requiredPlatforms"] = []string{p, p}
		},
		"required-unknown": func(m map[string]any) {
			m["requiredPlatforms"] = []string{runtime.GOOS + "/" + runtime.GOARCH, "unknown/arm64"}
		},
		"required-excludes-target": func(m map[string]any) {
			other := "linux/amd64"
			if other == runtime.GOOS+"/"+runtime.GOARCH {
				other = "darwin/arm64"
			}
			m["requiredPlatforms"] = []string{other}
		},
		"supported-truncated": func(m map[string]any) { m["supportedPlatforms"] = []string{runtime.GOOS + "/" + runtime.GOARCH} },
		"supported-duplicate": func(m map[string]any) {
			m["supportedPlatforms"] = []string{"darwin/amd64", "darwin/arm64", "linux/amd64", "linux/arm64", "windows/amd64", "windows/amd64"}
		},
		"supported-unknown": func(m map[string]any) {
			m["supportedPlatforms"] = []string{"darwin/amd64", "darwin/arm64", "linux/amd64", "linux/arm64", "windows/amd64", "unknown/arm64"}
		},
		"stable-proof-stripped": func(m map[string]any) {
			for _, key := range []string{"sourceLockSha256", "requiredPlatforms", "supportedPlatforms", "nativeReceiptSha256", "releaseGateSha256"} {
				delete(m, key)
			}
		},
		"empty-partial-nonstable-proof": func(m map[string]any) {
			m["stableReady"] = false
			for _, key := range []string{"requiredPlatforms", "supportedPlatforms", "nativeReceiptSha256", "releaseGateSha256"} {
				delete(m, key)
			}
			m["sourceLockSha256"] = ""
		},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			archive, sha := stableProofFixture(t, mutate)
			tool := filepath.Join(root(t), "tools")
			if _, err := Build(tool, archive, sha); updateErrorCode(err) != "ARTIFACT" {
				t.Fatalf("expected ARTIFACT refusal, got %v", err)
			}
			if _, err := os.Stat(tool); !os.IsNotExist(err) {
				t.Fatal("invalid stable archive touched tool root")
			}
		})
	}
	t.Run("unknown-field", func(t *testing.T) {
		archive, sha := stableProofFixture(t, func(m map[string]any) { m["approvedByManifest"] = true })
		tool := filepath.Join(root(t), "tools")
		if _, err := Build(tool, archive, sha); err == nil || !strings.Contains(err.Error(), `unknown field "approvedByManifest"`) {
			t.Fatalf("strict unknown-field protection changed: %v", err)
		}
		if _, err := os.Stat(tool); !os.IsNotExist(err) {
			t.Fatal("unknown manifest field touched tool root")
		}
	})
}
