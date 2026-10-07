package transaction

import (
	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/safefs"
)

type ArtifactRecord struct {
	Path       string            `json:"path"`
	Kind       string            `json:"kind"`
	Descriptor domain.Descriptor `json:"descriptor"`
}
type Artifact struct {
	ArtifactRecord
	Data []byte
}

func validateArtifacts(records []ArtifactRecord) error {
	seen := map[string]bool{}
	var paths safefs.PathSet
	for _, r := range records {
		if e := paths.Add(r.Path); e != nil {
			return e
		}
		if e := safefs.ValidateRef(r.Path); e != nil {
			return e
		}
		key := r.Path + ":" + r.Kind
		if seen[key] || r.Kind != "baseline" && r.Kind != "template" && r.Kind != "source" && r.Kind != "work-layout-mapping" || !canonicalDescriptor(r.Descriptor) || r.Descriptor.Type != "file" {
			return fail("STATE", "基线归档材料描述非法")
		}
		seen[key] = true
	}
	return nil
}
func normalizeArtifacts(artifacts []Artifact) ([]ArtifactRecord, error) {
	records := make([]ArtifactRecord, len(artifacts))
	for i, a := range artifacts {
		records[i] = a.ArtifactRecord
		if safefs.Digest(a.Data) != a.Descriptor.Digest {
			return nil, fail("PLAN", "基线归档材料摘要不匹配")
		}
	}
	return records, validateArtifacts(records)
}

// BaselineMaterial is read-only and accepts only registered exact bytes. Older
// archives can prove a baseline through their original/candidate descriptors.
func BaselineMaterial(root, ref string, want domain.Descriptor) ([]byte, string, bool, error) {
	r, e := NewMaterialReader(root)
	if e != nil {
		return nil, "", false, e
	}
	return r.Read(ref, want)
}

type MaterialReader struct {
	root     string
	archives []loaded
}

func NewMaterialReader(root string) (*MaterialReader, error) {
	all, _, e := scan(root)
	if e != nil {
		return nil, e
	}
	return &MaterialReader{root: root, archives: all}, nil
}
func (reader *MaterialReader) Read(ref string, want domain.Descriptor) ([]byte, string, bool, error) {
	if e := safefs.ValidateRef(ref); e != nil {
		return nil, "", false, e
	}
	if !canonicalDescriptor(want) || want.Type != "file" {
		return nil, "", false, fail("BASELINE", "无效模板基线")
	}
	all := reader.archives
	for i := len(all) - 1; i >= 0; i-- {
		l := all[i]
		found := false
		for _, a := range l.plan.Artifacts {
			found = found || a.Path == ref && a.Kind == "baseline" && a.Descriptor == want
		}
		for _, r := range l.plan.Operations {
			found = found || r.Path == ref && (r.Before == want || r.After == want)
		}
		if !found {
			continue
		}
		b, e := blob(reader.root, l.base, 0, want, false)
		if e != nil {
			return nil, "", false, e
		}
		return b, "transaction:" + l.plan.ID, true, nil
	}
	return nil, "unavailable", false, nil
}
