package project

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/iloveZzz/yss-cli/internal/bundle"
	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/governance"
	"github.com/iloveZzz/yss-cli/internal/identitymeta"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/schema"
	"github.com/iloveZzz/yss-cli/internal/transaction"
	"github.com/iloveZzz/yss-cli/internal/trustguard"
)

// ValidateMigrationRollbackForTransaction permits rollback only if the locked
// transaction's original instance is independently bound to a trusted Bundle.
func ValidateMigrationRollbackForTransaction(root, transactionID string) error {
	archive, err := transaction.OpenCandidateArchive(root, transactionID)
	if err != nil {
		return err
	}
	raw, before, found, err := archive.Original(MetadataFile)
	if err != nil {
		return err
	}
	if !found || before.Type != "file" {
		return domain.Fail("DOWNGRADE_UNSUPPORTED", "迁移原身份缺失，不能证明回滚来源")
	}
	meta, err := identitymeta.ValidateNative(root, raw, "")
	if err != nil {
		return domain.Fail("DOWNGRADE_UNSUPPORTED", "迁移原身份不受支持")
	}
	source, err := bundle.LoadTrustedSource(bundle.SourceIdentity{Profile: meta.Profile, Version: meta.TemplateVersion, Commit: meta.TemplateCommit, Snapshot: meta.SnapshotHash, Manifest: meta.ManifestHash, Bundle: meta.BundleHash, State: meta.TemplateSourceState})
	if err != nil || source.SchemaVersion != meta.BundleSchemaVersion {
		return domain.Fail("DOWNGRADE_UNSUPPORTED", "迁移原来源不属于受信 Bundle，保留现场")
	}
	guard, err := trustguard.Open(meta)
	if err != nil {
		return err
	}
	// Recovery restores pre-operation bytes. Authenticate every fixed policy
	// that existed before this transaction, including in a skills-only project.
	for _, ref := range archive.Paths() {
		if !recoveryPolicy(ref) {
			continue
		}
		_, d, found, e := archive.Original(ref)
		if e != nil {
			return e
		}
		if !found {
			return domain.Fail("BASELINE", "恢复原政策未登记: "+ref)
		}
		if d.Type == "missing" || d.Type == "directory" {
			continue
		}
		if d.Type != "file" {
			return domain.Fail("BASELINE", "恢复原政策类型不支持: "+ref)
		}
		if e = guard.VerifyPolicy(ref, d); e != nil {
			return e
		}
	}
	if meta.SchemaVersion == 5 && meta.Distribution["installation"] == "skills" {
		verified := false
		for ref, managed := range meta.Managed {
			if !strings.HasPrefix(ref, ".agents/skills/") || !strings.HasSuffix(ref, "/SKILL.md") || managed.Ownership != "managed" {
				continue
			}
			_, d, exists, e := archive.Original(ref)
			if e != nil {
				return e
			}
			if !exists {
				d, e = safefs.Describe(root, ref)
				if e != nil {
					return e
				}
			}
			if guard.VerifyPolicy(ref, d) == nil {
				verified = true
				break
			}
		}
		if !verified {
			return domain.Fail("DOWNGRADE_UNSUPPORTED", "独立技能原态缺少与受信 Bundle 一致的技能文件")
		}
	} else {
		identity, d, exists, e := archive.Original("yss-project.yaml")
		if e != nil {
			return e
		}
		if !exists {
			p, e := safefs.Path(root, "yss-project.yaml")
			if e != nil {
				return e
			}
			identity, e = os.ReadFile(p)
			if e != nil {
				return e
			}
			d, e = safefs.Describe(root, "yss-project.yaml")
			if e != nil {
				return e
			}
		}
		if d.Type != "file" || safefs.Digest(identity) != d.Digest {
			return domain.Fail("IDENTITY", "迁移原根身份不可验证")
		}
		parsed, e := schema.Parse(identity)
		if e != nil {
			return e
		}
		m, ok := object(parsed)
		version := number(m["schema_version"])
		if !ok || !exactVersion(m["schema_version"], version) || text(m["repository_mode"]) != "project-instance" || (meta.SchemaVersion >= 4 && version != 2) || (meta.SchemaVersion < 4 && version != 1) {
			return domain.Fail("IDENTITY", "迁移原根身份与 metadata 不一致")
		}
	}
	refs := []string{}
	if meta.SchemaVersion < 5 || meta.Distribution["installation"] == "governed" {
		refs = append(refs, ".template-spec/process/harness-profile.yaml")
	}
	workflow, err := identitymeta.SpecWorkflow(meta)
	if err != nil {
		return err
	}
	if workflow != "" {
		refs = append(refs, SpecWorkflowRef)
	}
	if meta.Profile == "spec" && meta.SchemaVersion >= 5 && meta.Distribution["installation"] == "governed" {
		view, e := source.ForInstallation("governed")
		if e != nil {
			return e
		}
		if text(view.Distribution["specWorkflow"]) != workflow {
			return domain.Fail("DOWNGRADE_UNSUPPORTED", "迁移原工作流声明与受信 Bundle 不一致")
		}
	}
	for _, ref := range refs {
		_, d, exists, err := archive.Original(ref)
		if err != nil {
			return err
		}
		if !exists {
			d, err = safefs.Describe(root, ref)
			if err != nil {
				return err
			}
		}
		if err = guard.VerifyPolicy(ref, d); err != nil {
			return err
		}
	}
	if meta.GovernanceHistory != nil {
		ref := meta.GovernanceHistory.Ref
		content, d, exists, err := archive.Original(ref)
		if err != nil {
			return err
		}
		if !exists {
			p, e := safefs.Path(root, ref)
			if e != nil {
				return e
			}
			content, err = os.ReadFile(p)
			if err != nil {
				return err
			}
			d, err = safefs.Describe(root, ref)
			if err != nil {
				return err
			}
		}
		if d.Type != "file" || safefs.Digest(content) != d.Digest {
			return domain.Fail("HISTORY_SOURCE_DRIFT", "历史归档不一致")
		}
		if err = identitymeta.ValidateHistoryProfile(meta.GovernanceHistory, content); err != nil {
			return err
		}
	}
	return nil
}

