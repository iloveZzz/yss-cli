package scaffold

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
)

const (
	dddSkill = "yss-ddd-scaffold-generator"
	mvcSkill = "yss-layered-mvc-scaffold-generator"
	wireRef  = ".agents/skills/yss-dto/references/openapi-wire-profile.yaml"
)

func skillRoot(family string) string {
	if family == "domain-driven" {
		return ".agents/skills/" + dddSkill
	}
	return ".agents/skills/" + mvcSkill
}

// sourceFileMap lists the files below refs with their digest and executable bit,
// keyed by path and ordered like Object.entries(...).sort(localeCompare).
func sourceFileMap(src Source, refs []string) (*OMap, error) {
	files := map[string]*OMap{}
	for _, ref := range refs {
		var list []string
		if src.Has(ref) {
			list = []string{ref}
		} else {
			for _, rel := range src.Walk(ref) {
				list = append(list, ref+"/"+rel)
			}
			if len(list) == 0 {
				return nil, failf("platform-provenance: 固定工具字节缺失: %s", ref)
			}
		}
		for _, path := range list {
			data, err := src.Read(path)
			if err != nil {
				return nil, err
			}
			mode, _ := src.Mode(path)
			files[path] = newMap("digest", sha256Ref(data), "executable", mode&0o111 != 0)
		}
	}
	return sortedMap(files), nil
}

func sortedMap(entries map[string]*OMap) *OMap {
	keys := make([]string, 0, len(entries))
	for key := range entries {
		keys = append(keys, key)
	}
	sortLocale(keys)
	out := NewOMap()
	for _, key := range keys {
		out.Set(key, entries[key])
	}
	return out
}

// platformSourceFingerprint binds the generator and verifier sources, as
// platformSourceFingerprint(family) in backend-platform-provenance.mjs does.
func platformSourceFingerprint(src Source, family string) (*OMap, error) {
	base := skillRoot("domain-driven")
	refs := []string{base + "/scripts/generate_scaffold.mjs", base + "/assets"}
	if family != "domain-driven" {
		if family != "layered-mvc" {
			return nil, fail("platform-provenance: unknown architecture")
		}
		base = skillRoot(family)
		refs = []string{base + "/scripts/generate_scaffold.mjs", base + "/assets", skillRoot("domain-driven") + "/assets/wrapper"}
	}
	refs = append(refs, "scripts/lib/backend-platform.mjs", "scripts/lib/scaffold-local-database.mjs", "scripts/lib/standalone-backend-scaffold.mjs", "scripts/lib/scaffold-reference-example.mjs", wireRef)
	if family == "layered-mvc" {
		refs = append(refs, skillRoot("domain-driven")+"/assets/reference")
	}
	generator, err := sourceFileMap(src, refs)
	if err != nil {
		return nil, err
	}
	verifier, err := sourceFileMap(src, []string{"scripts/lib/backend-platform-provenance.mjs", "scripts/lib/backend-platform-verification.mjs", "scripts/lib/command-runner.mjs", "scripts/vendor/xml.mjs", skillRoot("domain-driven") + "/scripts/run_scaffold_verification.mjs"})
	if err != nil {
		return nil, err
	}
	return newMap(
		"schema_version", 1,
		"generator_digest", platformDigest(JSON(generator, "")),
		"verifier_digest", platformDigest(JSON(verifier, "")),
	), nil
}

// generatedTreeDigest hashes the generated files' digests and executable bits.
func generatedTreeDigest(tree *Tree) string {
	entries := map[string]*OMap{}
	for _, path := range tree.Paths() {
		if path == ".yss/scaffold-generation.json" {
			continue
		}
		entries[path] = newMap("digest", sha256Ref(tree.Data(path)), "executable", tree.Mode(path)&0o111 != 0)
	}
	return platformDigest(JSON(sortedMap(entries), ""))
}

// sourceTreeDigest is treeDigest(directory): a SHA-256 over relative path and
// content of every file below prefix, siblings ordered by localeCompare.
func sourceTreeDigest(src Source, prefix string) (string, error) {
	hash := sha256.New()
	for _, rel := range orderedPaths(src.Walk(prefix), localeCompare) {
		data, err := src.Read(prefix + "/" + rel)
		if err != nil {
			return "", err
		}
		hash.Write([]byte(rel))
		hash.Write([]byte{0})
		hash.Write(data)
		hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// generatedFiles lists the project's files (except the manifest) the way the
// generators' fileEntries does, with their content digests.
func generatedFiles(tree *Tree, skipGit bool) []any {
	paths := make([]string, 0, len(tree.files))
	for path := range tree.files {
		if path == ".yss/scaffold-generation.json" {
			continue
		}
		if skipGit && containsGit(path) {
			continue
		}
		paths = append(paths, path)
	}
	out := make([]any, 0, len(paths))
	for _, path := range orderedPaths(paths, localeCompare) {
		out = append(out, newMap("path", path, "owner", "generator", "sha256", sha256Hex(tree.Data(path))))
	}
	return out
}

func containsGit(path string) bool {
	for _, part := range strings.Split(path, "/") {
		if part == ".git" {
			return true
		}
	}
	return false
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
