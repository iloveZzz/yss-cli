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
