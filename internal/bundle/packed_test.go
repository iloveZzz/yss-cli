package bundle

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/safefs"
)

func packedFixtures(t *testing.T) map[string]*Bundle {
	t.Helper()
	out := map[string]*Bundle{}
	for _, profile := range []string{"spec", "design", "backend", "frontend"} {
		shared := encodedFile([]byte("共享中文内容\n"), 0644, "managed")
		projected := shared
		projected.Mode, projected.Ownership = 0755, "managed-customizable"
		files := map[string]File{
			".agents/skills/demo/SKILL.md": shared,
			".codex/skills/demo/SKILL.md":  projected,
			"empty":                        encodedFile(nil, 0644, "managed"),
			"binary":                       encodedFile([]byte{0, 255, 128}, 0644, "managed"),
			"scripts/lib/work-layout.mjs":  encodedFile([]byte("generated\n"), 0644, "managed"),
		}
		initial := map[string]File{}
		for ref, f := range files {
			initial[ref] = f
		}
		if profile == "spec" {
			initial[".agents/skills/demo/SKILL.md"] = encodedFile([]byte("初始变体\n"), 0644, "managed")
		}
		b := &Bundle{SchemaVersion: 3, Profile: profile, TemplateVersion: "git:" + strings.Repeat("a", 40), TemplateCommit: strings.Repeat("a", 40), SourceState: "committed", SourcePolicy: PolicyProvenance{Digest: strings.Repeat("b", 64)}, Upgrade: DefaultUpgradePolicy(), Files: files, Initial: initial, Manifest: map[string]any{"nested": map[string]any{"value": "original"}}, NativeTransforms: []NativeTransform{{Path: "scripts/lib/work-layout.mjs", Generator: "native-work-layout-v1", Source: encodedFile(nil, 0644, "managed"), SourceAbsent: true, OutputDigest: files["scripts/lib/work-layout.mjs"].Digest}}}
		b.BundleHash = contentHash(b)
		out[profile] = b
	}
	return out
}

func TestPackedWriteRoundTripDeduplicationAndDeterminism(t *testing.T) {
	bundles := packedFixtures(t)
	before, err := json.Marshal(bundles)
	if err != nil {
		t.Fatal(err)
	}
	one, err := WriteBuilt(filepath.Join(packedTemp(t), "one"), bundles)
	if err != nil {
		t.Fatal(err)
	}
	two, err := WriteBuilt(filepath.Join(packedTemp(t), "two"), bundles)
	if err != nil {
		t.Fatal(err)
	}
	if one.SHA256 != two.SHA256 || one.ObjectCount != 5 || one.StorageFormatVersion != 1 {
		t.Fatalf("unexpected storage report: %+v %+v", one, two)
	}
	after, _ := json.Marshal(bundles)
	if !bytes.Equal(before, after) {
		t.Fatal("packing mutated its logical inputs")
	}
	compressed, raw, report := packedBytes(t, bundles)
	if report.CompressedBytes != len(compressed) || report.JSONBytes != len(raw) || report.SHA256 != safefs.Digest(compressed) {
		t.Fatal("physical report disagrees with written bytes")
	}
	for profile, want := range bundles {
		got, err := loadPacked(raw, profile)
		if err != nil || !reflect.DeepEqual(want, got) {
			t.Fatalf("%s lost logical content: %v", profile, err)
		}
	}
}

func packedBytes(t *testing.T, bundles map[string]*Bundle) ([]byte, []byte, *StorageReport) {
	t.Helper()
	out := packedTemp(t)
	report, err := WriteBuilt(out, bundles)
	if err != nil {
		t.Fatal(err)
	}
	compressed, err := os.ReadFile(filepath.Join(out, report.Filename))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := decompressSnapshot(compressed)
	if err != nil {
		t.Fatal(err)
	}
	return compressed, raw, report
}

