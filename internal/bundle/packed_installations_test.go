package bundle

import (
	"encoding/json"
	"strings"
	"testing"
)

func packedInstallationFixture(t *testing.T) *Bundle {
	t.Helper()
	f := func(s string) File { return encodedFile([]byte(s), 0644, "managed") }
	files := map[string]File{
		"yss-project.yaml": f(`{"schema_version":2,"repository_mode":"project-instance"}`),
		".template-spec/process/harness-profile.yaml":        f(`{"schema_version":3,"profile_id":"harness.spec-template","status":"active","required_capabilities":["role-independent-governance-v1"]}`),
		".template-spec/process/orchestration-contract.yaml": f(`{"schema_version":2,"governance_versions":{"required_capability":"role-independent-governance-v1"}}`),
		".agents/skills/probe/SKILL.md":                      f("shared bytes"),
	}
	b := &Bundle{SchemaVersion: 5, Profile: "spec", TemplateVersion: "git:" + strings.Repeat("a", 40), TemplateCommit: strings.Repeat("a", 40), SourceState: "committed", SnapshotHash: strings.Repeat("b", 64), SourcePolicy: PolicyProvenance{Digest: strings.Repeat("c", 64)}, Files: files, Initial: files, Distribution: map[string]any{}, Upgrade: DefaultUpgradePolicy(), Installations: map[string]Installation{
		"skills":   {Initial: map[string]File{".agents/skills/probe/SKILL.md": files[".agents/skills/probe/SKILL.md"], "AGENTS.md": f("unique entry")}, Distribution: map[string]any{"installation": "skills"}},
		"governed": {Distribution: map[string]any{"installation": "governed"}},
	}}
	b.BundleHash = contentHash(b)
	return b
}

func TestPackedInstallationObjectsRoundTripWithoutMutation(t *testing.T) {
	b := packedInstallationFixture(t)
	before, _ := json.Marshal(b)
	packed, _, err := packBundles(map[string]*Bundle{"spec": b})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := decompressSnapshot(packed)
	if err != nil {
		t.Fatal(err)
	}
	var archive packedArchive
	if err = json.Unmarshal(raw, &archive); err != nil {
		t.Fatal(err)
	}
	if archive.StorageFormatVersion != 2 {
		t.Fatalf("new storage version = %d, want 2", archive.StorageFormatVersion)
	}
	var stored Bundle
	if err = json.Unmarshal(archive.Profiles["spec"], &stored); err != nil {
		t.Fatal(err)
	}
	for ref, f := range stored.Installations["skills"].Initial {
		if f.Data != "" {
			t.Errorf("inline installation object remains at %s", ref)
		}
		if _, ok := archive.Objects[f.Digest]; !ok {
			t.Errorf("missing object for %s", ref)
		}
	}
	got, err := loadPacked(raw, "spec")
	if err != nil {
		t.Fatal(err)
	}
	decoded, _ := json.Marshal(got)
	if string(decoded) != string(before) {
		t.Fatal("logical bundle changed during pack/unpack")
	}
	after, _ := json.Marshal(b)
	if string(after) != string(before) {
		t.Fatal("pack mutated caller bundle")
	}
}

func TestLoadPackedLegacyInlineInstallationAndRejectTamper(t *testing.T) {
	original := packedInstallationFixture(t)
	item := original.Installations["skills"]
	item.Initial["empty.md"] = encodedFile(nil, 0644, "managed")
	original.Installations["skills"] = item
	original.BundleHash = contentHash(original)
	packed, _, err := packBundles(map[string]*Bundle{"spec": original})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := decompressSnapshot(packed)
	if err != nil {
		t.Fatal(err)
	}
	var archive packedArchive
	if err = json.Unmarshal(raw, &archive); err != nil {
		t.Fatal(err)
	}
	var stored Bundle
	if err = json.Unmarshal(archive.Profiles["spec"], &stored); err != nil {
		t.Fatal(err)
	}
	entry := stored.Installations["skills"]
	for ref, f := range entry.Initial {
		f.Data = archive.Objects[f.Digest]
		entry.Initial[ref] = f
	}
	stored.Installations["skills"] = entry
	delete(archive.Objects, entry.Initial["empty.md"].Digest) // old v1 did not require objects for inline files
	archive.StorageFormatVersion = 1
	archive.Profiles["spec"], err = json.Marshal(&stored)
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := json.Marshal(&archive)
	if err != nil {
		t.Fatal(err)
	}
	got, err := loadPacked(legacy, "spec")
	if err != nil {
		t.Fatalf("valid old archive rejected: %v", err)
	}
	want, _ := json.Marshal(original)
	actual, _ := json.Marshal(got)
	if string(want) != string(actual) {
		t.Fatal("legacy roundtrip changed bundle")
	}
	f := entry.Initial["AGENTS.md"]
	f.Data = "dGFtcGVyZWQ="
	entry.Initial["AGENTS.md"] = f
	stored.Installations["skills"] = entry
	archive.Profiles["spec"], _ = json.Marshal(&stored)
	legacy, _ = json.Marshal(&archive)
	if _, err = loadPacked(legacy, "spec"); err == nil {
		t.Fatal("accepted tampered legacy inline content")
	}
}

