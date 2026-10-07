package bundle

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"maps"
	"slices"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/safefs"
)

const packedFilename = "bundles.json.gz"
const storageFormatVersion = 1
const MaxArchiveBytes = 10_000_000

// StorageReport describes physical storage, independently of logical Bundle hashes.
type StorageReport struct {
	StorageFormatVersion int    `json:"storageFormatVersion"`
	Filename             string `json:"filename"`
	SHA256               string `json:"sha256"`
	CompressedBytes      int    `json:"compressedBytes"`
	JSONBytes            int    `json:"jsonBytes"`
	ObjectCount          int    `json:"objectCount"`
}

type packedArchive struct {
	StorageFormatVersion int                        `json:"storageFormatVersion"`
	Profiles             map[string]json.RawMessage `json:"profiles"`
	Objects              map[string]string          `json:"objects"`
}

// Only the content-bearing containers are copied; metadata is read, never edited.
func packBundles(bundles map[string]*Bundle) ([]byte, *StorageReport, error) {
	a := packedArchive{StorageFormatVersion: storageFormatVersion, Profiles: map[string]json.RawMessage{}, Objects: map[string]string{}}
	for _, profile := range sortedKeys(bundles) {
		if _, err := domain.GetProfile(profile); err != nil {
			return nil, nil, err
		}
		original := bundles[profile]
		if original == nil {
			return nil, nil, domain.Fail("BUNDLE", "缺少逻辑 Bundle: "+profile)
		}
		b := *original
		b.Files, b.Initial = maps.Clone(b.Files), maps.Clone(b.Initial)
		b.NativeTransforms = slices.Clone(b.NativeTransforms)
		if err := validate(&b, profile); err != nil {
			return nil, nil, err
		}
		if b.SchemaVersion < 2 || b.BundleHash != contentHash(&b) {
			return nil, nil, domain.Fail("BUNDLE", "Bundle 摘要不一致: "+profile)
		}
		err := mapFileContent(&b, func(ref string, f File) (File, error) {
			if previous, exists := a.Objects[f.Digest]; exists {
				if previous != f.Data {
					return f, domain.Fail("BUNDLE", "同摘要内容冲突: "+ref)
				}
			} else {
				data, err := base64.StdEncoding.DecodeString(f.Data)
				if err != nil || safefs.Digest(data) != f.Digest {
					return f, domain.Fail("BUNDLE", "内容对象摘要不一致: "+ref)
				}
				a.Objects[f.Digest] = f.Data
			}
			f.Data = ""
			return f, nil
		})
		if err != nil {
			return nil, nil, err
		}
		a.Profiles[profile], err = json.Marshal(&b)
		if err != nil {
			return nil, nil, err
		}
	}
	if len(a.Profiles) == 0 {
		return nil, nil, domain.Fail("BUNDLE", "不能生成空归档")
	}
	raw, err := json.Marshal(&a)
	if err != nil {
		return nil, nil, err
	}
	if len(raw) > snapshotLimit {
		return nil, nil, domain.Fail("BUNDLE", "快照超过限制")
	}
	// Verify the real reader before replacing any asset.
	for _, profile := range sortedKeys(bundles) {
		if _, err := loadPacked(raw, profile); err != nil {
			return nil, nil, err
		}
	}
	var buf bytes.Buffer
	z, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		return nil, nil, err
	}
	z.Header.OS = 255
	if _, err = z.Write(raw); err != nil {
		return nil, nil, err
	}
	if err = z.Close(); err != nil {
		return nil, nil, err
	}
	compressed := buf.Bytes()
	if len(compressed) > MaxArchiveBytes {
		return nil, nil, domain.Fail("BUNDLE", "模板归档超过 10,000,000 字节")
	}
	return compressed, &StorageReport{StorageFormatVersion: storageFormatVersion, Filename: packedFilename, SHA256: safefs.Digest(compressed), CompressedBytes: len(compressed), JSONBytes: len(raw), ObjectCount: len(a.Objects)}, nil
}

func loadPacked(raw []byte, profile string) (*Bundle, error) {
	if _, err := domain.GetProfile(profile); err != nil {
		return nil, err
	}
	var a packedArchive
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, domain.Wrap("BUNDLE", err)
	}
	if a.StorageFormatVersion != storageFormatVersion || a.Profiles == nil || a.Objects == nil {
		return nil, domain.Fail("BUNDLE", "内嵌存储格式不支持或不完整")
	}
	metadata, exists := a.Profiles[profile]
	if !exists {
		return nil, domain.Fail("BUNDLE", "内嵌归档缺少 Profile: "+profile)
	}
	var b Bundle
	if err := json.Unmarshal(metadata, &b); err != nil {
		return nil, domain.Wrap("BUNDLE", err)
	}
	if err := mapFileContent(&b, func(ref string, f File) (File, error) {
		data, exists := a.Objects[f.Digest]
		if !exists || f.Data != "" {
			return f, domain.Fail("BUNDLE", "内容引用缺失或包含内联内容: "+ref)
		}
		f.Data = data
		return f, nil
	}); err != nil {
		return nil, err
	}
	if err := validate(&b, profile); err != nil {
		return nil, err
	}
	if b.SchemaVersion < 2 || b.BundleHash != contentHash(&b) {
		return nil, domain.Fail("BUNDLE", "Bundle 摘要不一致")
	}
	return &b, nil
}

func mapFileContent(b *Bundle, transform func(string, File) (File, error)) error {
	for _, files := range []map[string]File{b.Files, b.Initial} {
		for ref, f := range files {
			changed, err := transform(ref, f)
			if err != nil {
				return err
			}
			files[ref] = changed
		}
	}
	for i, t := range b.NativeTransforms {
		changed, err := transform(t.Path, t.Source)
		if err != nil {
			return err
		}
		b.NativeTransforms[i].Source = changed
	}
	return nil
}
