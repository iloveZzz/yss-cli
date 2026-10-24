package kernel

import (
	"encoding/json"
	"path"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/identitymeta"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/schema"
)

// OriginalGovernanceSource is an explicit readonly selector, never a fallback from a failed current validator.
func (s *SemanticSession) OriginalGovernanceSource() (*SemanticSession, error) {
	present, err := s.Exists(domain.MetadataFile)
	if err != nil || !present {
		return nil, err
	}
	raw, err := s.Bytes(domain.MetadataFile)
	if err != nil {
		return nil, err
	}
	m, err := identitymeta.ValidateNative(s.Root, raw, "")
	if err != nil {
		return nil, err
	}
	if m.SchemaVersion < 4 {
		return nil, nil
	}
	if m.GovernanceHistory == nil {
		return nil, s.Reject("HISTORY_SOURCE_MISSING", "新实例没有绑定的原政策快照；--history不能回退到当前资格")
	}
	if m.Distribution["installation"] != "skills" {
		if err = s.Authorities(); err != nil {
			return nil, err
		}
	}
	h := m.GovernanceHistory
	raw, err = s.Bytes(h.Ref)
	if err != nil {
		return nil, err
	}
	if safefs.Digest(raw) != h.Digest {
		return nil, s.Reject("HISTORY_SOURCE_DRIFT", "原政策快照清单摘要变化")
	}
	if _, err = schema.Parse(raw); err != nil {
		return nil, err
	}
	var snapshot identitymeta.GovernanceSnapshot
	if err = json.Unmarshal(raw, &snapshot); err != nil {
		return nil, err
	}
	if snapshot.SchemaVersion != 1 || snapshot.Kind != "governance-original-source" || snapshot.SourceProfile != "spec" && snapshot.SourceProfile != "backend" || len(snapshot.Files) == 0 || len(snapshot.Files) > 20000 {
		return nil, s.Reject("HISTORY_UNSUPPORTED", "不支持的原治理快照；不重解释旧记录")
	}
	args := map[string]string{}
	for k, v := range s.Args {
		args[k] = v
	}
	args["profile"] = snapshot.SourceProfile
	child := NewSemanticSession(s.Ctx, s.Root, args)
	child.V.Virtual = map[string]semanticArchiveFile{}
	total := 0
	for ref, want := range snapshot.Files {
		if err = safefs.ValidateRef(ref); err != nil {
			return nil, err
		}
		if want.Type != "file" {
			return nil, s.Reject("HISTORY_UNSUPPORTED", "原快照仅接受普通文件")
		}
		stored := path.Join(path.Dir(h.Ref), "files", identitymeta.HistoryFile(ref))
		b, err := s.Bytes(stored)
		if err != nil {
			return nil, err
		}
		descriptor, err := s.V.Watch(s.LocalRef(stored))
		if err != nil {
			return nil, err
		}
		if descriptor != want || safefs.Digest(b) != want.Digest {
			return nil, s.Reject("HISTORY_SOURCE_DRIFT", "原快照文件变化: "+ref)
		}
		total += len(b)
		if total > 128*1024*1024 {
			return nil, s.Reject("HISTORY_UNSUPPORTED", "原快照超过 128 MiB")
		}
		child.V.Virtual[ref] = semanticArchiveFile{Data: b, Mode: want.Mode}
		child.Aliases[ref] = ref
	}
	if err = child.Authorities(); err != nil {
		return nil, err
	}
	if child.GovernanceVersion != 1 || child.Report.Profile != snapshot.SourceProfile {
		return nil, s.Reject("HISTORY_UNSUPPORTED", "原身份与原政策不属于已声明旧版本")
	}
	originalMetadata, err := child.Bytes(domain.MetadataFile)
	if err != nil {
		return nil, err
	}
	original, err := identitymeta.ValidateNative(s.Root, originalMetadata, snapshot.SourceProfile)
	if err != nil {
		return nil, err
	}
	if original.SchemaVersion != 3 {
		return nil, s.Reject("HISTORY_UNSUPPORTED", "原治理快照必须保留其 metadata3 原文")
	}
	child.Children = append(child.Children, s)
	SemanticCoverage(child, "historical_source", map[string]any{"ref": h.Ref, "digest": h.Digest, "profile": snapshot.SourceProfile, "current_qualification": false, "read_only": true})
	return child, nil
}
