package identitymeta

import (
	"encoding/json"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/schema"
)

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

// Callers read through their own live, frozen or transaction-archive view.
func ValidateHistoryProfile(history *GovernanceHistory, raw []byte) error {
	if safefs.Digest(raw) != history.Digest {
		return domain.Fail("HISTORY_SOURCE_DRIFT", "原政策快照清单摘要变化")
	}
	if _, err := schema.Parse(raw); err != nil {
		return domain.Wrap("IDENTITY", err)
	}
	var source GovernanceSnapshot
	if err := json.Unmarshal(raw, &source); err != nil {
		return domain.Wrap("IDENTITY", err)
	}
	_, err := domain.GetProfile(source.SourceProfile)
	return err
}

func HistoryFile(ref string) string {
	if ref == "CONTEXT.md" {
		return "source-context.snapshot.md"
	}
	return ref
}
