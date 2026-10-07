package bundle

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/safefs"
)

type FileDescriptor struct {
	Path      string `json:"path,omitempty"`
	Digest    string `json:"digest"`
	Mode      uint32 `json:"mode"`
	Ownership string `json:"ownership"`
	Size      int    `json:"size"`
}
type Inspection struct {
	NativeTransforms  []TransformDescriptor     `json:"nativeTransforms,omitempty"`
	Upgrade           *UpgradePolicy            `json:"upgradePolicy,omitempty"`
	Initial           map[string]FileDescriptor `json:"initial"`
	Legacy            LegacyLineage             `json:"legacy,omitempty"`
	SchemaVersion     int                       `json:"schemaVersion"`
	Profile           string                    `json:"profile"`
	TemplateVersion   string                    `json:"templateVersion"`
	TemplateCommit    string                    `json:"templateCommit"`
	SourceState       string                    `json:"sourceState"`
	SnapshotHash      string                    `json:"sourceSnapshotHash"`
	ManifestHash      string                    `json:"manifestHash"`
	BundleHash        string                    `json:"bundleHash"`
	LegacyVersion     string                    `json:"legacyVersion,omitempty"`
	LegacyCLICommit   string                    `json:"legacyCLICommit,omitempty"`
	Producer          Provenance                `json:"producer"`
	SourcePolicy      PolicyProvenance          `json:"sourcePolicy"`
	Manifest          map[string]any            `json:"manifest"`
	Distribution      map[string]any            `json:"distribution"`
	Files             map[string]FileDescriptor `json:"files"`
	InitialPaths      []string                  `json:"initialPaths"`
	StageRequirements any                       `json:"stageRequirements,omitempty"`
	SkillRequirements any                       `json:"skillRequirements,omitempty"`
}
type TransformDescriptor struct {
	Path         string `json:"path"`
	Generator    string `json:"generator"`
	SourceDigest string `json:"sourceDigest"`
	OutputDigest string `json:"outputDigest"`
	SourceAbsent bool   `json:"sourceAbsent,omitempty"`
}
type ExportResult struct {
	Inspection   *Inspection      `json:"inspection"`
	Directory    string           `json:"directory"`
	ManifestPath string           `json:"manifestPath"`
	Files        []FileDescriptor `json:"files"`
}

func Inspect(profile string) (*Inspection, error) {
	b, e := Load(profile)
	if e != nil {
		return nil, e
	}
	return inspectBundle(b)
}
func inspectBundle(b *Bundle) (*Inspection, error) {
	if e := validate(b, b.Profile); e != nil {
		return nil, e
	}
	version := b.TemplateVersion
	if version == "" {
		version = b.LegacyVersion
	}
	legacyCommit := b.LegacyCLICommit
	if legacyCommit == "" && b.SchemaVersion == 1 {
		legacyCommit = b.CLICommit
	}
	hash := b.BundleHash
	if hash == "" {
		hash = contentHash(b)
	}
	i := &Inspection{SchemaVersion: b.SchemaVersion, Profile: b.Profile, Legacy: b.Legacy, TemplateVersion: version, TemplateCommit: b.TemplateCommit, SourceState: b.SourceState, SnapshotHash: b.SnapshotHash, ManifestHash: b.ManifestHash, BundleHash: hash, LegacyVersion: b.LegacyVersion, LegacyCLICommit: legacyCommit, Producer: b.Producer, SourcePolicy: b.SourcePolicy, Manifest: b.Manifest, Distribution: b.Distribution, Files: map[string]FileDescriptor{}, InitialPaths: sortedKeys(b.Initial), StageRequirements: b.StageRequirements, SkillRequirements: b.SkillRequirements}
	for p, f := range b.Files {
		data, e := base64.StdEncoding.DecodeString(f.Data)
		if e != nil {
			return nil, e
		}
		i.Files[p] = FileDescriptor{Digest: f.Digest, Mode: f.Mode, Ownership: f.Ownership, Size: len(data)}
	}
	i.Upgrade = b.Upgrade
	for _, t := range b.NativeTransforms {
		i.NativeTransforms = append(i.NativeTransforms, TransformDescriptor{Path: t.Path, Generator: t.Generator, SourceDigest: t.Source.Digest, OutputDigest: t.OutputDigest, SourceAbsent: t.SourceAbsent})
	}
	i.Initial = map[string]FileDescriptor{}
	for p, f := range b.Initial {
		data, e := base64.StdEncoding.DecodeString(f.Data)
		if e != nil {
			return nil, e
		}
		i.Initial[p] = FileDescriptor{Digest: f.Digest, Mode: f.Mode, Ownership: f.Ownership, Size: len(data)}
	}
	return i, nil
}

