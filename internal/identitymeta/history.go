package identitymeta

import "github.com/iloveZzz/yss-cli/internal/domain"

// GovernanceHistory selects one immutable original source, never current execution policy.
type GovernanceHistory struct {
	Ref    string `json:"ref"`
	Digest string `json:"digest"`
}
type GovernanceSnapshot struct {
	SchemaVersion int                          `json:"schema_version"`
	Kind          string                       `json:"kind"`
	SourceProfile string                       `json:"source_profile"`
	Files         map[string]domain.Descriptor `json:"files"`
}

func HistoryFile(ref string) string {
	if ref == "CONTEXT.md" {
		return "source-context.snapshot.md"
	}
	return ref
}