// verifyRecoverySource checks candidate policy descriptors against a trusted
// Bundle. An unchanged policy is observed on disk; a changed policy uses the
// verified candidate bytes and mode from the transaction archive.
func recoveryCandidate(root, id, ref string) ([]byte, domain.Descriptor, bool, error) {
	if id != "" {
		return transaction.ArchivedCandidateByID(root, id, ref)
	}
	return transaction.ArchivedCandidate(root, ref)
}

func recoveryFile(root, id, ref string) ([]byte, error) {
	b, d, found, err := recoveryCandidate(root, id, ref)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, domain.Fail("ARCHIVE", "事务没有登记候选路径: "+ref)
	}
	if d.Type != "file" {
		return nil, domain.Fail("ARCHIVE", "归档路径没有候选文件: "+ref)
	}
	return b, nil
}

func verifyRecoverySource(root string, meta *identitymeta.Metadata, transactionID string) error {
	guard, err := trustguard.Open(meta)
	if err != nil {
		return err
	}
	refs := []string{}
	if meta.SchemaVersion < 5 || meta.Distribution["installation"] == "governed" {
		refs = append(refs, ".template-spec/process/harness-profile.yaml")
	}
	workflow, err := identitymeta.SpecWorkflow(meta)
	if err != nil {
		return err
	}
	if workflow != "" {
		refs = append(refs, SpecWorkflowRef)
	}
	for _, ref := range refs {
		_, actual, found, err := recoveryCandidate(root, transactionID, ref)
		if err != nil {
			return err
		}
		if !found {
			actual, err = safefs.Describe(root, ref)
			if err != nil {
				return err
			}
		}
		if err = guard.VerifyPolicy(ref, actual); err != nil {
			return err
		}
	}
	return nil
}

