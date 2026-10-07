package project

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/governance"
	"github.com/iloveZzz/yss-cli/internal/identitymeta"
	"github.com/iloveZzz/yss-cli/internal/safefs"
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
	legacyFiles := []domain.Profile{}
	for _, p := range domain.Profiles {
		if ok, e := exists(root, p.Metadata); e != nil {
			return nil, original
		} else if ok {
			legacyFiles = append(legacyFiles, p)
		}
	}
	if len(legacyFiles) > 1 {
		return nil, original
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
	if _, e := identitymeta.ValidateNative(root, b, ""); e != nil {
		return nil, domain.Wrap("STATE", e)
	}
	p, e := domain.GetProfile(m.Profile)
	if e != nil {
		return nil, e
	}
	if (m.SchemaVersion < 1 || m.SchemaVersion > 3) || m.ProtocolVersion != domain.ProtocolVersion || m.ProfileID != p.ID || m.TemplateSourceState != "committed" || !digestPattern.MatchString(m.SnapshotHash) || !digestPattern.MatchString(m.ManifestHash) {
		return nil, domain.Fail("STATE", "恢复归档中的 Profile 或来源合同未知")
	}
	if len(legacyFiles) == 1 {
		legacy := legacyFiles[0]
		if legacy.Name != p.Name {
			return nil, original
		}
		current, e := os.ReadFile(filepath.Join(root, legacy.Metadata))
		if e != nil {
			return nil, e
		}
		archived, e := transaction.ArchivedFile(root, legacy.Metadata)
		if e != nil || !bytes.Equal(current, archived) {
			return nil, original
		}
	}
	if explicit != "" && explicit != p.Name {
		return nil, domain.Fail("IDENTITY", "恢复归档与显式 Profile 不匹配")
	}
	return &Identity{Root: root, Profile: p, Mode: "project-instance", Native: &m}, nil
}

// RecoverPreparation seals only unpublished control-state evidence. It never
// restores, writes or guesses project assets. Initial partial preparations need
// an explicit Profile; installed projects are checked
// again under the transaction lock, including their exact metadata descriptors.
func RecoverPreparation(ctx context.Context, root, explicit string, apply bool) (transaction.Result, bool, error) {
	status, err := transaction.Status(root)
	if err != nil {
		return transaction.Result{}, false, err
	}
	if len(status.Pending) > 0 || (len(status.Preparations) == 0 && (len(status.SealedPreparations) == 0 || len(status.Transactions) > 0)) {
		return status, false, nil
	}
	id, err := Detect(root, explicit, true)
	if err != nil {
		return transaction.Result{}, true, err
	}
	if id.Profile.Name == "" {
		return transaction.Result{}, true, domain.Fail("IDENTITY", "准备恢复必须显式指定 Profile")
	}
	installed := id.Native != nil || id.Legacy != nil
	if !installed && explicit == "" {
		return transaction.Result{}, true, domain.Fail("IDENTITY", "空初始化准备必须显式指定 Profile")
	}
	descriptors := map[string]domain.Descriptor{}
	for _, ref := range []string{MetadataFile, id.LegacyFile, "yss-project.yaml", id.ProfileRef} {
		if ref == "" {
			continue
		}
		d, e := safefs.Describe(id.Root, ref)
		if e != nil {
			return transaction.Result{}, true, e
		}
		descriptors[ref] = d
	}
	validate := func(summary transaction.Summary, paths []string) error {
		current, e := Detect(id.Root, id.Profile.Name, true)
		if e != nil {
			return e
		}
		if (current.Native != nil || current.Legacy != nil) != installed {
			return domain.Fail("IDENTITY", "准备恢复期间项目身份变化")
		}
		for ref, want := range descriptors {
			d, e := safefs.Describe(id.Root, ref)
			if e != nil {
				return e
			}
			if d != want {
				return domain.Fail("CONCURRENT", "准备恢复期间身份资产变化: "+ref)
			}
		}
		if !installed {
			if summary.Kind == "abandoned-preparation" {
				// An interrupted header has no trusted asset plan or Profile.
				// The transaction layer has proved this is unpublished control
				// evidence only, without objects or write intents. Preserve it
				// under the caller's explicit Profile; do not authorize init or
				// infer any target write from the partial bytes.
				return nil
			}
			if summary.Kind == "attach" && summary.Profile == id.Profile.Name {
				// A complete durable, root-bound header and every original input
				// have already been checked by RecoverPreparations. No target
				// writes were published, so sealing preserves the existing project.
				return nil
			}
			if summary.Kind != "init" {
				return domain.Fail("KIND", "无安装身份的准备不能作为 init 封存")
			}
			return transaction.VerifyEmptyPreparationRoot(id.Root)
		}
		switch summary.Kind {
		case ProfileLinksKind:
			return ValidateProfileLinksTransaction(id.Root, id.Profile.Name, paths)
		case "spec-baseline-import":
			return governance.ValidateSpecBaselineImportTransaction(id.Root, id.Profile.Name, paths)
		case "init", "attach", "sync", "migrate", "skills", "assets", "abandoned-preparation":
			return nil
		default:
			return domain.Fail("KIND", "未知准备事务 kind，拒绝封存")
		}
	}
	return transaction.RecoverPreparations(ctx, id.Root, id.Profile.Name, apply, validate)
}
