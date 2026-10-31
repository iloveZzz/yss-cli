//go:build !yss_nobundle

package bundle

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
	"testing"
	"testing/fstest"
)

func trustedFixture(t *testing.T, version, commit string) (trustedArchive, []byte, SourceIdentity) {
	t.Helper()
	b, err := embeddedBundles.load("spec", newValidationOp())
	if err != nil {
		t.Fatal(err)
	}
	b.TemplateVersion, b.TemplateCommit = version, commit
	b.BundleHash = Hash(b)
	packed, _, err := packBundles(map[string]*Bundle{"spec": b})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := decompressSnapshot(packed)
	if err != nil {
		t.Fatal(err)
	}
	var content struct {
		Objects map[string]string `json:"objects"`
	}
	if err := json.Unmarshal(raw, &content); err != nil {
		t.Fatal(err)
	}
	marker := []byte(`,"objects":{`)
	at := bytes.LastIndex(raw, marker)
	if at < 0 {
		t.Fatal("missing object marker")
	}
	keys := make([]string, 0, len(content.Objects))
	for key := range content.Objects {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	patch, err := json.Marshal(map[string]any{"version": 1, "prefix": raw[:at+len(marker)], "keys": keys, "missing": content.Objects})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	z := gzip.NewWriter(&out)
	if _, err := z.Write(patch); err != nil {
		t.Fatal(err)
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	digest := func(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }
	entry := trustedArchive{Version: version, Commit: commit, SourceLock: "docs/trusted-sources/" + version + ".json", ProducerCommit: strings.Repeat("b", 40), ArchiveSHA256: digest(packed), RawSHA256: digest(raw), DeltaFile: version + ".delta.gz", DeltaSHA256: digest(out.Bytes())}
	source := SourceIdentity{Profile: b.Profile, Version: b.TemplateVersion, Commit: b.TemplateCommit, Snapshot: b.SnapshotHash, Manifest: b.ManifestHash, Bundle: b.BundleHash, State: b.SourceState}
	return entry, out.Bytes(), source
}

func TestTrustedRegistrySupportsMultiplePostResetSources(t *testing.T) {
	first, firstDelta, firstSource := trustedFixture(t, "5.0.0", strings.Repeat("2", 40))
	second, secondDelta, secondSource := trustedFixture(t, "5.1.0", strings.Repeat("3", 40))
	registry, err := json.Marshal(map[string]any{"schemaVersion": 1, "archives": []trustedArchive{first, second}})
	if err != nil {
		t.Fatal(err)
	}
	files := fstest.MapFS{
		"assets/trusted/registry.json":  {Data: registry},
		"assets/trusted/5.0.0.delta.gz": {Data: firstDelta},
		"assets/trusted/5.1.0.delta.gz": {Data: secondDelta},
	}
	for _, source := range []SourceIdentity{firstSource, secondSource} {
		if _, err := loadTrustedArchive(files, packedData, source); err != nil {
			t.Fatalf("trusted source %s rejected: %v", source.Version, err)
		}
	}
	untrusted := secondSource
	untrusted.Bundle = strings.Repeat("a", 64)
	if _, err := loadTrustedArchive(files, packedData, untrusted); err == nil {
		t.Fatal("untrusted bundle identity accepted")
	}
	files["assets/trusted/5.0.0.delta.gz"] = &fstest.MapFile{Data: []byte("tampered")}
	if _, err := loadTrustedArchive(files, packedData, firstSource); err == nil {
		t.Fatal("tampered delta accepted")
	}
}

func TestTrustedRegistryRejectsRetiredAndDuplicateSources(t *testing.T) {
	entry := trustedArchive{Version: "5.0.0", Commit: retiredTemplateCommit, SourceLock: "docs/trusted-sources/5.0.0.json", ProducerCommit: strings.Repeat("b", 40), ArchiveSHA256: strings.Repeat("c", 64), RawSHA256: strings.Repeat("d", 64), DeltaFile: "5.0.0.delta.gz", DeltaSHA256: strings.Repeat("e", 64)}
	for _, rows := range [][]trustedArchive{{entry}, {func() trustedArchive { e := entry; e.Commit = strings.Repeat("2", 40); return e }(), func() trustedArchive { e := entry; e.Commit = strings.Repeat("3", 40); return e }()}} {
		data, _ := json.Marshal(map[string]any{"schemaVersion": 1, "archives": rows})
		files := fstest.MapFS{"assets/trusted/registry.json": {Data: data}}
		if _, err := loadTrustedArchive(files, packedData, SourceIdentity{}); err == nil {
			t.Fatal("invalid registry accepted")
		}
	}
}
