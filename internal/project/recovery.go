package project

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/schema"
	"github.com/iloveZzz/yss-cli/internal/transaction"
)

// RecoveryIdentity uses the bound native metadata archive when init/attach was
// interrupted before metadata installation. Conflicting existing identities
// remain errors; recovery never guesses a Profile from a directory name.
func RecoveryIdentity(root, explicit string) (*Identity, error) {
	id, original := Detect(root, explicit, false)
	if original == nil {
		return id, nil
	}
	for _, p := range domain.Profiles {
		if ok, e := exists(root, p.Metadata); e != nil || ok {
			return nil, original
		}
	}
	if ok, e := exists(root, MetadataFile); e != nil || ok {
		return nil, original
	}
	root, e := filepath.Abs(root)
	if e != nil {
		return nil, e
	}
	if info, e := os.Lstat(filepath.Join(root, "yss-project.yaml")); e == nil {
		if !info.Mode().IsRegular() {
			return nil, original
		}
		v, e := load(root, "yss-project.yaml")
		if e != nil {
			return nil, e
		}
		m, ok := object(v)
		if !ok || number(m["schema_version"]) != 1 || text(m["repository_mode"]) != "project-instance" {
			return nil, original
		}
	} else if !os.IsNotExist(e) {
		return nil, e
	}
	b, e := transaction.ArchivedFile(root, MetadataFile)
	if e != nil {
		return nil, original
	}
	if !json.Valid(b) {
		return nil, domain.Fail("STATE", "恢复归档中的 metadata 不是 JSON")
	}
	if _, e = schema.Parse(b); e != nil {
		return nil, e
	}
	var m Metadata
	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.DisallowUnknownFields()
	if e = decoder.Decode(&m); e != nil {
		return nil, e
	}
	p, e := domain.GetProfile(m.Profile)
	if e != nil {
		return nil, e
	}
	if m.SchemaVersion != 1 || m.ProtocolVersion != domain.ProtocolVersion || m.ProfileID != p.ID || m.TemplateSourceState != "committed" || !digestPattern.MatchString(m.SnapshotHash) || !digestPattern.MatchString(m.ManifestHash) {
		return nil, domain.Fail("STATE", "恢复归档中的 Profile 或来源合同未知")
	}
	if explicit != "" && explicit != p.Name {
		return nil, domain.Fail("IDENTITY", "恢复归档与显式 Profile 不匹配")
	}
	return &Identity{Root: root, Profile: p, Mode: "project-instance", Native: &m}, nil
}
