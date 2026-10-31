package bundle

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"regexp"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/schema"
)

type trustedArchive struct {
	Version        string `json:"version"`
	Commit         string `json:"commit"`
	SourceLock     string `json:"sourceLock"`
	ProducerCommit string `json:"producerCommit"`
	ArchiveSHA256  string `json:"archiveSha256"`
	RawSHA256      string `json:"rawSha256"`
	DeltaFile      string `json:"deltaFile"`
	DeltaSHA256    string `json:"deltaSha256"`
}

var trustedCommitPattern = regexp.MustCompile(`^[a-f0-9]{40}$`)
var trustedDigestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
var trustedVersionPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

// Historical snapshots are independently pinned by the committed registry and
// reconstructed from the current archive and the matching embedded delta.
func loadTrustedArchive(files fs.FS, current []byte, source SourceIdentity) (*Bundle, error) {
	if files == nil {
		return nil, domain.Fail("BASELINE", "受信来源登记不可用")
	}
	raw, err := fs.ReadFile(files, "assets/trusted/registry.json")
	if err != nil {
		return nil, domain.Wrap("BUNDLE", err)
	}
	if _, err = schema.Parse(raw); err != nil {
		return nil, domain.Wrap("BUNDLE", err)
	}
	var registry struct {
		SchemaVersion int              `json:"schemaVersion"`
		Archives      []trustedArchive `json:"archives"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&registry); err != nil || registry.SchemaVersion != 1 || registry.Archives == nil {
		return nil, domain.Fail("BUNDLE", "受信来源登记无效")
	}
	seen := map[string]bool{}
	var selected *trustedArchive
	for i := range registry.Archives {
		entry := &registry.Archives[i]
		key := entry.Version
		if !trustedVersionPattern.MatchString(entry.Version) || !IsSemver(entry.Version) || !(VersionRange{Min: "5.0.0"}).Contains(entry.Version) || !trustedCommitPattern.MatchString(entry.Commit) || entry.Commit == retiredTemplateCommit || !trustedCommitPattern.MatchString(entry.ProducerCommit) || !trustedDigestPattern.MatchString(entry.ArchiveSHA256) || !trustedDigestPattern.MatchString(entry.RawSHA256) || !trustedDigestPattern.MatchString(entry.DeltaSHA256) || entry.SourceLock != "docs/trusted-sources/"+entry.Version+".json" || entry.DeltaFile != entry.Version+".delta.gz" || seen[key] {
			return nil, domain.Fail("BUNDLE", "受信来源登记包含无效或重复条目")
		}
		seen[key] = true
		if entry.Version == source.Version && entry.Commit == source.Commit {
			selected = entry
		}
	}
	if selected == nil {
		return nil, domain.Fail("BASELINE", "实例来源不在受信 Bundle 登记中")
	}
	delta, err := fs.ReadFile(files, "assets/trusted/"+selected.DeltaFile)
	if err != nil {
		return nil, domain.Wrap("BUNDLE", err)
	}
	digest := sha256.Sum256(delta)
	if hex.EncodeToString(digest[:]) != selected.DeltaSHA256 {
		return nil, domain.Fail("BUNDLE", "受信 Bundle 差量摘要不符")
	}
	baseline, err := reconstructBaseline(current, delta, selected.RawSHA256)
	if err != nil {
		return nil, err
	}
	b, err := loadPacked(baseline, source.Profile)
	if err != nil {
		return nil, err
	}
	if !supportedProduct(b) || !MatchesSource(b, source) {
		return nil, domain.Fail("BASELINE", "受信 Bundle 与实例来源不匹配")
	}
	return b, nil
}