func packedTemp(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestPackedReadRejectsCorruptionAndUnknownFormat(t *testing.T) {
	bundles := packedFixtures(t)
	compressed, raw, _ := packedBytes(t, bundles)
	sharedDigest := bundles["spec"].Files[".agents/skills/demo/SKILL.md"].Digest
	cases := []struct {
		name string
		edit func(*packedArchive)
	}{
		{"format", func(a *packedArchive) { a.StorageFormatVersion++ }},
		{"missing-object", func(a *packedArchive) { delete(a.Objects, sharedDigest) }},
		{"bad-base64", func(a *packedArchive) { a.Objects[sharedDigest] = "invalid!" }},
		{"wrong-content", func(a *packedArchive) { a.Objects[sharedDigest] = "d3Jvbmc=" }},
		{"missing-profile", func(a *packedArchive) { delete(a.Profiles, "spec") }},
		{"mode", func(a *packedArchive) {
			var b Bundle
			json.Unmarshal(a.Profiles["spec"], &b)
			f := b.Files["empty"]
			f.Mode = 0777
			b.Files["empty"] = f
			a.Profiles["spec"], _ = json.Marshal(b)
		}},
		{"inline-data", func(a *packedArchive) {
			var b Bundle
			json.Unmarshal(a.Profiles["spec"], &b)
			f := b.Files["empty"]
			f.Data = "d3Jvbmc="
			b.Files["empty"] = f
			a.Profiles["spec"], _ = json.Marshal(b)
		}},
		{"logical-hash", func(a *packedArchive) {
			var b Bundle
			json.Unmarshal(a.Profiles["spec"], &b)
			b.Manifest["changed"] = true
			a.Profiles["spec"], _ = json.Marshal(b)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var a packedArchive
			if err := json.Unmarshal(raw, &a); err != nil {
				t.Fatal(err)
			}
			tc.edit(&a)
			bad, _ := json.Marshal(a)
			if _, err := loadPacked(bad, "spec"); err == nil {
				t.Fatal("invalid packed storage accepted")
			}
		})
	}
	if _, err := loadPacked(append(raw, []byte("{}")...), "spec"); err == nil {
		t.Fatal("trailing JSON accepted")
	}
	if _, err := decompressSnapshot(compressed[:len(compressed)-5]); err == nil {
		t.Fatal("truncated gzip accepted")
	}
	if _, err := decompressSnapshotWithLimit(compressed, 8); err == nil {
		t.Fatal("decompression limit ignored")
	}
}

func TestPackedConcurrentReadsReturnIndependentObjects(t *testing.T) {
	bundles := packedFixtures(t)
	_, raw, _ := packedBytes(t, bundles)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for profile, want := range bundles {
				got, err := loadPacked(raw, profile)
				if err != nil || !reflect.DeepEqual(got, want) {
					t.Errorf("%s: %v", profile, err)
					return
				}
				got.Manifest["nested"].(map[string]any)["value"] = "mutated"
				delete(got.Files, "empty")
			}
		}()
	}
	wg.Wait()
}

func TestPackedFailedWritePreservesPreviousArchive(t *testing.T) {
	out := packedTemp(t)
	report, err := WriteBuilt(out, packedFixtures(t))
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(out, report.Filename)
	before, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"logical-hash", "same-digest-conflict"} {
		bundles := packedFixtures(t)
		if kind == "logical-hash" {
			bundles["backend"].BundleHash = strings.Repeat("0", 64)
		} else {
			b := bundles["backend"]
			f := b.Files[".agents/skills/demo/SKILL.md"]
			// Base64 permits newlines, but one digest must have one stored encoding.
			f.Data += "\n"
			b.Files[".agents/skills/demo/SKILL.md"] = f
			b.BundleHash = contentHash(b)
		}
		if _, err := WriteBuilt(out, bundles); err == nil {
			t.Fatalf("%s was accepted", kind)
		}
		after, err := os.ReadFile(file)
		if err != nil || !bytes.Equal(before, after) {
			t.Fatalf("%s replaced a valid archive: %v", kind, err)
		}
	}
	entries, err := os.ReadDir(out)
	if err != nil || len(entries) != 1 {
		t.Fatalf("failed write left staging files: %v", err)
	}
}

func TestPackedEmbeddedMatchesFrozenBaseline(t *testing.T) {
	root := os.Getenv("YSS_BUNDLE_BASELINE_ROOT")
	if root == "" {
		t.Skip("frozen pre-optimization corpus not supplied")
	}
	for _, profile := range []string{"spec", "design", "backend", "frontend"} {
		t.Run(profile, func(t *testing.T) {
			compressed, err := os.ReadFile(filepath.Join(root, profile+".json.gz"))
			if err != nil {
				t.Fatal(err)
			}
			raw, err := decompressSnapshot(compressed)
			if err != nil {
				t.Fatal(err)
			}
			var want Bundle
			if err := json.Unmarshal(raw, &want); err != nil {
				t.Fatal(err)
			}
			got, err := Load(profile)
			if err != nil || !reflect.DeepEqual(&want, got) {
				t.Fatalf("%s changed fixed corpus: %v", profile, err)
			}
		})
	}
}

func BenchmarkLoad(b *testing.B) {
	for _, profile := range []string{"spec", "design", "backend", "frontend"} {
		b.Run(profile, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, err := Load(profile); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func TestContentHashMatchesMarshalBytes(t *testing.T) {
	for profile, b := range packedFixtures(t) {
		b.Manifest["escaped"] = "中文 <>&\n\u2028\u2029"
		before, _ := json.Marshal(b)
		clone := *b
		clone.BundleHash = ""
		raw, err := json.Marshal(&clone)
		if err != nil || contentHash(b) != safefs.Digest(raw) {
			t.Fatalf("%s changed the logical digest: %v", profile, err)
		}
		after, _ := json.Marshal(b)
		if !bytes.Equal(before, after) {
			t.Fatal("hashing mutated its input")
		}
		b.Manifest["unsupported"] = func() {}
		if contentHash(b) != safefs.Digest(nil) {
			t.Fatal("encoding failure changed existing hash behavior")
		}
	}
}