// VerifyRecoveryCandidatePolicies is called by the transaction validator while
// the transaction lock is held. Every affected fixed policy must be present in
// the verified candidate archive and match the selected trusted source.
func VerifyRecoveryCandidatePolicies(root string, id *Identity, paths []string, transactionID string) error {
	if id == nil || id.Native == nil {
		return domain.Fail("IDENTITY", "恢复缺少受信实例身份")
	}
	candidate := func(ref string) ([]byte, domain.Descriptor, bool, error) {
		return recoveryCandidate(root, transactionID, ref)
	}
	var archive *transaction.CandidateArchive
	if transactionID != "" {
		var err error
		archive, err = transaction.OpenCandidateArchive(root, transactionID)
		if err != nil {
			return err
		}
		candidate = archive.Candidate
	}
	meta := id.Native
	raw, _, found, err := candidate(MetadataFile)
	if err != nil {
		return err
	}
	if found {
		meta, err = identitymeta.ValidateNative(root, raw, "")
		if err != nil {
			return err
		}
		if meta.Profile != id.Profile.Name {
			return domain.Fail("IDENTITY", "恢复目标 Profile 不匹配")
		}
	}
	guard, err := trustguard.Open(meta)
	if err != nil {
		return err
	}
	var originalGuard *trustguard.Guard
	var originalMeta *identitymeta.Metadata
	if archive != nil && archive.Kind() == "sync" {
		previous := id.Native
		original, desc, found, e := archive.Original(MetadataFile)
		if e != nil {
			return e
		}
		if found {
			if desc.Type != "file" {
				return domain.Fail("IDENTITY", "同步事务缺少原身份")
			}
			previous, e = identitymeta.ValidateNative(root, original, "")
			if e != nil {
				return e
			}
			if previous.Profile != meta.Profile {
				return domain.Fail("IDENTITY", "同步前后 Profile 不一致")
			}
		}
		originalGuard, err = trustguard.Open(previous)
		if err != nil {
			return err
		}
		originalMeta = previous
	}
	for _, ref := range paths {
		if !recoveryPolicy(ref) {
			continue
		}
		var before domain.Descriptor
		if originalGuard != nil {
			_, previous, found, e := archive.Original(ref)
			if e != nil {
				return e
			}
			before = previous
			if !found {
				return domain.Fail("BASELINE", "恢复策略缺少原始归档: "+ref)
			}
			if before.Type == "file" {
				if e = originalGuard.VerifyPolicy(ref, before); e != nil {
					return e
				}
			} else if before.Type != "missing" && before.Type != "directory" {
				return domain.Fail("BASELINE", "恢复原政策类型不支持: "+ref)
			}
		}
		_, actual, found, err := candidate(ref)
		if err != nil {
			return err
		}
		if !found {
			return domain.Fail("BASELINE", "恢复策略缺少归档候选: "+ref)
		}
		if actual.Type == "directory" {
			continue
		}
		if actual.Type == "missing" && originalGuard != nil && before.Type == "file" {
			if err = verifyRecoveryRetirement(meta, originalMeta, ref, guard, archive); err != nil {
				return err
			}
			continue
		}
		if actual.Type != "file" {
			return domain.Fail("BASELINE", "恢复策略没有候选文件: "+ref)
		}
		if err = guard.VerifyPolicy(ref, actual); err != nil {
			return err
		}
	}
	return nil
}

func verifyRecoveryRetirement(target, previous *identitymeta.Metadata, ref string, guard *trustguard.Guard, archive *transaction.CandidateArchive) error {
	b, err := bundle.LoadTrustedSource(bundle.SourceIdentity{Profile: target.Profile, Version: target.TemplateVersion, Commit: target.TemplateCommit, Snapshot: target.SnapshotHash, Manifest: target.ManifestHash, Bundle: target.BundleHash, State: target.TemplateSourceState})
	if err != nil {
		return err
	}
	return verifyRecoveryRetirementInBundle(target, previous, ref, guard, archive, b)
}

func verifyRecoveryRetirementInBundle(target, previous *identitymeta.Metadata, ref string, guard *trustguard.Guard, archive *transaction.CandidateArchive, b *bundle.Bundle) error {
	if _, exists := target.Managed[ref]; exists {
		return domain.Fail("BASELINE", "目标身份仍登记退役策略: "+ref)
	}
	if _, exists := b.Files[ref]; exists || b.Upgrade == nil {
		return domain.Fail("BASELINE", "目标来源未退役策略: "+ref)
	}
	matcher := upgradePlanner{id: &Identity{Profile: domain.Profiles[previous.Profile], Native: previous}}
	for _, rule := range b.Upgrade.Rules {
		if rule.SourcePath != ref || rule.From.Profile != previous.Profile || (rule.Action != "delete" && rule.Action != "rename") {
			continue
		}
		matched, e := matcher.matchesSource(rule.From)
		if e != nil {
			return e
		}
		if !matched {
			continue
		}
		if rule.Action == "rename" {
			_, after, found, e := archive.Candidate(rule.TargetPath)
			if e != nil {
				return e
			}
			if !found || after.Type != "file" {
				return domain.Fail("BASELINE", "改名目标缺少可信候选: "+rule.TargetPath)
			}
			if e = guard.VerifyPolicy(rule.TargetPath, after); e != nil {
				return e
			}
		}
		return nil
	}
	return domain.Fail("BASELINE", "目标来源缺少匹配的策略退役规则: "+ref)
}