// Export uses the same embedded full bundle as Inspect. Output must be a new
// ordinary directory; it exports unrendered asset bytes with exact modes.
func Export(ctx context.Context, profile, out string) (result *ExportResult, err error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	b, e := Load(profile)
	if e != nil {
		return nil, e
	}
	i, e := inspectBundle(b)
	if e != nil {
		return nil, e
	}
	out, e = filepath.Abs(out)
	if e != nil {
		return nil, e
	}
	if _, e = safefs.Path(out, ".yss-bundle.json"); e != nil {
		return nil, e
	}
	if _, e = os.Lstat(out); e == nil {
		return nil, domain.Fail("EXISTS", "export target already exists: "+out)
	} else if !os.IsNotExist(e) {
		return nil, e
	}
	parent := filepath.Dir(out)
	if e = os.MkdirAll(parent, 0755); e != nil {
		return nil, e
	}
	if e = os.Mkdir(out, 0755); e != nil {
		return nil, e
	}
	defer func() {
		if err != nil {
			os.RemoveAll(out)
		}
	}()
	root, e := os.OpenRoot(out)
	if e != nil {
		return nil, e
	}
	defer root.Close()
	list := make([]FileDescriptor, 0, len(b.Files))
	for _, ref := range sortedKeys(b.Files) {
		if e = ctx.Err(); e != nil {
			return nil, e
		}
		if ref == ".yss-bundle.json" || ref == SnapshotFile {
			return nil, fmt.Errorf("reserved bundle manifest path")
		}
		f := b.Files[ref]
		data, e := base64.StdEncoding.DecodeString(f.Data)
		if e != nil {
			return nil, e
		}
		dir := filepath.Dir(filepath.FromSlash(ref))
		if dir != "." {
			if e = root.MkdirAll(dir, 0755); e != nil {
				return nil, e
			}
		}
		file, e := root.OpenFile(filepath.FromSlash(ref), os.O_WRONLY|os.O_CREATE|os.O_EXCL, os.FileMode(f.Mode))
		if e != nil {
			return nil, e
		}
		_, e = file.Write(data)
		if e == nil {
			e = file.Chmod(os.FileMode(f.Mode))
		}
		if ce := file.Close(); e == nil {
			e = ce
		}
		if e != nil {
			return nil, e
		}
		d := i.Files[ref]
		d.Path = ref
		list = append(list, d)
	}
	if e = ctx.Err(); e != nil {
		return nil, e
	}
	manifest, e := json.MarshalIndent(i, "", "  ")
	if e != nil {
		return nil, e
	}
	manifest = append(manifest, '\n')
	f, e := root.OpenFile(".yss-bundle.json", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if e != nil {
		return nil, e
	}
	_, e = f.Write(manifest)
	if ce := f.Close(); e == nil {
		e = ce
	}
	if e != nil {
		return nil, e
	}
	snapshot, e := json.Marshal(b)
	if e != nil {
		return nil, e
	}
	sf, e := root.OpenFile(SnapshotFile, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if e != nil {
		return nil, e
	}
	_, e = sf.Write(append(snapshot, '\n'))
	if ce := sf.Close(); e == nil {
		e = ce
	}
	if e != nil {
		return nil, e
	}
	return &ExportResult{Inspection: i, Directory: out, ManifestPath: filepath.Join(out, ".yss-bundle.json"), Files: list}, nil
}
