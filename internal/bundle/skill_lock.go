package bundle

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/iloveZzz/yss-cli/internal/safefs"
	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

// IgnoredSkillFile is shared by tree hashing and planning; local caches are
// preserved explicitly without changing a fixed Skill's source digest.
func IgnoredSkillFile(name string) bool {
	for _, part := range strings.Split(name, "/") {
		if part == ".DS_Store" || part == "__pycache__" {
			return true
		}
	}
	return strings.HasSuffix(name, ".iml") || strings.HasSuffix(name, ".pyc") || strings.HasSuffix(name, ".pyo")
}

// SkillTreeHash is the Node supply-chain normalized tree contract: relative
// names in English collation order, NUL separators, and CRLF normalization for
// text. File mode is protected separately by transaction descriptors.
func SkillTreeHash(files map[string]File, prefix string, vars map[string]string) (string, error) {
	refs := []string{}
	for ref := range files {
		if strings.HasPrefix(ref, prefix+"/") {
			refs = append(refs, ref)
		}
	}
	return hashSkillTree(files, prefix, refs, vars)
}

// Only known runtime roots are indexed. Presence includes ignored files so an
// existing empty projection still fails rather than masquerading as omitted.
type skillTreeIndex map[string][]string

func indexSkillTrees(files map[string]File) skillTreeIndex {
	index := skillTreeIndex{}
	for ref := range files {
		parts := strings.SplitN(ref, "/", 4)
		if len(parts) != 4 || parts[1] != "skills" {
			continue
		}
		switch parts[0] {
		case ".agents", ".codex", ".cursor", ".pi", ".claude":
			prefix := strings.Join(parts[:3], "/")
			index[prefix] = append(index[prefix], ref)
		}
	}
	return index
}

func hashSkillTree(files map[string]File, prefix string, entries []string, vars map[string]string) (string, error) {
	if err := safefs.ValidateRef(prefix); err != nil {
		return "", err
	}
	refs := make([]string, 0, len(entries))
	for _, ref := range entries {
		if IgnoredSkillFile(strings.TrimPrefix(ref, prefix+"/")) {
			continue
		}
		if err := safefs.ValidateRef(ref); err != nil {
			return "", err
		}
		refs = append(refs, ref)
	}
	if len(refs) == 0 {
		return "", fmt.Errorf("distributed Skill tree missing: %s", prefix)
	}
	order := collate.New(language.English)
	sort.Slice(refs, func(i, j int) bool {
		left, right := strings.TrimPrefix(refs[i], prefix+"/"), strings.TrimPrefix(refs[j], prefix+"/")
		if n := order.CompareString(left, right); n != 0 {
			return n < 0
		}
		return left < right
	})
	h := sha256.New()
	for _, ref := range refs {
		f := files[ref]
		b, err := base64.StdEncoding.DecodeString(f.Data)
		if err != nil || safefs.Digest(b) != f.Digest {
			return "", fmt.Errorf("distributed Skill file digest invalid: %s", ref)
		}
		if !bytes.ContainsRune(b, 0) {
			for key, token := range map[string]string{"projectName": projectToken, "businessDomain": domainToken, "teamSize": teamToken} {
				if bytes.Contains(b, []byte(token)) {
					if _, bound := vars[key]; !bound {
						return "", fmt.Errorf("Skill token requires final instance variable %s: %s", key, ref)
					}
				}
			}
			if vars != nil {
				b = renderDecoded(b, vars)
			}
			b = bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n"))
		}
		h.Write([]byte(strings.TrimPrefix(ref, prefix+"/")))
		h.Write([]byte{0})
		h.Write(b)
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// RenderedSkillLock binds only effectiveHash to the distributed trees. Source
// revisions, upstream hashes, unknown metadata and canonical ordering survive.
func RenderedSkillLock(data []byte, files map[string]File, vars map[string]string) ([]byte, error) {
	root, err := decodeOrdered(data)
	if err != nil {
		return nil, err
	}
	if root.kind != 'o' || root.values["skills"] == nil || root.values["skills"].kind != 'o' {
		return nil, fmt.Errorf("invalid skills lock")
	}
	if version := root.values["version"]; version != nil && (version.kind != 0 || version.scalar != json.Number("3")) {
		return nil, fmt.Errorf("unsupported skills lock version")
	}
	section := root.values["skills"]
	shared := section.values["shared"]
	if shared == nil || shared.kind != 'o' {
		return nil, fmt.Errorf("invalid shared Skill section")
	}
	index := indexSkillTrees(files)
	for _, name := range shared.keys {
		entry := shared.values[name]
		if entry.kind != 'o' || path.Base(name) != name {
			return nil, fmt.Errorf("invalid shared Skill entry: %s", name)
		}
		prefix := ".agents/skills/" + name
		digest, err := hashSkillTree(files, prefix, index[prefix], vars)
		if err != nil {
			return nil, err
		}
		if targets := entry.values["targets"]; targets != nil {
			if targets.kind != 'a' {
				return nil, fmt.Errorf("invalid Skill targets: %s", name)
			}
			for _, target := range targets.array {
				ref, ok := target.scalar.(string)
				if !ok || target.kind != 0 || (ref != ".agents/skills" && ref != ".codex/skills" && ref != ".cursor/skills" && ref != ".pi/skills" && ref != ".claude/skills") {
					return nil, fmt.Errorf("invalid Skill target root: %s", name)
				}
				if ref == ".agents/skills" {
					continue
				}
				projection := ref + "/" + name
				if _, present := index[projection]; !present {
					continue // Source policy may omit an entire runtime projection.
				}
				actual, err := hashSkillTree(files, projection, index[projection], vars)
				if err != nil || actual != digest {
					return nil, fmt.Errorf("distributed Skill projection disagrees: %s", projection)
				}
			}
		}
		entry.set("effectiveHash", &orderedJSON{scalar: digest})
	}
	if platform := section.values["platform"]; platform != nil {
		if platform.kind != 'o' {
			return nil, fmt.Errorf("invalid platform Skill section")
		}
		for _, ref := range platform.keys {
			entries := platform.values[ref]
			if entries.kind != 'o' || (ref != ".codex/skills" && ref != ".cursor/skills" && ref != ".pi/skills" && ref != ".claude/skills") {
				return nil, fmt.Errorf("invalid platform Skill root: %s", ref)
			}
			for _, name := range entries.keys {
				entry := entries.values[name]
				if entry.kind != 'o' || path.Base(name) != name {
					return nil, fmt.Errorf("invalid platform Skill entry: %s/%s", ref, name)
				}
				prefix := ref + "/" + name
				digest, err := hashSkillTree(files, prefix, index[prefix], vars)
				if err != nil {
					return nil, err
				}
				entry.set("effectiveHash", &orderedJSON{scalar: digest})
			}
		}
	}
	return orderedBytes(root)
}

func bindBuiltSkillLock(files map[string]File) error {
	f, exists := files["skills-lock.json"]
	if !exists {
		return nil
	}
	raw, err := base64.StdEncoding.DecodeString(f.Data)
	if err != nil {
		return err
	}
	raw, err = RenderedSkillLock(raw, files, nil)
	if err != nil {
		return err
	}
	f.Data, f.Digest = base64.StdEncoding.EncodeToString(raw), safefs.Digest(raw)
	files["skills-lock.json"] = f
	return nil
}