func recoveryPolicy(ref string) bool {
	if ref == ".template-spec/process/reading-policy.yaml" {
		return false
	}
	if trustguard.IsPolicy(ref) {
		return true
	}
	// Process assets may contain additional fixed policies or templates. Unknown
	// paths are rejected rather than guessing; these named files are local CI
	// configuration and its transaction receipt, not Bundle policy.
	if strings.HasPrefix(ref, ".template-spec/process/") {
		switch ref {
		case ".template-spec/process/reading-policy.yaml", ".template-spec/process/project-ci.yaml", ".template-spec/process/project-ci-native.json", ".template-spec/process/project-ci-native-install.json", ".template-spec/process/.project-ci-transaction.json":
			return false
		}
		return true
	}
	return false
}

// RecoveryIdentity uses the bound native metadata archive when init/attach was
// interrupted before metadata installation. Conflicting existing identities
// remain errors; recovery never guesses a Profile from a directory name.
func RecoveryIdentity(root, explicit string) (*Identity, error) {
	return RecoveryIdentityForTransaction(root, explicit, "")
}

func RecoveryIdentityForTransaction(root, explicit, transactionID string) (*Identity, error) {
	if err := rejectRemovedDesign(root); err != nil {
		return nil, err
	}
	id, original := Detect(root, explicit, false)
	if original == nil {
		status, err := transaction.Status(root)
		if err != nil {
			return nil, err
		}
		if len(status.Pending) > 0 {
			if len(status.Pending) != 1 {
				return nil, domain.Fail("AMBIGUOUS", "多个未完成事务不能推断恢复来源")
			}
			candidate := id.Native
			raw, _, found, err := recoveryCandidate(root, transactionID, MetadataFile)
			if err != nil {
				return nil, err
			}
			if found {
				candidate, err = identitymeta.ValidateNative(root, raw, "")
				if err != nil {
					return nil, err
				}
				if candidate.Profile != id.Profile.Name {
					return nil, domain.Fail("IDENTITY", "恢复目标与已安装 Profile 不匹配")
				}
			}
			if err = verifyRecoverySource(root, candidate, transactionID); err != nil {
				return nil, err
			}
		}
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
		if e == nil {
			if migrated, err := migrationRecoveryIdentity(root, explicit, transactionID); err == nil {
				return migrated, nil
			}
		}
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
		if !ok || (number(m["schema_version"]) != 1 && number(m["schema_version"]) != 2) || text(m["repository_mode"]) != "project-instance" {
			return nil, original
		}
	} else if !os.IsNotExist(e) {
		return nil, e
	}
	b, e := recoveryFile(root, transactionID, MetadataFile)
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
	if e = verifyRecoverySource(root, &m, transactionID); e != nil {
		return nil, e
	}
	if m.GovernanceHistory != nil {
		raw, err := recoveryFile(root, transactionID, m.GovernanceHistory.Ref)
		if err != nil {
			return nil, err
		}
		if err = identitymeta.ValidateHistoryProfile(m.GovernanceHistory, raw); err != nil {
			return nil, err
		}
	}
	p, e := domain.GetProfile(m.Profile)
	if e != nil {
		return nil, e
	}
	if (m.SchemaVersion < 1 || m.SchemaVersion > 5) || m.ProtocolVersion != domain.ProtocolVersion || m.ProfileID != p.ID || !identitymeta.ValidTemplateSource(&m) || !digestPattern.MatchString(m.SnapshotHash) || !digestPattern.MatchString(m.ManifestHash) {
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
		archived, e := recoveryFile(root, transactionID, legacy.Metadata)
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
	installed := id.Native != nil
	if !installed && explicit == "" {
		return transaction.Result{}, true, domain.Fail("IDENTITY", "空初始化准备必须显式指定 Profile")
	}
	descriptors := map[string]domain.Descriptor{}
	for _, ref := range []string{MetadataFile, "yss-project.yaml", id.ProfileRef} {
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
		if (current.Native != nil) != installed {
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
			return domain.Fail("PROFILE_REMOVED", "Spec→Design 基线事务已移除")
		case "lifecycle-target":
			return governance.ValidateProgressionTransaction(id.Root, id.Profile.Name, paths)
		case "sync":
			return domain.Fail("KIND", "同步不支持恢复；请检查新增准备材料并手动处理")
		case "init", "attach", "migrate", "skills", "assets", "abandoned-preparation":
			return nil
		default:
			return domain.Fail("KIND", "未知准备事务 kind，拒绝封存")
		}
	}
	return transaction.RecoverPreparations(ctx, id.Root, id.Profile.Name, apply, validate)
}