func TestLoadPackedRejectsMissingInstallationObject(t *testing.T) {
	original := packedInstallationFixture(t)
	packed, _, err := packBundles(map[string]*Bundle{"spec": original})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := decompressSnapshot(packed)
	if err != nil {
		t.Fatal(err)
	}
	var archive packedArchive
	if err = json.Unmarshal(raw, &archive); err != nil {
		t.Fatal(err)
	}
	delete(archive.Objects, original.Installations["skills"].Initial["AGENTS.md"].Digest)
	raw, _ = json.Marshal(&archive)
	if _, err = loadPacked(raw, "spec"); err == nil {
		t.Fatal("accepted missing installation object")
	}
}

func TestLoadPackedV2RejectsInlineInstallationContent(t *testing.T) {
	b := packedInstallationFixture(t)
	compressed, _, err := packBundles(map[string]*Bundle{"spec": b})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := decompressSnapshot(compressed)
	if err != nil {
		t.Fatal(err)
	}
	var archive packedArchive
	if err = json.Unmarshal(raw, &archive); err != nil {
		t.Fatal(err)
	}
	var stored Bundle
	if err = json.Unmarshal(archive.Profiles["spec"], &stored); err != nil {
		t.Fatal(err)
	}
	entry := stored.Installations["skills"]
	f := entry.Initial["AGENTS.md"]
	f.Data = archive.Objects[f.Digest]
	entry.Initial["AGENTS.md"] = f
	stored.Installations["skills"] = entry
	archive.Profiles["spec"], _ = json.Marshal(&stored)
	raw, _ = json.Marshal(&archive)
	if _, err = loadPacked(raw, "spec"); err == nil {
		t.Fatal("v2 accepted inline installation bytes")
	}
}

func TestLoadPackedV2RejectsMissingEmptyObject(t *testing.T) {
	b := packedInstallationFixture(t)
	item := b.Installations["skills"]
	item.Initial["empty.md"] = encodedFile(nil, 0644, "managed")
	b.Installations["skills"] = item
	b.BundleHash = contentHash(b)
	packed, _, err := packBundles(map[string]*Bundle{"spec": b})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := decompressSnapshot(packed)
	if err != nil {
		t.Fatal(err)
	}
	var a packedArchive
	if err = json.Unmarshal(raw, &a); err != nil {
		t.Fatal(err)
	}
	delete(a.Objects, item.Initial["empty.md"].Digest)
	raw, _ = json.Marshal(a)
	if _, err = loadPacked(raw, "spec"); err == nil {
		t.Fatal("accepted missing v2 empty object")
	}
}

func TestLoadPackedV2RejectsCorruptObjectAndUnknownVersion(t *testing.T) {
	original := packedInstallationFixture(t)
	packed, _, err := packBundles(map[string]*Bundle{"spec": original})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := decompressSnapshot(packed)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []string{"bad-base64", "wrong-bytes", "unknown-version"} {
		t.Run(tc, func(t *testing.T) {
			var a packedArchive
			if err := json.Unmarshal(raw, &a); err != nil {
				t.Fatal(err)
			}
			key := original.Installations["skills"].Initial["AGENTS.md"].Digest
			switch tc {
			case "bad-base64":
				a.Objects[key] = "*"
			case "wrong-bytes":
				a.Objects[key] = "dGFtcGVyZWQ="
			case "unknown-version":
				a.StorageFormatVersion = 999
			}
			changed, _ := json.Marshal(&a)
			if _, err := loadPacked(changed, "spec"); err == nil {
				t.Fatal("corrupt archive accepted")
			}
		})
	}
}
